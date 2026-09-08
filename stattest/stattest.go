// Package stattest implements a compact set of randomness tests for byte
// sequences: the summary statistics of John Walker's ent, a few min-entropy
// estimators from NIST SP 800-90B, and a subset of the NIST SP 800-22
// statistical test suite.
//
// It provides diagnostics for local experiments without external tools.
// It is not a substitute for the full
// SP 800-22 or SP 800-90B tool chains.
package stattest

import (
	"bytes"
	"compress/flate"
	"math"
	"math/bits"
)

// Report holds ent-style summary statistics plus min-entropy estimates for a
// byte sequence.
type Report struct {
	Bytes int

	// ShannonEntropy is the empirical entropy of the byte distribution in
	// bits per byte. 8 is ideal.
	ShannonEntropy float64
	// MinEntropyByte is the SP 800-90B most-common-value estimate on bytes,
	// in bits per byte.
	MinEntropyByte float64
	// MinEntropyBit is the most-common-value estimate on bits, in bits per
	// bit.
	MinEntropyBit float64
	// MarkovMinEntropy is a first-order binary Markov diagnostic inspired by
	// SP 800-90B 6.3.3, in bits/bit. It omits the standard's confidence
	// adjustments and is not a conforming entropy assessment.
	MarkovMinEntropy float64
	// CondMinEntropy is -log2 of the probability of guessing the next byte
	// given the previous byte with the best possible strategy, in bits per
	// byte. It is a first-order check for serial dependence.
	CondMinEntropy float64

	// ChiSquare is the chi-square statistic of the byte distribution against
	// uniform (255 degrees of freedom) and ChiSquareP its p-value. Values of
	// ChiSquareP very close to 0 or 1 indicate non-randomness.
	ChiSquare  float64
	ChiSquareP float64
	// Mean is the arithmetic mean of the bytes. 127.5 is ideal.
	Mean float64
	// MonteCarloPi estimates pi from consecutive 6-byte points; ErrPct is the
	// relative error in percent.
	MonteCarloPi       float64
	MonteCarloPiErrPct float64
	// SerialCorrelation is the correlation between consecutive bytes. 0 is
	// ideal.
	SerialCorrelation float64
	// CompressionRatio is the size after deflate divided by the input size.
	// Random data does not compress, so about 1.0 is ideal.
	CompressionRatio float64
}

// Analyze computes a Report for data.
func Analyze(data []byte) Report {
	n := len(data)
	r := Report{Bytes: n}
	if n == 0 {
		return r
	}
	fn := float64(n)

	var counts [256]int
	ones := 0
	for _, b := range data {
		counts[b]++
		ones += bits.OnesCount8(b)
	}
	maxc := 0
	for _, c := range counts {
		if c > 0 {
			p := float64(c) / fn
			r.ShannonEntropy -= p * math.Log2(p)
		}
		maxc = max(maxc, c)
	}
	r.MinEntropyByte = mcv(maxc, n)
	r.MinEntropyBit = mcv(max(ones, 8*n-ones), 8*n)
	r.MarkovMinEntropy = markov(data)
	r.CondMinEntropy = condMinEntropy(data)

	exp := fn / 256
	for _, c := range counts {
		d := float64(c) - exp
		r.ChiSquare += d * d / exp
	}
	r.ChiSquareP = Igamc(255.0/2, r.ChiSquare/2)

	sum := 0.0
	for i, c := range counts {
		sum += float64(i) * float64(c)
	}
	r.Mean = sum / fn

	r.MonteCarloPi, r.MonteCarloPiErrPct = monteCarloPi(data)
	r.SerialCorrelation = serialCorrelation(data)
	r.CompressionRatio = compressionRatio(data)
	return r
}

// mcv is the SP 800-90B 6.3.1 most-common-value estimate: -log2 of the 99%
// upper confidence bound on the probability of the mode.
func mcv(maxCount, n int) float64 {
	if n < 2 {
		return 0
	}
	p := float64(maxCount) / float64(n)
	pu := p + 2.576*math.Sqrt(p*(1-p)/float64(n-1))
	if pu >= 1 {
		return 0
	}
	return -math.Log2(pu)
}

// MinEntropy64 is the most-common-value estimate for 64-bit samples, in bits
// per sample.
func MinEntropy64(samples []uint64) float64 {
	if len(samples) == 0 {
		return 0
	}
	counts := make(map[uint64]int, 4096)
	maxc := 0
	for _, s := range samples {
		counts[s]++
		maxc = max(maxc, counts[s])
	}
	return mcv(maxc, len(samples))
}

