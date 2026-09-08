// Command racerand-bench measures racerand against the standard library
// generators and a timing-jitter source, and writes a Markdown report.
//
// Usage:
//
//	racerand-bench [-out BENCHMARKS.md] [-json bench.json] [-noise dir] [-bytes N] [-dur 1s] [-samples N] [-gobench file] [-quick]
//	racerand-bench -from bench.json -noise-only          # regenerate the report and images from saved results
package main

import (
	"bytes"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	mrand1 "math/rand"
	mrand "math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/yohimik/racerand"
	"github.com/yohimik/racerand/internal/jitter"
	"github.com/yohimik/racerand/stattest"
)

type Machine struct {
	OS, Arch, CPU string
	NumCPU        int
	GOMAXPROCS    int
	GoVersion     string
}

type ThroughputRow struct {
	Name   string
	Family string
	// MBps64 and MBps64K are megabytes per second for 64-byte and 64 KiB
	// reads.
	MBps64  float64
	MBps64K float64
	Err     string
}

type Uint64Row struct {
	Name    string
	NsPerOp float64
	Err     string
}

type QualityRow struct {
	Name   string
	Report stattest.Report
	NIST   []stattest.TestResult
	Passed int
	Total  int
	Err    string
}

type SweepRow struct {
	Label       string
	Source      string
	Feedback    bool
	Workers     int
	Procs       int
	Samples     int
	NsPerSample float64
	MCV         float64 // bits per 64-bit sample
	Cond        float64 // conditional min-entropy, bits per sample
	Shannon     float64
	FoldMCV     float64 // bits per folded byte
	FoldShannon float64
	RateMbit    float64 // MCV min-entropy per second, Mbit/s
	SeedMicros  float64 // time to harvest a seed at the default assumption
	CalMicros   float64 // time to harvest a seed if MCV were the assumption
	NewErr      string
}

type LatencyRow struct {
	Name   string
	Micros float64
	Note   string
	Err    string
}

type Result struct {
	Machine    Machine
	Generated  time.Time
	Config     map[string]any
	Throughput []ThroughputRow
	Uint64     []Uint64Row
	Quality    []QualityRow
	Workers    []SweepRow
	Variants   []SweepRow
	Procs      []SweepRow
	Latency    []LatencyRow
	Noise      []NoiseRow
	GoBench    string
}

type generator struct {
	name   string
	family string
	open   func() (io.Reader, func(), error)
	u64    func() (func() uint64, func(), error)
}

type sourceReader struct{ next func() uint64 }

func (s sourceReader) Read(p []byte) (int, error) {
	i := 0
	for ; i+8 <= len(p); i += 8 {
		v := s.next()
		p[i] = byte(v)
		p[i+1] = byte(v >> 8)
		p[i+2] = byte(v >> 16)
		p[i+3] = byte(v >> 24)
		p[i+4] = byte(v >> 32)
		p[i+5] = byte(v >> 40)
		p[i+6] = byte(v >> 48)
		p[i+7] = byte(v >> 56)
	}
	if i < len(p) {
		v := s.next()
		for ; i < len(p); i++ {
			p[i] = byte(v)
			v >>= 8
		}
	}
	return len(p), nil
}

func rr(opts ...racerand.Option) func() (io.Reader, func(), error) {
	return func() (io.Reader, func(), error) {
		r, err := racerand.New(opts...)
		if err != nil {
			return nil, nil, err
		}
		return r, func() { r.Close() }, nil
	}
}

func rrU64(opts ...racerand.Option) func() (func() uint64, func(), error) {
	return func() (func() uint64, func(), error) {
		r, err := racerand.New(opts...)
		if err != nil {
			return nil, nil, err
		}
		return r.Uint64, func() { r.Close() }, nil
	}
}

func static(r io.Reader) func() (io.Reader, func(), error) {
	return func() (io.Reader, func(), error) { return r, func() {}, nil }
}

func staticU64(f func() uint64) func() (func() uint64, func(), error) {
	return func() (func() uint64, func(), error) { return f, func() {}, nil }
}

