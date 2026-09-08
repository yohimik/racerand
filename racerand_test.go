package racerand

import (
	"bytes"
	"errors"
	"io"
	"math"
	mrand "math/rand/v2"
	"runtime"
	"sync"
	"testing"
)

// API tests exercise buffering and synchronization without assuming anything
// about the host's entropy. Live acceptance is tested separately below.
func mustNew(t *testing.T, opts ...Option) *Reader {
	t.Helper()
	opts = append(opts, WithoutHealthTests())
	r, err := New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func notConstant(t *testing.T, b []byte) {
	t.Helper()
	for _, x := range b[1:] {
		if x != b[0] {
			return
		}
	}
	t.Fatalf("output is constant (%d bytes of %#x)", len(b), b[0])
}

func TestDefaults(t *testing.T) {
	r := mustNew(t)
	buf := make([]byte, 1<<20+123)
	n, err := r.Read(buf)
	if err != nil || n != len(buf) {
		t.Fatalf("Read: n=%d err=%v", n, err)
	}
	notConstant(t, buf)
	s := r.Stats()
	if s.Mode != ModeDRBG || s.Source != SourceAtomic {
		t.Errorf("stats %+v", s)
	}
	if s.Seeds < 2 {
		t.Errorf("expected a reseed after 1 MiB, seeds=%d", s.Seeds)
	}
	if s.Workers != max(1, runtime.GOMAXPROCS(0)-1) {
		t.Errorf("workers=%d", s.Workers)
	}
	if s.SamplesPerSeed != 1024 || s.SamplesPerBlock != 2048 {
		t.Errorf("samples per seed/block = %d/%d", s.SamplesPerSeed, s.SamplesPerBlock)
	}
	if !r.cfg.skipHealth {
		if s.RCTCutoff != 61 {
			t.Errorf("RCT cutoff %d", s.RCTCutoff)
		}
		if s.StartupMinEntropy < 0.5 {
			t.Errorf("startup estimate %v", s.StartupMinEntropy)
		}
	}
	if s.Bytes != uint64(len(buf)) {
		t.Errorf("bytes %d", s.Bytes)
	}
	if r.Stats().Workers > 0 && r.h.Running() {
		t.Error("workers should be stopped between harvests in DRBG mode")
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(buf[:1]); !errors.Is(err, ErrClosed) {
		t.Errorf("Read after Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestModes(t *testing.T) {
	for _, mode := range []Mode{ModeDRBG, ModeTRNG, ModeRaw} {
		for _, persistent := range []bool{false, true} {
			opts := []Option{WithMode(mode)}
			if persistent {
				opts = append(opts, WithPersistentWorkers())
			}
			r := mustNew(t, opts...)
			buf := make([]byte, 4096)
			if _, err := io.ReadFull(r, buf); err != nil {
				t.Fatalf("%v persistent=%v: %v", mode, persistent, err)
			}
			if mode != ModeRaw {
				notConstant(t, buf)
			}
			// Odd sizes exercise block buffering.
			for _, n := range []int{1, 3, 63, 64, 65, 200} {
				if _, err := r.Read(make([]byte, n)); err != nil {
					t.Fatalf("%v: Read(%d): %v", mode, n, err)
				}
			}
			if r.h.Running() != persistent {
				t.Errorf("%v persistent=%v: running=%v", mode, persistent, r.h.Running())
			}
			if mode.String() == "" {
				t.Error("empty mode string")
			}
			r.Close()
		}
	}
}

func TestDRBGKinds(t *testing.T) {
	var outs [][]byte
	for _, k := range []DRBGKind{DRBGChaCha8, DRBGAESCTR} {
		r := mustNew(t, WithDRBG(k))
		buf := make([]byte, 1<<16)
		if _, err := r.Read(buf); err != nil {
			t.Fatal(err)
		}
		notConstant(t, buf)
		outs = append(outs, buf)
		var u [4]uint64
		for i := range u {
			u[i] = r.Uint64()
		}
		if u[0] == u[1] && u[1] == u[2] && u[2] == u[3] {
			t.Errorf("%v: Uint64 constant", k)
		}
	}
	if bytes.Equal(outs[0], outs[1]) {
		t.Error("two readers produced identical output")
	}
}

func TestReseedInterval(t *testing.T) {
	r := mustNew(t, WithReseedInterval(1024))
	buf := make([]byte, 10*1024)
	if _, err := r.Read(buf); err != nil {
		t.Fatal(err)
	}
	if s := r.Stats(); s.Seeds < 10 {
		t.Errorf("seeds=%d, want >= 10", s.Seeds)
	}
	// Reads larger than the interval are split across reseeds.
	if _, err := r.Read(make([]byte, 5000)); err != nil {
		t.Fatal(err)
	}
	before := r.Stats().Seeds
	if err := r.Reseed(); err != nil {
		t.Fatal(err)
	}
	if r.Stats().Seeds != before+1 {
		t.Error("Reseed did not reseed")
	}
	// Uint64 also triggers reseeds.
	for i := 0; i < 1024; i++ {
		r.Uint64()
	}
	if r.Stats().Seeds < before+8 {
		t.Errorf("Uint64 did not reseed: %d", r.Stats().Seeds)
	}
}

func TestSource(t *testing.T) {
	r := mustNew(t)
	rng := mrand.New(r)
	seen := map[int]bool{}
	for i := 0; i < 1000; i++ {
		seen[rng.IntN(10)] = true
	}
	if len(seen) != 10 {
		t.Errorf("IntN(10) covered %d values", len(seen))
	}
	if x := rng.Float64(); x < 0 || x >= 1 {
		t.Errorf("Float64=%v", x)
	}
}

func TestConcurrentReads(t *testing.T) {
	r := mustNew(t, WithReseedInterval(4096))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			buf := make([]byte, 1000)
			for j := 0; j < 50; j++ {
				if _, err := r.Read(buf); err != nil {
					t.Error(err)
					return
				}
				r.Uint64()
			}
		}()
	}
	wg.Wait()
	if err := r.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestOSEntropyMix(t *testing.T) {
	r := mustNew(t, WithOSEntropyMix(), WithReseedInterval(512))
	buf := make([]byte, 4096)
	if _, err := r.Read(buf); err != nil {
		t.Fatal(err)
	}
	notConstant(t, buf)
}

func TestNotEnoughProcs(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	if _, err := New(); !errors.Is(err, ErrNotEnoughProcs) {
		t.Errorf("GOMAXPROCS=1: err=%v", err)
	}
}

func TestInsufficientEntropy(t *testing.T) {
	// 4096 samples cannot demonstrate 40 bits per sample.
	_, err := New(WithMinEntropy(40))
	if !errors.Is(err, ErrInsufficientEntropy) {
		t.Errorf("err=%v", err)
	}
	// Unless the check is disabled.
	r := mustNew(t, WithMinEntropy(40), WithoutHealthTests())
	if _, err := r.Read(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if s := r.Stats(); s.RCTCutoff != 0 || math.IsNaN(s.StartupMinEntropy) || s.StartupMinEntropy < 0 {
		t.Errorf("stats %+v", s)
	}
}

func TestOptionValidation(t *testing.T) {
	bad := [][]Option{
		{WithMode(Mode(9))},
		{WithDRBG(DRBGKind(9))},
		{WithSource(SourceKind(9))},
		{WithWorkers(-1)},
		{WithWorkers(257)},
		{nil},
		{WithMinEntropy(math.SmallestNonzeroFloat64)},
		{WithMinEntropy(math.NaN())},
		{WithMinEntropy(math.Inf(1))},
		{WithStartupSamples(math.MaxInt)},
		{WithHealthAlpha(math.NaN())},
		{WithMinEntropy(0)},
		{WithMinEntropy(-1)},
		{WithMinEntropy(100)},
		{WithReseedInterval(0)},
		{WithStartupSamples(10)},
		{WithHealthAlpha(0)},
		{WithHealthAlpha(1)},
	}
	for i, opts := range bad {
		if _, err := New(opts...); err == nil {
			t.Errorf("case %d: expected an error", i)
		}
	}
	for _, s := range []string{Mode(9).String(), DRBGKind(9).String(), SourceKind(9).String()} {
		if s == "" {
			t.Error("empty String()")
		}
	}
}

func TestHealthFailureIsSticky(t *testing.T) {
	r := mustNew(t, WithMode(ModeRaw))
	good := r.health
	// A negative APT cutoff fails at the end of the first window whatever
	// the samples are.
	r.health = &health{rctCutoff: 1 << 30, aptWindow: 8, aptCutoff: -1}
	_, err := r.Read(make([]byte, 64))
	if !errors.Is(err, ErrHealthTestFailed) {
		t.Fatalf("err=%v", err)
	}
	var he *HealthError
	if !errors.As(err, &he) || he.Test != "APT" {
		t.Fatalf("err=%v", err)
	}
	if he.Error() == "" {
		t.Error("empty message")
	}
	if _, err2 := r.Read(make([]byte, 1)); err2 != err {
		t.Errorf("error not sticky: %v", err2)
	}
	if r.Err() != err {
		t.Error("Err() mismatch")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("Uint64 should panic on a failed reader")
			}
		}()
		r.Uint64()
	}()
	if s := r.Stats(); s.HealthFailures == 0 {
		t.Error("failure not counted")
	}
	r.health = good
	if err := r.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := r.Read(make([]byte, 64)); err != nil {
		t.Fatalf("Read after Reset: %v", err)
	}
	r.Close()
	if err := r.Reset(); !errors.Is(err, ErrClosed) {
		t.Errorf("Reset after Close: %v", err)
	}
	if err := r.Reseed(); !errors.Is(err, ErrClosed) {
		t.Errorf("Reseed after Close: %v", err)
	}
}

func TestUnsynchronizedSource(t *testing.T) {
	if raceEnabled {
		t.Skip("SourceUnsynchronized is a data race by design")
	}
	for _, mode := range []Mode{ModeRaw, ModeTRNG, ModeDRBG} {
		r := mustNew(t, WithSource(SourceUnsynchronized), WithMode(mode))
		buf := make([]byte, 4096)
		if _, err := r.Read(buf); err != nil {
			t.Fatalf("%v: %v", mode, err)
		}
		if mode != ModeRaw {
			notConstant(t, buf)
		}
		if r.Stats().Source != SourceUnsynchronized {
			t.Error("stats source")
		}
	}
}

func TestWorkersOption(t *testing.T) {
	for _, w := range []int{1, 2, 3} {
		r := mustNew(t, WithWorkers(w), WithMode(ModeRaw))
		if r.Stats().Workers != w {
			t.Errorf("workers=%d", r.Stats().Workers)
		}
		if _, err := r.Read(make([]byte, 256)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWithoutFeedback(t *testing.T) {
	r := mustNew(t, WithoutFeedback(), WithMode(ModeRaw))
	if _, err := r.Read(make([]byte, 256)); err != nil {
		t.Fatal(err)
	}
	// A single worker without feedback runs a perfectly regular loop and is
	// a weak source. Either the startup estimate or the continuous tests are
	// expected to reject it on some machines; what must not happen is a
	// silent success with a bad estimate.
	if raceEnabled {
		return
	}
	r2, err := New(WithWorkers(1), WithoutFeedback(), WithMode(ModeRaw))
	if err != nil {
		if !errors.Is(err, ErrInsufficientEntropy) && !errors.Is(err, ErrHealthTestFailed) {
			t.Fatalf("unexpected error: %v", err)
		}
		t.Logf("1 worker without feedback rejected, as allowed: %v", err)
		return
	}
	defer r2.Close()
	if s := r2.Stats(); s.StartupMinEntropy < s.MinEntropy {
		t.Errorf("accepted with estimate %.3f below assumption %.3f", s.StartupMinEntropy, s.MinEntropy)
	}
}

func TestDefaultReader(t *testing.T) {
	buf := make([]byte, 64)
	if _, err := Read(buf); err != nil {
		if !errors.Is(err, ErrHealthTestFailed) && !errors.Is(err, ErrInsufficientEntropy) {
			t.Fatal(err)
		}
		t.Logf("live source rejected: %v", err)
		return
	}
	notConstant(t, buf)
	r1, _ := Default()
	r2, _ := Default()
	if r1 != r2 {
		t.Error("Default is not a singleton")
	}
}

func TestStartupSamplesOption(t *testing.T) {
	r := mustNew(t, WithStartupSamples(2048), WithHealthAlpha(1e-6))
	if s := r.Stats(); s.Samples < 2048 || (!r.cfg.skipHealth && s.RCTCutoff != 1+40) {
		t.Errorf("stats %+v", s)
	}
}
