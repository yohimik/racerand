package racerand

import (
	"bytes"
	"errors"
	"math"
	"runtime"
	"testing"
)

func TestUint64ReseedBoundaries(t *testing.T) {
	for _, kind := range []DRBGKind{DRBGChaCha8, DRBGAESCTR} {
		for _, interval := range []int{1, 7, 8, 9, 17, math.MaxInt} {
			r := mustNew(t, WithDRBG(kind), WithReseedInterval(interval), WithMinEntropy(40))
			for i := 0; i < 4; i++ {
				r.Uint64()
				if r.since > interval {
					t.Fatalf("%v interval=%d: emitted %d bytes since seed", kind, interval, r.since)
				}
				if _, err := r.Read(make([]byte, 3)); err != nil {
					t.Fatal(err)
				}
			}
			if r.Stats().Bytes != 44 {
				t.Fatalf("incorrect byte accounting: %+v", r.Stats())
			}
			if interval == 1 && r.Stats().Seeds != 44 {
				t.Fatalf("one-byte interval: expected 44 seeds, got %d", r.Stats().Seeds)
			}
			r.Close()
		}
	}
}

func TestFailedPersistentReaderStopsAndClears(t *testing.T) {
	r := mustNew(t, WithMode(ModeRaw), WithPersistentWorkers())
	r.health = &health{rctCutoff: 1000, aptWindow: 8, aptCutoff: -1}
	before := r.Stats().Samples
	buf := bytes.Repeat([]byte{255}, 64)
	n, err := r.Read(buf)
	if n != 0 || !errors.Is(err, ErrHealthTestFailed) || !bytes.Equal(buf, make([]byte, len(buf))) {
		t.Fatalf("failure must return zero bytes and clear output: n=%d err=%v", n, err)
	}
	if r.h.Running() || r.Stats().Samples-before != 8 {
		t.Fatal("failed worker cleanup or sample accounting")
	}
	r.health = nil
	if err := r.Reset(); err != nil || !r.h.Running() {
		t.Fatalf("reset must resume persistent workers: %v", err)
	}
}

func TestRuntimeProcsChange(t *testing.T) {
	r := mustNew(t, WithPersistentWorkers())
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	if err := r.Reseed(); !errors.Is(err, ErrNotEnoughProcs) {
		t.Fatalf("got %v", err)
	}
	if r.h.Running() {
		t.Fatal("workers still running after unavailable source")
	}
}

func TestZeroReader(t *testing.T) {
	var r Reader
	p := []byte{1, 2, 3}
	if n, err := r.Read(p); n != 0 || !errors.Is(err, ErrUninitialized) || !bytes.Equal(p, []byte{0, 0, 0}) {
		t.Fatalf("zero Reader: %d %v %v", n, err, p)
	}
	if !errors.Is(r.Reseed(), ErrUninitialized) || !errors.Is(r.Reset(), ErrUninitialized) {
		t.Fatal("zero Reader lifecycle should return ErrUninitialized")
	}
	_ = r.Stats()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLiveDefaultHealth(t *testing.T) {
	r, err := New()
	if err != nil {
		if !errors.Is(err, ErrInsufficientEntropy) && !errors.Is(err, ErrHealthTestFailed) {
			t.Fatal(err)
		}
		t.Logf("host source rejected at startup: %v", err)
		return
	}
	defer r.Close()
	s := r.Stats()
	if s.RCTCutoff != 61 || s.APTCutoff != 420 || s.StartupMinEntropy < s.MinEntropy {
		t.Fatalf("health configuration: %+v", s)
	}
	if _, err := r.Read(make([]byte, 2<<20)); err != nil {
		if !errors.Is(err, ErrHealthTestFailed) || r.Err() != err {
			t.Fatal(err)
		}
		t.Logf("host source rejected during reseed: %v", err)
	}
}
