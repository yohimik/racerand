package main

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yohimik/racerand"
	"github.com/yohimik/racerand/stattest"
)

func TestReportIncludesTestsAfterSourceRejection(t *testing.T) {
	r := Result{Quality: []QualityRow{
		{Name: "rejected", Err: "source failed"},
		{Name: "reference", NIST: []stattest.TestResult{{Name: "Frequency", P: 0.5, Pass: true}}, Passed: 1, Total: 1},
	}, Noise: []NoiseRow{{Name: "incomplete"}}}
	text := markdown(r)
	if !strings.Contains(text, "Frequency") || !strings.Contains(text, "reference | 1/1") {
		t.Fatal("missing reference tests after source error")
	}
}

func TestUint64RejectionReported(t *testing.T) {
	g := generator{name: "rejected", u64: staticU64(func() uint64 { panic(racerand.ErrHealthTestFailed) })}
	r := measureOneUint64(g, time.Millisecond)
	if r.Err == "" || r.NsPerOp != 0 {
		t.Fatalf("%+v", r)
	}
}

func TestNoiseOutputError(t *testing.T) {
	_, err := measureNoise(nil, filepath.Join(os.DevNull, "images"), 65536)
	if err == nil {
		t.Fatal("output errors must propagate")
	}
}

func TestRelativeImagePaths(t *testing.T) {
	dir := t.TempDir()
	r := Result{Noise: []NoiseRow{{Gray: filepath.Join(dir, "noise", "a.png")}}}
	got := relativeNoise(r, filepath.Join(dir, "reports", "report.md"))
	if got.Noise[0].Gray != "../noise/a.png" || r.Noise[0].Gray == got.Noise[0].Gray {
		t.Fatal("incorrect relative path or input mutated")
	}
	resolveLoadedNoise(&got, filepath.Join(dir, "reports", "report.json"))
	if filepath.Clean(got.Noise[0].Gray) != r.Noise[0].Gray {
		t.Fatal("path did not round-trip")
	}
}

func TestSourceReaderFillsOddBuffers(t *testing.T) {
	for _, n := range []int{0, 1, 7, 8, 9, 17} {
		buf := make([]byte, n)
		r := sourceReader{next: func() uint64 { return math.MaxUint64 }}
		if read, err := io.ReadFull(r, buf); read != n || err != nil {
			t.Fatalf("%d %v", read, err)
		}
		for _, v := range buf {
			if v != 255 {
				t.Fatal("unfilled tail")
			}
		}
	}
}

func TestThroughputError(t *testing.T) {
	_, err := throughput(errorReader{}, 32, time.Millisecond)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