func generators() []generator {
	pcg := mrand.New(mrand.NewPCG(1, 2))
	cc8 := mrand.NewChaCha8([32]byte{1})
	v1 := mrand1.New(mrand1.NewSource(1))
	return []generator{
		{"racerand raw (atomic)", "racerand", rr(racerand.WithMode(racerand.ModeRaw), racerand.WithPersistentWorkers()), nil},
		{"racerand raw (unsynchronized)", "racerand", rr(racerand.WithMode(racerand.ModeRaw), racerand.WithPersistentWorkers(), racerand.WithSource(racerand.SourceUnsynchronized)), nil},
		{"racerand TRNG (H=0.5 default)", "racerand", rr(racerand.WithMode(racerand.ModeTRNG), racerand.WithPersistentWorkers()), nil},
		{"racerand TRNG (H=2 assumed)", "racerand", rr(racerand.WithMode(racerand.ModeTRNG), racerand.WithPersistentWorkers(), racerand.WithMinEntropy(2)), nil},
		{"racerand DRBG ChaCha8", "racerand", rr(), rrU64()},
		{"racerand DRBG AES-256-CTR", "racerand", rr(racerand.WithDRBG(racerand.DRBGAESCTR)), rrU64(racerand.WithDRBG(racerand.DRBGAESCTR))},
		{"racerand DRBG ChaCha8 + OS mix", "racerand", rr(racerand.WithOSEntropyMix()), nil},
		{"crypto/rand", "stdlib", static(crand.Reader), staticU64(func() uint64 {
			var b [8]byte
			crand.Read(b[:])
			return uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24 |
				uint64(b[4])<<32 | uint64(b[5])<<40 | uint64(b[6])<<48 | uint64(b[7])<<56
		})},
		{"math/rand/v2 ChaCha8", "stdlib", static(cc8), staticU64(cc8.Uint64)},
		{"math/rand/v2 PCG", "stdlib", static(sourceReader{pcg.Uint64}), staticU64(pcg.Uint64)},
		{"math/rand (v1)", "stdlib", static(v1), staticU64(v1.Uint64)},
		{"jitter raw", "jitter", static(jitter.RawReader()), nil},
		{"jitter conditioned (SHA-512, 2048/block)", "jitter", static(jitter.ConditionedReader(2048)), nil},
	}
}

func throughput(r io.Reader, chunk int, dur time.Duration) (float64, error) {
	buf := make([]byte, chunk)
	batch := max(1, 4096/chunk)
	start := time.Now()
	var total int64
	for {
		for i := 0; i < batch; i++ {
			if _, err := io.ReadFull(r, buf); err != nil {
				return 0, err
			}
		}
		total += int64(batch * chunk)
		if el := time.Since(start); el >= dur {
			return float64(total) / el.Seconds() / 1e6, nil
		}
	}
}

func measureThroughput(gens []generator, dur time.Duration) []ThroughputRow {
	var rows []ThroughputRow
	for _, g := range gens {
		row := ThroughputRow{Name: g.name, Family: g.family}
		r, closer, err := g.open()
		if err != nil {
			row.Err = err.Error()
			rows = append(rows, row)
			continue
		}
		if row.MBps64, err = throughput(r, 64, dur); err != nil {
			row.Err = err.Error()
		}
		if row.MBps64K, err = throughput(r, 1<<16, dur); err != nil {
			row.Err = err.Error()
		}
		closer()
		rows = append(rows, row)
		fmt.Fprintf(os.Stderr, "  throughput %-42s %10.2f MB/s (64 B) %10.2f MB/s (64 KiB)\n", g.name, row.MBps64, row.MBps64K)
	}
	return rows
}

func sourceError(err error) bool {
	return errors.Is(err, racerand.ErrHealthTestFailed) ||
		errors.Is(err, racerand.ErrInsufficientEntropy) ||
		errors.Is(err, racerand.ErrNotEnoughProcs)
}

func measureUint64(gens []generator, dur time.Duration) []Uint64Row {
	var rows []Uint64Row
	for _, g := range gens {
		if g.u64 == nil {
			continue
		}
		row := measureOneUint64(g, dur)
		rows = append(rows, row)
		fmt.Fprintf(os.Stderr, "  uint64 %s: %.2f ns/op %s\n", row.Name, row.NsPerOp, row.Err)
	}
	return rows
}

