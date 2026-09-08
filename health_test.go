package racerand

import (
	"errors"
	"math"
	"testing"
)

func TestCutoffs(t *testing.T) {
	alpha := math.Exp2(-30)
	if c := rctCutoff(0.5, alpha); c != 61 {
		t.Errorf("rctCutoff(0.5)=%d", c)
	}
	if c := rctCutoff(1, alpha); c != 31 {
		t.Errorf("rctCutoff(1)=%d", c)
	}
	if c := rctCutoff(8, alpha); c != 5 {
		t.Errorf("rctCutoff(8)=%d", c)
	}
	// Binomial(511, 0.5): mean 255.5, sd 11.3; the 1-2^-30 quantile is
	// about 6.3 sd above the mean.
	if c := aptCutoff(512, 1, alpha); c < 315 || c > 335 {
		t.Errorf("aptCutoff(512,1)=%d", c)
	}
	// Binomial(511, 2^-0.5): mean 361, sd 10.3.
	if c := aptCutoff(512, 0.5, alpha); c < 415 || c > 435 {
		t.Errorf("aptCutoff(512,0.5)=%d", c)
	}
	if c := aptCutoff(512, 0, alpha); c != 511 {
		t.Errorf("aptCutoff(512,0)=%d", c)
	}
	// A less demanding alpha lowers the cutoffs.
	if aptCutoff(512, 1, 0.01) >= aptCutoff(512, 1, alpha) {
		t.Error("alpha ordering")
	}
}

func TestRCT(t *testing.T) {
	h := newHealth(1, math.Exp2(-30), 512)
	for i := 0; i < h.rctCutoff-1; i++ {
		if err := h.check(42); err != nil {
			t.Fatalf("premature failure at %d: %v", i, err)
		}
	}
	err := h.check(42)
	var he *HealthError
	if !errors.As(err, &he) || he.Test != "RCT" || he.Count != h.rctCutoff {
		t.Fatalf("err=%v", err)
	}
	if !errors.Is(err, ErrHealthTestFailed) {
		t.Error("Is")
	}
	// A run interrupted by a different sample does not fail.
	h = newHealth(1, math.Exp2(-30), 512)
	for i := 0; i < 10*h.rctCutoff; i++ {
		v := uint64(42)
		if i%(h.rctCutoff-1) == 0 {
			v = 7
		}
		if err := h.check(v); err != nil {
			t.Fatalf("unexpected failure: %v", err)
		}
	}
}

func TestAPT(t *testing.T) {
	h := newHealth(1, math.Exp2(-30), 512)
	// Reference repeats every other sample: 255 repeats in the window, far
	// below the cutoff of ~325, so this passes.
	for i := 0; i < 512*4; i++ {
		if err := h.check(uint64(i & 1)); err != nil {
			t.Fatalf("alternating: %v", err)
		}
	}
	// Reference repeats 3 out of 4 samples: ~383 repeats, above the cutoff,
	// but never more than 3 in a row, so only the APT fires.
	h = newHealth(1, math.Exp2(-30), 512)
	var err error
	for i := 0; i < 512 && err == nil; i++ {
		v := uint64(0)
		if i%4 == 3 {
			v = 1
		}
		err = h.check(v)
	}
	var he *HealthError
	if !errors.As(err, &he) || he.Test != "APT" {
		t.Fatalf("err=%v", err)
	}
	if he.Count <= he.Cutoff {
		t.Errorf("count %d cutoff %d", he.Count, he.Cutoff)
	}
	if h.failures != 1 {
		t.Errorf("failures=%d", h.failures)
	}
}
