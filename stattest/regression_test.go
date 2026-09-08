package stattest

import (
	"encoding/json"
	"math"
	"testing"
)

func TestUnavailableResultsRoundTrip(t *testing.T) {
	results := NIST(nil)
	data, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []TestResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, r := range decoded {
		if !math.IsNaN(r.P) || r.Pass {
			t.Fatalf("unavailable test became a result: %+v", r)
		}
	}
}

func TestGammaBoundaries(t *testing.T) {
	for _, args := range [][2]float64{{math.NaN(), 1}, {1, math.NaN()}, {0, 1}, {1, -1}, {math.Inf(1), 1}} {
		if !math.IsNaN(Igam(args[0], args[1])) || !math.IsNaN(Igamc(args[0], args[1])) {
			t.Fatalf("invalid inputs: %v", args)
		}
	}
	if Igam(1, 0) != 0 || Igamc(1, 0) != 1 || Igam(1, math.Inf(1)) != 1 || Igamc(1, math.Inf(1)) != 0 {
		t.Fatal("boundary limits")
	}
	for _, a := range []float64{0.5, 1, 2, 127.5, 4096, 32768} {
		for _, factor := range []float64{0.5, 1, 2} {
			p, q := Igam(a, a*factor), Igamc(a, a*factor)
			if math.IsNaN(p) || p < 0 || p > 1 || math.Abs(p+q-1) > 1e-12 {
				t.Fatalf("a=%v x=%v P=%v Q=%v", a, a*factor, p, q)
			}
		}
	}
}

func TestMonobitReference(t *testing.T) {
	// Six ones and four zeros: two-sided normal-tail probability.
	b := []uint8{1, 0, 1, 1, 0, 1, 0, 1, 0, 1}
	const want = 0.5270892568655381
	if got := frequency(b); math.Abs(got-want) > 1e-12 {
		t.Fatalf("got %v want %v", got, want)
	}
}