// Shannon64 is the empirical entropy of 64-bit samples in bits per sample.
func Shannon64(samples []uint64) float64 {
	if len(samples) == 0 {
		return 0
	}
	counts := make(map[uint64]int, 4096)
	for _, s := range samples {
		counts[s]++
	}
	n := float64(len(samples))
	e := 0.0
	for _, c := range counts {
		p := float64(c) / n
		e -= p * math.Log2(p)
	}
	return e
}

// CondMinEntropy64 is -log2 of the probability of guessing the next sample
// given the previous one with the best strategy, in bits per sample. Because
// it is estimated from pair frequencies it needs many more samples than
// distinct values to be meaningful. Sparse observations bias this plug-in
// diagnostic; it is not an entropy bound or an SP 800-90B estimator.
func CondMinEntropy64(samples []uint64) float64 {
	if len(samples) < 2 {
		return 0
	}
	type key struct{ a, b uint64 }
	pairs := make(map[key]int, 4096)
	for i := 1; i < len(samples); i++ {
		pairs[key{samples[i-1], samples[i]}]++
	}
	best := make(map[uint64]int, 4096)
	for k, c := range pairs {
		best[k.a] = max(best[k.a], c)
	}
	sum := 0
	for _, c := range best {
		sum += c
	}
	return -math.Log2(float64(sum) / float64(len(samples)-1))
}

func markov(data []byte) float64 {
	var n [2]int
	var t [2][2]int
	prev := -1
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bit := int(b>>uint(i)) & 1
			n[bit]++
			if prev >= 0 {
				t[prev][bit]++
			}
			prev = bit
		}
	}
	L := float64(n[0] + n[1])
	if L == 0 {
		return 0
	}
	p0 := float64(n[0]) / L
	p1 := float64(n[1]) / L
	var p00, p01, p10, p11 float64
	if f := t[0][0] + t[0][1]; f > 0 {
		p00 = float64(t[0][0]) / float64(f)
		p01 = float64(t[0][1]) / float64(f)
	}
	if f := t[1][0] + t[1][1]; f > 0 {
		p10 = float64(t[1][0]) / float64(f)
		p11 = float64(t[1][1]) / float64(f)
	}
	seqs := [6]float64{
		p0 * math.Pow(p00, 127),
		p0 * math.Pow(p01, 64) * math.Pow(p10, 63),
		p0 * p01 * math.Pow(p11, 126),
		p1 * p10 * math.Pow(p00, 126),
		p1 * math.Pow(p10, 64) * math.Pow(p01, 63),
		p1 * math.Pow(p11, 127),
	}
	pmax := 0.0
	for _, s := range seqs {
		pmax = math.Max(pmax, s)
	}
	if pmax <= 0 {
		return 1
	}
	return math.Min(-math.Log2(pmax)/128, 1)
}

func condMinEntropy(data []byte) float64 {
	if len(data) < 2 {
		return 0
	}
	pair := new([256][256]int)
	for i := 1; i < len(data); i++ {
		pair[data[i-1]][data[i]]++
	}
	sum := 0
	for a := range pair {
		m := 0
		for _, c := range pair[a] {
			m = max(m, c)
		}
		sum += m
	}
	return -math.Log2(float64(sum) / float64(len(data)-1))
}

func monteCarloPi(data []byte) (pi, errPct float64) {
	const monten = 6
	incirc := math.Pow(math.Pow(256, monten/2)-1, 2)
	total, inside := 0, 0
	for i := 0; i+monten <= len(data); i += monten {
		x := float64(int(data[i])<<16 | int(data[i+1])<<8 | int(data[i+2]))
		y := float64(int(data[i+3])<<16 | int(data[i+4])<<8 | int(data[i+5]))
		total++
		if x*x+y*y <= incirc {
			inside++
		}
	}
	if total == 0 {
		return 0, 100
	}
	pi = 4 * float64(inside) / float64(total)
	return pi, math.Abs(pi-math.Pi) / math.Pi * 100
}

func serialCorrelation(data []byte) float64 {
	n := len(data)
	if n < 2 {
		return 0
	}
	var t1, t2, t3 float64
	for i := 0; i < n; i++ {
		u := float64(data[i])
		next := float64(data[(i+1)%n])
		t1 += u * next
		t2 += u
		t3 += u * u
	}
	fn := float64(n)
	den := fn*t3 - t2*t2
	if den == 0 {
		return 1
	}
	return (fn*t1 - t2*t2) / den
}

func compressionRatio(data []byte) float64 {
	if len(data) == 0 {
		return 0
	}
	var buf bytes.Buffer
	w, err := flate.NewWriter(&buf, flate.DefaultCompression)
	if err != nil {
		return math.NaN()
	}
	w.Write(data)
	w.Close()
	return float64(buf.Len()) / float64(len(data))
}
