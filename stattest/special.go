package stattest

import "math"

// These routines evaluate the incomplete gamma power series and continued
// fraction using a bounded iteration count. Formula references:
// https://dlmf.nist.gov/8.7.E1 and https://dlmf.nist.gov/8.9.
// They are intended for the parameter ranges used by this diagnostic suite,
// not as a general-purpose numerical library.
const gammaTolerance = 2e-15
const gammaIterations = 100000

// Igam is the regularized lower incomplete gamma function P(a,x).
// Invalid inputs or failure to converge return NaN.
func Igam(a, x float64) float64 {
	p, _ := gammaPair(a, x)
	return p
}

// Igamc is the regularized upper incomplete gamma function Q(a,x).
// Invalid inputs or failure to converge return NaN.
func Igamc(a, x float64) float64 {
	_, q := gammaPair(a, x)
	return q
}

func gammaPair(a, x float64) (float64, float64) {
	if math.IsNaN(a) || math.IsNaN(x) || a <= 0 || x < 0 || math.IsInf(a, 0) {
		return math.NaN(), math.NaN()
	}
	if x == 0 {
		return 0, 1
	}
	if math.IsInf(x, 1) {
		return 1, 0
	}
	lg, _ := math.Lgamma(a)
	logScale := a*math.Log(x) - x - lg
	if x < a+1 {
		term, sum := 1.0, 1.0
		for k := 1; k <= gammaIterations; k++ {
			term *= x / (a + float64(k))
			sum += term
			if math.Abs(term) <= math.Abs(sum)*gammaTolerance {
				p := math.Exp(logScale - math.Log(a) + math.Log(sum))
				p = math.Max(0, math.Min(1, p))
				return p, 1 - p
			}
		}
	} else {
		// Modified Lentz evaluation of the upper-tail continued fraction.
		const tiny = 1e-300
		floor := func(v float64) float64 {
			if math.Abs(v) < tiny {
				return math.Copysign(tiny, v)
			}
			return v
		}
		denominator := x + 1 - a
		c := 1 / tiny
		d := 1 / floor(denominator)
		fraction := d
		for k := 1; k <= gammaIterations; k++ {
			fk := float64(k)
			numerator := fk * (a - fk)
			denominator += 2
			d = 1 / floor(denominator+numerator*d)
			c = floor(denominator + numerator/c)
			change := c * d
			fraction *= change
			if math.Abs(change-1) <= gammaTolerance {
				q := math.Exp(logScale) * fraction
				q = math.Max(0, math.Min(1, q))
				return 1 - q, q
			}
		}
	}
	return math.NaN(), math.NaN()
}

// NormalCDF is the standard normal cumulative distribution function.
func NormalCDF(x float64) float64 { return 0.5 * math.Erfc(-x/math.Sqrt2) }
