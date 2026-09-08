package stattest

import (
	"math"
	mrand "math/rand/v2"
	"testing"
)

func TestSpecialFunctions(t *testing.T) {
	// Q(1, x) = exp(-x).
	for _, x := range []float64{0.1, 1, 2.5, 10} {
		if got, want := Igamc(1, x), math.Exp(-x); math.Abs(got-want) > 1e-12 {
			t.Errorf("Igamc(1,%v)=%v want %v", x, got, want)
		}
		if got, want := Igam(1, x), 1-math.Exp(-x); math.Abs(got-want) > 1e-12 {
			t.Errorf("Igam(1,%v)=%v want %v", x, got, want)
		}
	}
	// Q(1/2, x) = erfc(sqrt(x)).
	for _, x := range []float64{0.3, 1, 4} {
		if got, want := Igamc(0.5, x), math.Erfc(math.Sqrt(x)); math.Abs(got-want) > 1e-12 {
			t.Errorf("Igamc(0.5,%v)=%v want %v", x, got, want)
		}
	}
	// Chi-square with 255 df: the median is about 254.33.
	if p := Igamc(127.5, 254.334/2); math.Abs(p-0.5) > 0.01 {
		t.Errorf("chi-square median p=%v", p)
	}
	if p := NormalCDF(0); p != 0.5 {
		t.Errorf("NormalCDF(0)=%v", p)
	}
	if p := NormalCDF(1.959964); math.Abs(p-0.975) > 1e-5 {
		t.Errorf("NormalCDF(1.96)=%v", p)
	}
}

func goodData(n int) []byte {
	var seed [32]byte
	copy(seed[:], "racerand stattest fixed seed 01")
	c := mrand.NewChaCha8(seed)
	data := make([]byte, n)
	c.Read(data)
	return data
}

func TestGoodSource(t *testing.T) {
	data := goodData(1 << 20)
	r := Analyze(data)
	if r.ShannonEntropy < 7.999 {
		t.Errorf("shannon %v", r.ShannonEntropy)
	}
	if r.MinEntropyByte < 7.8 {
		t.Errorf("min-entropy byte %v", r.MinEntropyByte)
	}
	if r.MinEntropyBit < 0.995 {
		t.Errorf("min-entropy bit %v", r.MinEntropyBit)
	}
	if r.MarkovMinEntropy < 0.99 {
		t.Errorf("markov %v", r.MarkovMinEntropy)
	}
	if r.CondMinEntropy < 7.0 {
		t.Errorf("cond min-entropy %v", r.CondMinEntropy)
	}
	if r.ChiSquareP < 0.001 || r.ChiSquareP > 0.999 {
		t.Errorf("chi p %v", r.ChiSquareP)
	}
	if math.Abs(r.Mean-127.5) > 0.5 {
		t.Errorf("mean %v", r.Mean)
	}
	if r.MonteCarloPiErrPct > 0.5 {
		t.Errorf("pi err %v", r.MonteCarloPiErrPct)
	}
	if math.Abs(r.SerialCorrelation) > 0.01 {
		t.Errorf("scc %v", r.SerialCorrelation)
	}
	if r.CompressionRatio < 0.999 {
		t.Errorf("compression %v", r.CompressionRatio)
	}
	for _, tr := range NIST(data) {
		if !tr.Pass {
			t.Errorf("%s failed: p=%v %s", tr.Name, tr.P, tr.Note)
		}
	}
}

func TestBadSources(t *testing.T) {
	zeros := make([]byte, 1<<16)
	r := Analyze(zeros)
	if r.ShannonEntropy != 0 || r.MinEntropyByte != 0 || r.CompressionRatio > 0.01 {
		t.Errorf("zeros: %+v", r)
	}
	if passed, _ := Passed(NIST(zeros)); passed != 0 {
		t.Errorf("zeros passed %d NIST tests", passed)
	}

	counter := make([]byte, 1<<16)
	for i := range counter {
		counter[i] = byte(i)
	}
	r = Analyze(counter)
	if r.ShannonEntropy < 7.99 {
		t.Errorf("counter shannon %v", r.ShannonEntropy)
	}
	if r.CondMinEntropy > 0.01 {
		t.Errorf("counter cond min-entropy %v should be ~0", r.CondMinEntropy)
	}
	if r.CompressionRatio > 0.05 {
		t.Errorf("counter compression %v", r.CompressionRatio)
	}
	if passed, total := Passed(NIST(counter)); passed == total {
		t.Errorf("counter passed all %d NIST tests", total)
	}

	// 60% ones: every frequency-sensitive test must fail.
	biased := goodData(1 << 16)
	rng := mrand.New(mrand.NewChaCha8([32]byte{7}))
	for i := range biased {
		var b byte
		for j := 0; j < 8; j++ {
			if rng.Float64() < 0.6 {
				b |= 1 << uint(j)
			}
		}
		biased[i] = b
	}
	if passed, total := Passed(NIST(biased)); passed > total/2 {
		t.Errorf("biased passed %d/%d", passed, total)
	}
	if r := Analyze(biased); r.MinEntropyBit > 0.8 || r.MarkovMinEntropy > 0.8 {
		t.Errorf("biased: %+v", r)
	}
}

func TestEstimators64(t *testing.T) {
	c := make([]uint64, 10000)
	if MinEntropy64(c) != 0 || Shannon64(c) != 0 || CondMinEntropy64(c) != 0 {
		t.Error("constant sequence should have zero entropy")
	}
	var seed [32]byte
	rng := mrand.New(mrand.NewChaCha8(seed))
	u := make([]uint64, 1<<16)
	for i := range u {
		u[i] = rng.Uint64N(256)
	}
	if h := MinEntropy64(u); h < 7.5 {
		t.Errorf("uniform MCV %v", h)
	}
	if h := Shannon64(u); h < 7.99 {
		t.Errorf("uniform shannon %v", h)
	}
	// Alternating sequence: zero conditional entropy, one bit of MCV.
	alt := make([]uint64, 1<<12)
	for i := range alt {
		alt[i] = uint64(i & 1)
	}
	if h := CondMinEntropy64(alt); h > 0.01 {
		t.Errorf("alternating cond %v", h)
	}
	if h := MinEntropy64(alt); h < 0.9 || h > 1 {
		t.Errorf("alternating MCV %v", h)
	}
}

func TestRank32(t *testing.T) {
	var id [32]uint32
	for i := range id {
		id[i] = 1 << uint(i)
	}
	if r := rank32(id); r != 32 {
		t.Errorf("identity rank %d", r)
	}
	var zero [32]uint32
	if r := rank32(zero); r != 0 {
		t.Errorf("zero rank %d", r)
	}
	dup := id
	dup[5] = dup[6]
	if r := rank32(dup); r != 31 {
		t.Errorf("duplicate row rank %d", r)
	}
}