func measureOneUint64(g generator, dur time.Duration) (row Uint64Row) {
	row.Name = g.name
	defer func() {
		if p := recover(); p != nil {
			if err, ok := p.(error); ok && sourceError(err) {
				row.Err = err.Error()
			} else {
				panic(p)
			}
		}
	}()
	f, closer, err := g.u64()
	if err != nil {
		row.Err = err.Error()
		return
	}
	defer closer()
	start := time.Now()
	var n int64
	var sink uint64
	for time.Since(start) < dur {
		for i := 0; i < 1<<16; i++ {
			sink ^= f()
		}
		n += 1 << 16
	}
	runtime.KeepAlive(sink)
	row.NsPerOp = float64(time.Since(start).Nanoseconds()) / float64(n)
	return
}

func measureQuality(gens []generator, size int) []QualityRow {
	var rows []QualityRow
	for _, g := range gens {
		row := QualityRow{Name: g.name}
		r, closer, err := g.open()
		if err != nil {
			row.Err = err.Error()
			rows = append(rows, row)
			continue
		}
		data := make([]byte, size)
		start := time.Now()
		_, err = io.ReadFull(r, data)
		closer()
		if err != nil {
			row.Err = err.Error()
			rows = append(rows, row)
			continue
		}
		fmt.Fprintf(os.Stderr, "  quality %-42s collected %d bytes in %v; analysing...\n", g.name, size, time.Since(start).Round(time.Millisecond))
		row.Report = stattest.Analyze(data)
		row.NIST = stattest.NIST(data)
		row.Passed, row.Total = stattest.Passed(row.NIST)
		rows = append(rows, row)
	}
	return rows
}

func sweep(label string, cfg racerand.HarvesterConfig, n int) SweepRow {
	h := racerand.NewHarvester(cfg)
	h.Start()
	s := make([]uint64, n)
	h.Fill(s[:min(n, 4096)])
	start := time.Now()
	h.Fill(s)
	el := time.Since(start)
	h.Close()
	row := SweepRow{
		Label:    label,
		Source:   h.Config().Source.String(),
		Feedback: !cfg.DisableFeedback,
		Workers:  h.Config().Workers,
		Procs:    runtime.GOMAXPROCS(0),
		Samples:  n,
	}
	row.NsPerSample = float64(el.Nanoseconds()) / float64(n)
	row.MCV = stattest.MinEntropy64(s)
	row.Cond = stattest.CondMinEntropy64(s)
	row.Shannon = stattest.Shannon64(s)
	folded := make([]byte, n)
	for i, v := range s {
		folded[i] = racerand.Fold(v)
	}
	fr := stattest.Analyze(folded)
	row.FoldMCV = fr.MinEntropyByte
	row.FoldShannon = fr.ShannonEntropy
	row.RateMbit = row.MCV / row.NsPerSample * 1000
	row.SeedMicros = 1024 * row.NsPerSample / 1000
	if row.MCV > 0 {
		row.CalMicros = math.Ceil(512/row.MCV) * row.NsPerSample / 1000
	}
	fmt.Fprintf(os.Stderr, "  sweep %-28s %7.1f ns/sample  MCV %.2f  cond %.2f  shannon %.2f\n", label, row.NsPerSample, row.MCV, row.Cond, row.Shannon)
	return row
}

func measureWorkers(n int) []SweepRow {
	var rows []SweepRow
	ncpu := runtime.NumCPU()
	counts := []int{1, 2, 3, 4, 6, 8, 12, ncpu - 1, ncpu, 2 * ncpu}
	seen := map[int]bool{}
	for _, w := range counts {
		if w < 1 || w > 256 || seen[w] {
			continue
		}
		seen[w] = true
		rows = append(rows, sweep(fmt.Sprintf("atomic w=%d", w), racerand.HarvesterConfig{Workers: w}, n))
	}
	return rows
}

func measureVariants(n int) []SweepRow {
	var rows []SweepRow
	for _, src := range []racerand.SourceKind{racerand.SourceAtomic, racerand.SourceUnsynchronized} {
		for _, fb := range []bool{true, false} {
			for _, w := range []int{1, 0} {
				cfg := racerand.HarvesterConfig{Workers: w, Source: src, DisableFeedback: !fb}
				rows = append(rows, sweep(fmt.Sprintf("%v fb=%v w=%d", src, fb, racerand.NewHarvester(cfg).Config().Workers), cfg, n))
			}
		}
	}
	return rows
}

