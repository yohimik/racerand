package racerand

import "math"

// EstimateMinEntropy returns the most-common-value min-entropy estimate of
// NIST SP 800-90B section 6.3.1 for a sequence of raw samples, in bits per
// sample. Reader uses it as a startup diagnostic.
//
// The confidence adjustment assumes IID samples. Race samples are correlated,
// so this is not a bound on their entropy rate. Oversampling does not establish
// such a bound either.
func EstimateMinEntropy(samples []uint64) float64 {
	if len(samples) < 2 {
		return 0
	}
	counts := make(map[uint64]int, 1024)
	mode := 0
	for _, s := range samples {
		counts[s]++
		if counts[s] > mode {
			mode = counts[s]
		}
	}
	n := float64(len(samples))
	p := float64(mode) / n
	// 99% upper confidence bound on the probability of the mode.
	pu := p + 2.576*math.Sqrt(p*(1-p)/(n-1))
	if pu >= 1 {
		return 0
	}
	return -math.Log2(pu)
}
