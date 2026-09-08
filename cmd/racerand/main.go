// Command racerand writes random bytes from goroutine races to standard
// output, for piping into external test suites such as ent, dieharder or
// the NIST STS, or for use as a quick entropy source in scripts.
//
// Usage:
//
//	racerand [-mode drbg|trng|raw] [-n bytes] [-hex] [-workers N] [-stats] > out.bin
//
// With -n 0 it streams until the write fails (for example when the reader of
// a pipe exits).
package main

import (
	"bufio"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yohimik/racerand"
)

func main() {
	var (
		mode       = flag.String("mode", "drbg", "output mode: drbg, trng or raw")
		n          = flag.Int64("n", 1<<20, "number of bytes to write; 0 streams forever")
		hexOut     = flag.Bool("hex", false, "write hexadecimal text instead of raw bytes")
		workers    = flag.Int("workers", 0, "racing goroutines; 0 selects GOMAXPROCS-1")
		source     = flag.String("source", "atomic", "raw source: atomic or unsync (research only)")
		drbg       = flag.String("drbg", "chacha8", "DRBG for drbg mode: chacha8 or aes")
		minEntropy = flag.Float64("min-entropy", 0.5, "assumed min-entropy in bits per raw sample")
		mixOS      = flag.Bool("mix-os", false, "fold crypto/rand into every seed")
		stats      = flag.Bool("stats", false, "print reader statistics to stderr on exit")
	)
	flag.Parse()
	if *n < 0 || flag.NArg() != 0 {
		fatalf("-n must be nonnegative and positional arguments are not accepted")
	}
	if *mixOS && *mode != "drbg" {
		fatalf("-mix-os only applies to drbg mode")
	}

	opts := []racerand.Option{racerand.WithWorkers(*workers), racerand.WithMinEntropy(*minEntropy)}
	switch *mode {
	case "drbg":
		opts = append(opts, racerand.WithMode(racerand.ModeDRBG))
	case "trng":
		opts = append(opts, racerand.WithMode(racerand.ModeTRNG), racerand.WithPersistentWorkers())
	case "raw":
		opts = append(opts, racerand.WithMode(racerand.ModeRaw), racerand.WithPersistentWorkers())
	default:
		fatalf("unknown mode %q", *mode)
	}
	switch *source {
	case "atomic":
	case "unsync":
		opts = append(opts, racerand.WithSource(racerand.SourceUnsynchronized))
	default:
		fatalf("unknown source %q", *source)
	}
	switch *drbg {
	case "chacha8":
	case "aes":
		opts = append(opts, racerand.WithDRBG(racerand.DRBGAESCTR))
	default:
		fatalf("unknown drbg %q", *drbg)
	}
	if *mixOS {
		opts = append(opts, racerand.WithOSEntropyMix())
	}

	r, err := racerand.New(opts...)
	if err != nil {
		fatalf("%v", err)
	}
	defer r.Close()

	bw := bufio.NewWriterSize(os.Stdout, 1<<16)
	var out io.Writer = bw
	if *hexOut {
		out = hex.NewEncoder(bw)
	}
	buf := make([]byte, 1<<16)
	var written int64
	for *n == 0 || written < *n {
		chunk := buf
		if *n != 0 && *n-written < int64(len(buf)) {
			chunk = buf[:*n-written]
		}
		if _, err := r.Read(chunk); err != nil {
			fatalf("read: %v", err)
		}
		if _, err := out.Write(chunk); err != nil {
			fatalf("write: %v", err)
		}
		written += int64(len(chunk))
	}
	if *hexOut {
		if _, err := fmt.Fprintln(bw); err != nil {
			fatalf("write: %v", err)
		}
	}
	if err := bw.Flush(); err != nil {
		fatalf("flush: %v", err)
	}
	if *stats {
		s := r.Stats()
		fmt.Fprintf(os.Stderr, "mode=%v source=%v workers=%d assumed-min-entropy=%.2f startup-estimate=%.2f bits/sample\n",
			s.Mode, s.Source, s.Workers, s.MinEntropy, s.StartupMinEntropy)
		fmt.Fprintf(os.Stderr, "samples=%d seeds=%d bytes=%d health-failures=%d rct-cutoff=%d apt-cutoff=%d/%d\n",
			s.Samples, s.Seeds, s.Bytes, s.HealthFailures, s.RCTCutoff, s.APTCutoff, s.APTWindow)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "racerand: "+format+"\n", args...)
	os.Exit(1)
}