func measureProcs(n int) []SweepRow {
	var rows []SweepRow
	orig := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(orig)
	ncpu := runtime.NumCPU()
	for _, p := range []int{1, 2, 3, 4, 8, ncpu} {
		if p > ncpu {
			continue
		}
		runtime.GOMAXPROCS(p)
		samples := n
		if p == 1 {
			// With one P, yields and preemption dominate sample latency.
			samples = 64
		}
		row := sweep(fmt.Sprintf("GOMAXPROCS=%d", p), racerand.HarvesterConfig{}, samples)
		if r, err := racerand.New(); err != nil {
			row.NewErr = err.Error()
		} else {
			r.Close()
		}
		rows = append(rows, row)
	}
	return rows
}

func measureLatency() []LatencyRow {
	var rows []LatencyRow
	measure := func(name, note string, iters int, f func() error) {
		row := LatencyRow{Name: name, Note: note}
		start := time.Now()
		for i := 0; i < iters; i++ {
			if err := f(); err != nil {
				row.Err = err.Error()
				break
			}
		}
		if row.Err == "" {
			row.Micros = float64(time.Since(start).Nanoseconds()) / float64(iters) / 1000
		}
		rows = append(rows, row)
		fmt.Fprintf(os.Stderr, "  latency %s: %.3f us %s\n", name, row.Micros, row.Err)
	}
	measure("New() with defaults", "startup checks, seed, worker start/stop", 10, func() error {
		r, err := racerand.New()
		if err != nil {
			return err
		}
		return r.Close()
	})
	for _, h := range []float64{0.5, 2, 4} {
		name := fmt.Sprintf("Reseed() at assumed H=%v", h)
		r, err := racerand.New(racerand.WithMinEntropy(h))
		if err != nil {
			rows = append(rows, LatencyRow{Name: name, Err: err.Error()})
			continue
		}
		measure(name, "worker start, harvest, SHA-512, worker stop", 50, r.Reseed)
		r.Close()
	}
	for _, c := range []struct {
		name  string
		opts  []racerand.Option
		count int
	}{
		{"TRNG Read(32), workers per call", []racerand.Option{racerand.WithMode(racerand.ModeTRNG)}, 50},
		{"TRNG Read(32), persistent workers", []racerand.Option{racerand.WithMode(racerand.ModeTRNG), racerand.WithPersistentWorkers()}, 50},
		{"DRBG Read(32)", nil, 10000},
	} {
		r, err := racerand.New(c.opts...)
		if err != nil {
			rows = append(rows, LatencyRow{Name: c.name, Err: err.Error()})
			continue
		}
		buf := make([]byte, 32)
		measure(c.name, "average per call; buffered TRNG block spans two calls", c.count, func() error { _, err := r.Read(buf); return err })
		r.Close()
	}
	return rows
}

func cpuModel() string {
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.Command("sysctl", "-n", "machdep.cpu.brand_string").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "linux":
		if data, err := os.ReadFile("/proc/cpuinfo"); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "model name") {
					if i := strings.Index(line, ":"); i >= 0 {
						return strings.TrimSpace(line[i+1:])
					}
				}
			}
		}
	}
	return runtime.GOARCH
}

