package racerand

import (
	"fmt"
	"math"
)

// HealthError reports a continuous health-test failure. It matches
// ErrHealthTestFailed with errors.Is.
type HealthError struct {
	// Test is "RCT" (repetition count) or "APT" (adaptive proportion).
	Test string
	// Count is the statistic that crossed the cutoff.
	Count int
	// Cutoff is the threshold: RCT fails at or above it, APT strictly above it.
	Cutoff int
	// Sample is the raw sample that triggered the failure.
	Sample uint64
}

func (e *HealthError) Error() string {
	return fmt.Sprintf("racerand: %s health test failed: count %d, cutoff %d (sample %#x)",
		e.Test, e.Count, e.Cutoff, e.Sample)
}

// Is reports whether target is ErrHealthTestFailed.
func (e *HealthError) Is(target error) bool { return target == ErrHealthTestFailed }

// health implements the Repetition Count Test and the Adaptive Proportion
// Test of NIST SP 800-90B section 4.4 on raw 64-bit samples.
//
// Both tests are parameterised by the assumed min-entropy per sample H and a
// false-positive probability alpha. The RCT fails when a sample repeats
// 1+ceil(-log2(alpha)/H) times in a row. The APT fails when, within a window
// of W samples, the first sample of the window reappears more often than a
// binomial(W-1, 2^-H) variable would at the 1-alpha quantile.
type health struct {
	rctCutoff int
	aptWindow int
	aptCutoff int

	last     uint64
	rctCount int

	aptRef   uint64
	aptCount int
	aptPos   int

	failures uint64
}

func newHealth(minEntropy, alpha float64, window int) *health {
	return &health{
		rctCutoff: rctCutoff(minEntropy, alpha),
		aptWindow: window,
		aptCutoff: aptCutoff(window, minEntropy, alpha),
	}
}

// check feeds one sample through both tests and returns a *HealthError on
// failure. After a failure the test state is reset so that a subsequent
// Reset on the Reader starts clean.
func (t *health) check(s uint64) error {
	// Repetition count test.
	switch {
	case t.rctCount == 0:
		t.last = s
		t.rctCount = 1
	case s == t.last:
		t.rctCount++
		if t.rctCount >= t.rctCutoff {
			t.failures++
			c := t.rctCount
			t.reset()
			return &HealthError{Test: "RCT", Count: c, Cutoff: t.rctCutoff, Sample: s}
		}
	default:
		t.last = s
		t.rctCount = 1
	}

	// Adaptive proportion test.
	if t.aptPos == 0 {
		t.aptRef = s
		t.aptCount = 0
	} else if s == t.aptRef {
		t.aptCount++
	}
	t.aptPos++
	if t.aptPos == t.aptWindow {
		t.aptPos = 0
		if t.aptCount > t.aptCutoff {
			t.failures++
			c := t.aptCount
			t.reset()
			return &HealthError{Test: "APT", Count: c, Cutoff: t.aptCutoff, Sample: s}
		}
	}
	return nil
}

func (t *health) reset() {
	t.rctCount = 0
	t.aptPos = 0
	t.aptCount = 0
}

// rctCutoff returns the repetition-count cutoff of SP 800-90B 4.4.1.
func rctCutoff(minEntropy, alpha float64) int {
	return 1 + int(math.Ceil(-math.Log2(alpha)/minEntropy))
}

// aptCutoff returns the smallest c such that P(X <= c) >= 1-alpha for
// X ~ Binomial(window-1, 2^-minEntropy). The APT fails when the number of
// repeats of the reference sample within the window exceeds c.
func aptCutoff(window int, minEntropy, alpha float64) int {
	n := window - 1
	p := math.Pow(2, -minEntropy)
	if p >= 1 {
		return n
	}
	lp := math.Log(p)
	lq := math.Log1p(-p)
	// Sum the upper tail directly, avoiding cancellation in 1-alpha.
	tail := 0.0
	for c := n; c >= 0; c-- {
		if tail > alpha {
			return c + 1
		}
		tail += math.Exp(lchoose(n, c) + float64(c)*lp + float64(n-c)*lq)
	}
	return 0
}

func lchoose(n, k int) float64 {
	a, _ := math.Lgamma(float64(n + 1))
	b, _ := math.Lgamma(float64(k + 1))
	c, _ := math.Lgamma(float64(n - k + 1))
	return a - b - c
}
