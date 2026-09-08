GO ?= go
BENCHCOUNT ?= 3

.PHONY: all test race vet bench bench-quick gobench report clean

all: vet test

vet:
	$(GO) vet ./...

test:
	$(GO) test -count=1 -timeout 300s ./...

race:
	$(GO) test -race -count=1 -timeout 600s ./...

# Standard Go benchmarks (testing.B). Written to bench.txt.
gobench:
	$(GO) test -v -run '^$$' -bench . -benchmem -benchtime 300ms -count $(BENCHCOUNT) -timeout 30m . > bench.txt
	@cat bench.txt

# Full comparison report. Takes a few minutes; writes BENCHMARKS.md, bench.json and noise/.
bench: gobench
	$(GO) run ./cmd/racerand-bench -out BENCHMARKS.md -json bench.json -gobench bench.txt -noise noise

# Regenerate the report and example-output images from saved results without re-measuring.
report:
	$(GO) run ./cmd/racerand-bench -from bench.json -out BENCHMARKS.md -noise ''

bench-quick:
	$(GO) run ./cmd/racerand-bench -quick -out BENCHMARKS.quick.md -noise ''

clean:
	rm -f BENCHMARKS.quick.md