func main() {
	var (
		out     = flag.String("out", "BENCHMARKS.md", "Markdown report path")
		jsonOut = flag.String("json", "", "optional JSON results path")
		size    = flag.Int("bytes", 1<<20, "bytes collected from each generator for the quality tests")
		dur     = flag.Duration("dur", time.Second, "duration of each throughput measurement")
		samples = flag.Int("samples", 1<<16, "raw samples per sweep point")
		gobench = flag.String("gobench", "", "file with `go test -bench` output to embed")
		noise   = flag.String("noise", "noise", "directory for example-output images; empty disables them")
		from    = flag.String("from", "", "load previous results from this JSON file instead of measuring")
		noiseOK = flag.Bool("noise-only", false, "with -from: collect new noise images while keeping other results")
		quick   = flag.Bool("quick", false, "small sizes for a smoke run")
	)
	flag.Parse()
	if *quick {
		*size = 1 << 17
		*dur = 200 * time.Millisecond
		*samples = 1 << 13
	}

	if *size < 1 || *size > 1<<24 || *samples < 1024 || *samples > 1<<20 || *dur <= 0 || flag.NArg() != 0 {
		fatalf("require -bytes in [1,16777216], -samples in [1024,1048576], a positive duration, and no positional arguments")
	}
	if *noiseOK && *from == "" {
		fatalf("-noise-only requires -from")
	}
	var res Result
	gens := generators()
	if *from != "" {
		data, err := os.ReadFile(*from)
		if err == nil {
			decoder := json.NewDecoder(bytes.NewReader(data))
			decoder.UseNumber()
			err = decoder.Decode(&res)
			if err == nil {
				resolveLoadedNoise(&res, *from)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *from == "" {
		res = Result{
			Machine: Machine{
				OS: runtime.GOOS, Arch: runtime.GOARCH, CPU: cpuModel(),
				NumCPU: runtime.NumCPU(), GOMAXPROCS: runtime.GOMAXPROCS(0), GoVersion: runtime.Version(),
			},
			Generated: time.Now(),
			Config:    map[string]any{"quality_bytes": *size, "throughput_dur": dur.String(), "sweep_samples": *samples},
		}
		fmt.Fprintln(os.Stderr, "== throughput")
		res.Throughput = measureThroughput(gens, *dur)
		fmt.Fprintln(os.Stderr, "== uint64")
		res.Uint64 = measureUint64(gens, *dur)
		fmt.Fprintln(os.Stderr, "== quality")
		res.Quality = measureQuality(gens, *size)
		fmt.Fprintln(os.Stderr, "== worker sweep")
		res.Workers = measureWorkers(*samples)
		fmt.Fprintln(os.Stderr, "== variants")
		res.Variants = measureVariants(*samples)
		fmt.Fprintln(os.Stderr, "== GOMAXPROCS sweep")
		res.Procs = measureProcs(*samples)
		fmt.Fprintln(os.Stderr, "== latency")
		res.Latency = measureLatency()
	}
	if *noise != "" && (*from == "" || *noiseOK) {
		fmt.Fprintln(os.Stderr, "== example output")
		var err error
		res.Noise, err = measureNoise(gens, *noise, *size)
		if err != nil {
			fatalf("noise output: %v", err)
		}
		res.Config["noise_bytes"] = max(*size, noiseSide*noiseSide)
		res.Config["noise_generated"] = time.Now().Format(time.RFC3339)
	}
	if *gobench != "" {
		if data, err := os.ReadFile(*gobench); err == nil {
			res.GoBench = string(data)
		} else {
			fatalf("read benchmarks: %v", err)
		}
	}

	if err := os.WriteFile(*out, []byte(markdown(relativeNoise(res, *out))), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *jsonOut != "" {
		data, err := json.MarshalIndent(relativeNoise(res, *jsonOut), "", "  ")
		if err != nil {
			fatalf("encode JSON: %v", err)
		}
		if err := os.WriteFile(*jsonOut, data, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %s\n", *out)
}
func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "racerand-bench: "+format+"\n", args...)
	os.Exit(1)
}

// Stored image paths are relative to the file that contains them.
func resolveLoadedNoise(r *Result, file string) {
	if r.Config == nil {
		r.Config = make(map[string]any)
	}
	for i := range r.Noise {
		for _, p := range []*string{&r.Noise[i].Gray, &r.Noise[i].Bits, &r.Noise[i].Lag} {
			if *p != "" && !filepath.IsAbs(*p) {
				*p = filepath.Join(filepath.Dir(file), *p)
			}
		}
	}
}

func relativeNoise(r Result, file string) Result {
	r.Noise = append([]NoiseRow(nil), r.Noise...)
	base, err := filepath.Abs(filepath.Dir(file))
	if err != nil {
		fatalf("report path: %v", err)
	}
	for i := range r.Noise {
		for _, p := range []*string{&r.Noise[i].Gray, &r.Noise[i].Bits, &r.Noise[i].Lag} {
			if *p == "" {
				continue
			}
			abs, err := filepath.Abs(*p)
			if err != nil {
				fatalf("image path: %v", err)
			}
			rel, err := filepath.Rel(base, abs)
			if err != nil {
				fatalf("relative image path: %v", err)
			}
			*p = filepath.ToSlash(rel)
		}
	}
	return r
}
