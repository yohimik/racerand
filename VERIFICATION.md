# Verification notes

Reviewed on 2026-09-08 using an Apple M5 Pro (darwin/arm64).

The review corrected the reseed boundary for short intervals, bounded memory
allocation from configuration values, and made source failures stop persistent
workers. It also added explicit behavior for zero-value readers and fixed
command-line output errors, report loading, image paths, and unavailable
p-values in JSON. The frequency estimator now uses the SP 800-90B sample-size
adjustment. The numerical helper has bounded iteration and input checks.

The README and generated report distinguish distribution diagnostics from
entropy bounds. Claims about guaranteed security, snapshot resistance,
novelty, and superiority over jitterentropy were removed or qualified.

## Local checks

- Formatting, `go vet ./...`, and the test suite passed with Go 1.26.5.
- Tests passed with Go 1.24.0 and `GOMAXPROCS=2`.
- Race-detector tests passed with Go 1.26.5, both at `GOMAXPROCS=2` and at the
  host default of 15. Deliberately unsynchronized source tests are excluded.
- Builds succeeded for darwin/arm64, linux/amd64, linux/386, and windows/amd64.
  Cross-compilation does not test runtime behavior on those systems.
- CLI checks covered all three modes, exact hexadecimal output length,
  invalid arguments, a read-only stdout descriptor, and `GOMAXPROCS=1` rejection.
- Benchmark CLI checks covered invalid sizes, invalid flag combinations,
  loading saved measurements, and rendering to a different directory.
- A scan of publication files found no private keys, common access-token
  patterns, local user paths, or assistant-specific text.

## Reproducing the results

`make bench` runs the Go benchmarks three times and then collects the comparison
report and images. It writes `bench.txt`, `bench.json`, `BENCHMARKS.md`, and
`noise/`. `make report` renders the saved data without resampling.

The committed CI workflow checks Go 1.24 and the current stable Go release on
Linux, macOS, and Windows. Race detection also runs on Linux and macOS. See the
repository's Actions page for actual hosted results; this file records local
verification, not a promise that every runner produces usable noise.

Hosted checks also exposed two portability issues: Windows checkout line
endings and a timing-baseline test that assumed raw samples could never be
constant. The repository now specifies LF text files. Reader tests check
buffering and byte counts independently of host timer resolution; distribution
quality remains a measurement reported by the benchmark harness.

## What these checks do not establish

These checks exercise implementation behavior. They do not validate the source's
entropy assumption, prove cryptographic security, or establish independence
between samples. The official NIST assessment tools, full NIST STS, dieharder,
and PractRand were not run. The bundled statistical routines are diagnostics,
not a certification suite. Source rejection is a supported result on a host
whose observations fail the configured checks.
