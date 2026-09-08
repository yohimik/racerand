package stattest

import (
	"encoding/json"
	"math"
)

// Alpha is the significance level at which a NIST test is considered passed.
const Alpha = 0.01

// TestResult is the outcome of one NIST SP 800-22 test.
type TestResult struct {
	Name string
	// P is the p-value. NaN means the test was not applicable to the input.
	P    float64
	Pass bool
	Note string
}

// MarshalJSON represents an unavailable p-value as null, not a fabricated zero.
func (r TestResult) MarshalJSON() ([]byte, error) {
	var p *float64
	if !math.IsNaN(r.P) && !math.IsInf(r.P, 0) {
		p = &r.P
	}
	return json.Marshal(struct {
		Name string
		P    *float64
		Pass bool
		Note string
	}{r.Name, p, r.Pass, r.Note})
}

// UnmarshalJSON restores null p-values as NaN.
func (r *TestResult) UnmarshalJSON(data []byte) error {
	var v struct {
		Name string
		P    *float64
		Pass bool
		Note string
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	r.Name, r.Pass, r.Note, r.P = v.Name, v.Pass, v.Note, math.NaN()
	if v.P != nil {
		r.P = *v.P
	} else {
		r.Pass = false
	}
	return nil
}

func result(name string, p float64) TestResult {
	r := TestResult{Name: name, P: p, Pass: !math.IsNaN(p) && p >= Alpha && p <= 1}
	if math.IsNaN(p) {
		r.Note = "not applicable: input too short"
	}
	return r
}

// NIST runs a subset of the NIST SP 800-22 statistical test suite on data,
// treating each byte as eight bits most significant first. The subset is
// Frequency, Block Frequency (M=128), Runs, Longest Run of Ones, Binary
// Matrix Rank, Cumulative Sums (forward and backward), Approximate Entropy
// (m=2), Serial (m=3) and Maurer's Universal test. All are O(n).
//
// A single sequence passing at alpha=0.01 is weak evidence of randomness; a
// sequence failing several tests by wide margins is strong evidence against.
func NIST(data []byte) []TestResult {
	b := toBits(data)
	res := []TestResult{
		result("Frequency (Monobit)", frequency(b)),
		result("Block Frequency (M=128)", blockFrequency(b, 128)),
		result("Runs", runs(b)),
		result("Longest Run of Ones", longestRun(b)),
		result("Binary Matrix Rank", rank(b)),
		result("Cumulative Sums (fwd)", cusum(b, false)),
		result("Cumulative Sums (bwd)", cusum(b, true)),
		result("Approximate Entropy (m=2)", approxEntropy(b, 2)),
	}
	p1, p2 := serial(b, 3)
	res = append(res,
		result("Serial (m=3) P1", p1),
		result("Serial (m=3) P2", p2),
		result("Maurer's Universal", universal(b)),
	)
	return res
}

// Passed counts passing results.
func Passed(results []TestResult) (passed, total int) {
	for _, r := range results {
		total++
		if r.Pass {
			passed++
		}
	}
	return passed, total
}

func toBits(data []byte) []uint8 {
	b := make([]uint8, 8*len(data))
	for i, x := range data {
		for j := 0; j < 8; j++ {
			b[8*i+j] = (x >> uint(7-j)) & 1
		}
	}
	return b
}

func frequency(b []uint8) float64 {
	n := len(b)
	if n == 0 {
		return math.NaN()
	}
	s := 0
	for _, x := range b {
		s += 2*int(x) - 1
	}
	sobs := math.Abs(float64(s)) / math.Sqrt(float64(n))
	return math.Erfc(sobs / math.Sqrt2)
}

func blockFrequency(b []uint8, m int) float64 {
	n := len(b)
	nb := n / m
	if nb == 0 {
		return math.NaN()
	}
	chi := 0.0
	for i := 0; i < nb; i++ {
		ones := 0
		for _, x := range b[i*m : (i+1)*m] {
			ones += int(x)
		}
		pi := float64(ones)/float64(m) - 0.5
		chi += pi * pi
	}
	chi *= 4 * float64(m)
	return Igamc(float64(nb)/2, chi/2)
}

func runs(b []uint8) float64 {
	n := len(b)
	if n < 2 {
		return math.NaN()
	}
	ones := 0
	for _, x := range b {
		ones += int(x)
	}
	fn := float64(n)
	pi := float64(ones) / fn
	if math.Abs(pi-0.5) >= 2/math.Sqrt(fn) {
		return 0
	}
	v := 1
	for k := 0; k+1 < n; k++ {
		if b[k] != b[k+1] {
			v++
		}
	}
	num := math.Abs(float64(v) - 2*fn*pi*(1-pi))
	den := 2 * math.Sqrt(2*fn) * pi * (1 - pi)
	return math.Erfc(num / den)
}

func longestRun(b []uint8) float64 {
	n := len(b)
	var m, k, vmin, vmax int
	var pi []float64
	switch {
	case n < 128:
		return math.NaN()
	case n < 6272:
		m, k, vmin, vmax = 8, 3, 1, 4
		pi = []float64{0.2148, 0.3672, 0.2305, 0.1875}
	case n < 750000:
		m, k, vmin, vmax = 128, 5, 4, 9
		pi = []float64{0.1174, 0.2430, 0.2493, 0.1752, 0.1027, 0.1124}
	default:
		m, k, vmin, vmax = 10000, 6, 10, 16
		pi = []float64{0.0882, 0.2092, 0.2483, 0.1933, 0.1208, 0.0675, 0.0727}
	}
	nb := n / m
	nu := make([]float64, k+1)
	for i := 0; i < nb; i++ {
		run, longest := 0, 0
		for _, x := range b[i*m : (i+1)*m] {
			if x == 1 {
				run++
				longest = max(longest, run)
			} else {
				run = 0
			}
		}
		c := min(max(longest, vmin), vmax) - vmin
		nu[c]++
	}
	chi := 0.0
	for i := 0; i <= k; i++ {
		e := float64(nb) * pi[i]
		d := nu[i] - e
		chi += d * d / e
	}
	return Igamc(float64(k)/2, chi/2)
}

func rank(b []uint8) float64 {
	const dim = 32
	nb := len(b) / (dim * dim)
	if nb < 38 {
		return math.NaN()
	}
	var f32, f31 int
	for i := 0; i < nb; i++ {
		var rows [dim]uint32
		base := i * dim * dim
		for r := 0; r < dim; r++ {
			var v uint32
			for c := 0; c < dim; c++ {
				v = v<<1 | uint32(b[base+r*dim+c])
			}
			rows[r] = v
		}
		switch rank32(rows) {
		case 32:
			f32++
		case 31:
			f31++
		}
	}
	fn := float64(nb)
	f30 := fn - float64(f32) - float64(f31)
	chi := math.Pow(float64(f32)-0.2888*fn, 2)/(0.2888*fn) +
		math.Pow(float64(f31)-0.5776*fn, 2)/(0.5776*fn) +
		math.Pow(f30-0.1336*fn, 2)/(0.1336*fn)
	return math.Exp(-chi / 2)
}

func rank32(rows [32]uint32) int {
	rank := 0
	for col := 31; col >= 0 && rank < 32; col-- {
		piv := -1
		for i := rank; i < 32; i++ {
			if rows[i]>>uint(col)&1 == 1 {
				piv = i
				break
			}
		}
		if piv < 0 {
			continue
		}
		rows[rank], rows[piv] = rows[piv], rows[rank]
		for i := 0; i < 32; i++ {
			if i != rank && rows[i]>>uint(col)&1 == 1 {
				rows[i] ^= rows[rank]
			}
		}
		rank++
	}
	return rank
}

func cusum(b []uint8, backward bool) float64 {
	n := len(b)
	if n == 0 {
		return math.NaN()
	}
	s, z := 0, 0
	for i := 0; i < n; i++ {
		x := b[i]
		if backward {
			x = b[n-1-i]
		}
		if x == 1 {
			s++
		} else {
			s--
		}
		if s < 0 {
			z = max(z, -s)
		} else {
			z = max(z, s)
		}
	}
	if z == 0 {
		return 0
	}
	sqn := math.Sqrt(float64(n))
	fz := float64(z)
	sum1 := 0.0
	for k := (-n/z + 1) / 4; k <= (n/z-1)/4; k++ {
		fk := float64(k)
		sum1 += NormalCDF((4*fk+1)*fz/sqn) - NormalCDF((4*fk-1)*fz/sqn)
	}
	sum2 := 0.0
	for k := (-n/z - 3) / 4; k <= (n/z-1)/4; k++ {
		fk := float64(k)
		sum2 += NormalCDF((4*fk+3)*fz/sqn) - NormalCDF((4*fk+1)*fz/sqn)
	}
	return 1 - sum1 + sum2
}

// patternCounts counts every m-bit pattern in the cyclic sequence.
func patternCounts(b []uint8, m int) []int {
	n := len(b)
	counts := make([]int, 1<<m)
	if m == 0 || n == 0 {
		return counts
	}
	mask := 1<<m - 1
	v := 0
	for i := 0; i < m-1; i++ {
		v = v<<1 | int(b[i%n])
	}
	for i := m - 1; i < n; i++ {
		v = (v<<1 | int(b[i])) & mask
		counts[v]++
	}
	for i := 0; i < m-1; i++ {
		v = (v<<1 | int(b[i])) & mask
		counts[v]++
	}
	return counts
}

func approxEntropy(b []uint8, m int) float64 {
	n := len(b)
	if n < 1<<(m+1) {
		return math.NaN()
	}
	phi := func(m int) float64 {
		if m == 0 {
			return 0
		}
		sum := 0.0
		for _, c := range patternCounts(b, m) {
			if c > 0 {
				p := float64(c) / float64(n)
				sum += p * math.Log(p)
			}
		}
		return sum
	}
	apen := phi(m) - phi(m+1)
	chi := 2 * float64(n) * (math.Ln2 - apen)
	return Igamc(math.Pow(2, float64(m-1)), chi/2)
}

func serial(b []uint8, m int) (p1, p2 float64) {
	n := len(b)
	if n < 1<<m {
		return math.NaN(), math.NaN()
	}
	psi2 := func(m int) float64 {
		if m <= 0 {
			return 0
		}
		sum := 0.0
		for _, c := range patternCounts(b, m) {
			sum += float64(c) * float64(c)
		}
		return sum*math.Pow(2, float64(m))/float64(n) - float64(n)
	}
	pm, pm1, pm2 := psi2(m), psi2(m-1), psi2(m-2)
	del1 := pm - pm1
	del2 := pm - 2*pm1 + pm2
	p1 = Igamc(math.Pow(2, float64(m-2)), del1/2)
	p2 = Igamc(math.Pow(2, float64(m-3)), del2/2)
	return p1, p2
}

var (
	universalExpected = [17]float64{0, 0, 0, 0, 0, 0, 5.2177052, 6.1962507, 7.1836656, 8.1764248, 9.1723243,
		10.170032, 11.168765, 12.168070, 13.167693, 14.167488, 15.167379}
	universalVariance = [17]float64{0, 0, 0, 0, 0, 0, 2.954, 3.125, 3.238, 3.311, 3.356,
		3.384, 3.401, 3.410, 3.416, 3.419, 3.421}
)

func universal(b []uint8) float64 {
	n := len(b)
	thresholds := []int{387840, 904960, 2068480, 4654080, 10342400, 22753280, 49643520,
		107560960, 231669760, 496435200, 1059061760}
	l := 5
	for _, t := range thresholds {
		if n >= t {
			l++
		}
	}
	if l < 6 {
		return math.NaN()
	}
	q := 10 << uint(l)
	k := n/l - q
	if k <= 0 {
		return math.NaN()
	}
	table := make([]int, 1<<uint(l))
	block := func(i int) int {
		v := 0
		for _, x := range b[(i-1)*l : i*l] {
			v = v<<1 | int(x)
		}
		return v
	}
	for i := 1; i <= q; i++ {
		table[block(i)] = i
	}
	sum := 0.0
	for i := q + 1; i <= q+k; i++ {
		v := block(i)
		sum += math.Log2(float64(i - table[v]))
		table[v] = i
	}
	fn := sum / float64(k)
	fl := float64(l)
	fk := float64(k)
	c := 0.7 - 0.8/fl + (4+32/fl)*math.Pow(fk, -3/fl)/15
	sigma := c * math.Sqrt(universalVariance[l]/fk)
	return math.Erfc(math.Abs(fn-universalExpected[l]) / (math.Sqrt2 * sigma))
}
