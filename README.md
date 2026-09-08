# racerand

An experiment in generating random noise from goroutine scheduling, written in
Go with only the standard library.

Workers compete over a shared counter. A sampler records how much it changes
and how long it takes to notice. The library can return those observations,
hash them into blocks, or use them to seed a fast pseudorandom generator.

The interesting question is how much useful variation comes from scheduling,
and how it changes with worker count, synchronization, and CPU load. The
repository includes benchmarks, statistical diagnostics, and noise images to
make that question easier to explore.

**This is research software. Use `crypto/rand` for keys, tokens, and other
secrets.** Passing the included tests does not establish true randomness or
cryptographic security.

## Try it

Requires Go 1.24 or newer and `GOMAXPROCS >= 2`.

```sh
go get github.com/yohimik/racerand
```

```go
package main

import (
    "fmt"
    "log"
    "math/rand/v2"

    "github.com/yohimik/racerand"
)

func main() {
    r, err := racerand.New()
    if err != nil {
        log.Fatal(err)
    }
    defer r.Close()

    sample := make([]byte, 32)
    if _, err := r.Read(sample); err != nil {
        log.Fatal(err)
    }
    fmt.Printf("sample: %x\n", sample)

    rng := rand.New(r)
    fmt.Println(rng.IntN(100))
}
```

`Reader` is safe to share between goroutines. `Read` fills the whole buffer or
clears it and returns an error. Source errors remain until `Reset`, which runs
startup checks again. `Uint64` panics on failure because `rand.Source` has no
error return. Construct readers with `New`; the zero value cannot generate data.

## What gets generated

| Mode | Output | Purpose |
|---|---|---|
| `ModeDRBG` (default) | ChaCha8 or AES-256-CTR, seeded from conditioned samples | Compare a race-seeded PRNG with standard generators |
| `ModeTRNG` | A SHA-512 digest of fresh samples for each 64-byte block | Study conditioning without deterministic expansion |
| `ModeRaw` | One byte made by XOR-folding each raw sample | Inspect bias and correlation |

`ModeTRNG` is an API name, not a claim of validated true randomness. Fresh
samples can still be correlated or predictable. DRBG output is deterministic
between reseeds.

The default source uses atomic operations. `SourceUnsynchronized` uses plain
loads and stores and deliberately creates data races. It is included for
comparison and is excluded from race-detector tests.

## How sampling works

Each worker increments a counter, stores its id, and performs a short loop
whose length depends on the state it observed. The sampler polls the counter
until it changes, yielding periodically and stopping after a bounded number of
polls if nothing changes.

| Sample bits | Observation |
|---|---|
| 0–47 | Counter delta since the previous sample |
| 48–55 | Low eight bits of the poll count |
| 56–63 | Last observed worker id |

The counter and worker id are read separately. They need not describe the same
worker operation. Scheduling, cache contention, instrumentation, and other
work on the host can all affect the observations.

Reader applies repetition-count and adaptive-proportion checks inspired by
[NIST SP 800-90B](https://csrc.nist.gov/pubs/sp/800/90/b/final). At startup it
also measures the frequency of the most common sample. The default assumption
is 0.5 bits per sample, with twice the nominal sample count fed to SHA-512:
1024 samples per DRBG seed and 2048 per conditioned block.

That assumption has **not** been validated. A frequency estimate does not
account for all dependence between samples, and doubling the sample count
does not fix that limitation. This is not a complete SP 800-90B assessment or
a conforming SP 800-90A DRBG implementation.

## Options

| Option | Default | Effect |
|---|---|---|
| `WithMode(m)` | `ModeDRBG` | Select output mode |
| `WithDRBG(k)` | `DRBGChaCha8` | Select ChaCha8 or AES-256-CTR |
| `WithWorkers(n)` | `GOMAXPROCS-1`, capped at 256 | Set worker count; 0 selects the default |
| `WithMinEntropy(h)` | 0.5 | Set assumed bits/sample, from 1/1024 to 56 |
| `WithReseedInterval(n)` | 1 MiB | Limit DRBG output between reseeds |
| `WithOSEntropyMix()` | Off | Add 32 OS bytes to each DRBG seed; no effect in other modes |
| `WithPersistentWorkers()` | Off | Keep workers running between reads; close the reader when finished |
| `WithStartupSamples(n)` | 4096 | Set startup sample count, from 1024 to 1048576 |
| `WithHealthAlpha(a)` | 2⁻³⁰ | Set nominal per-test false-positive parameter, between 0 and 1 |
| `WithSource(k)` | `SourceAtomic` | Select atomic or deliberately unsynchronized access |
| `WithoutFeedback()` | Feedback enabled | Remove the variable-length worker loop |
| `WithoutHealthTests()` | Checks enabled | Disable source rejection for controlled experiments |

Persistent workers busy-loop; they are not pinned to cores. A failed harvest
stops them. The default DRBG runs workers only during startup and reseeding.

`Harvester` exposes full 64-bit samples. Its sampling methods require a single
consumer and must not overlap `Start`. Package-level `Read` and `Default`
provide a lazily initialized shared Reader.

## Measurements and example noise

[BENCHMARKS.md](BENCHMARKS.md) contains throughput, allocation counts, startup
and reseed latency, statistical diagnostics, and sweeps across worker counts
and `GOMAXPROCS`. [bench.json](bench.json) contains the data. These are
observations from the machine and date shown in the report, not portable
performance or entropy guarantees.

The comparisons include `crypto/rand`, `math/rand/v2` ChaCha8 and PCG,
`math/rand`, and a small timing-jitter baseline written for this project.
**The baseline is not jitterentropy or HAVEGE**, so these results do not rank
racerand against those implementations. Wall time multiplied by worker count
is only a rough occupancy estimate, not a measurement of CPU time or energy.

Each row below shows 64 KiB as grayscale pixels and a density plot of
consecutive byte pairs. Lag plots use a separate logarithmic brightness scale
for each generator; compare structure, not brightness. Images are collected
separately from the statistical test samples.

| | Raw race samples | Race-seeded ChaCha8 | crypto/rand | Timing-jitter baseline |
|---|---|---|---|---|
| Bytes | ![Raw race bytes](noise/racerand-raw-atomic-bytes.png) | ![Race-seeded bytes](noise/racerand-drbg-chacha8-bytes.png) | ![OS random bytes](noise/crypto-rand-bytes.png) | ![Jitter bytes](noise/jitter-raw-bytes.png) |
| Byte pairs | ![Raw race pairs](noise/racerand-raw-atomic-lag.png) | ![Race-seeded pairs](noise/racerand-drbg-chacha8-lag.png) | ![OS random pairs](noise/crypto-rand-lag.png) | ![Jitter pairs](noise/jitter-raw-lag.png) |

The [full image comparison](BENCHMARKS.md#what-the-output-looks-like) includes
bitmaps and hexadecimal examples for every generator. Visually uniform noise
does not prove unpredictability: a deterministic PRNG can look the same.

## Related work and practical uses

The broad idea predates this project. Teh, Alawida, and Samsudin's 2020 paper,
[Generating True Random Numbers Based on Multicore CPU Using Race Conditions
and Chaotic Maps](https://doi.org/10.1007/s13369-020-04552-0), studies competing
threads and chaotic postprocessing. This repository is not a reproduction of
that paper and does not inherit its security claims.

[Jitterentropy](https://github.com/smuellerDD/jitterentropy-library) collects
CPU execution timing variation and includes an entropy assessment methodology.
[haveged](https://github.com/jirka-h/haveged) is an entropy daemon based on
HAVEGE. They are relevant prior art, but neither is implemented by the small
timing baseline in this repository.

The practical uses here are teaching concurrency, comparing sampling methods,
and measuring how scheduling changes the distribution of observations. A
supplementary entropy source is a possible research direction, not a demonstrated
security benefit. There is no established advantage over standard generators
for applications, simulations, games, or cryptographic use.

## Limits of the experiment

This work does not establish an entropy lower bound, resistance to an attacker
sharing the host, or independence from the OS random generator's failure modes.
Different sequences across runs are not proof of true randomness. The source
is also not a reliable detector of virtual machines, snapshots, or replay.

Hashing can hide visible bias without making predictable input unpredictable.
The included frequency, pair-frequency, and Markov diagnostics are useful for
finding problems, but they are not a substitute for the full
[NIST entropy assessment](https://github.com/usnistgov/SP800-90B_EntropyAssessment).
The SP 800-22 subset reports eleven p-values, some from the same test family;
occasional failures are expected even for good generators.

OS mixing adds another input to DRBG seeds. It does not certify the custom
construction or make it a better choice than using `crypto/rand` directly.
Go does not guarantee that clearing a buffer erases every copy of its contents.

## Run the tools

```sh
go run ./cmd/racerand -n 1048576 > random.bin
go run ./cmd/racerand -mode raw -n 4194304 | ent
go run ./cmd/racerand -mode trng -hex -n 32 -stats

make test        # unit and live-source tests
make race        # synchronization checks; deliberate data races excluded
make gobench     # standard Go benchmarks, saved to bench.txt
make bench       # full report, JSON data, and noise images
make report      # render saved results without collecting new samples
make bench-quick # shorter comparison run
```

Source checks can reject a host or a particular run. Benchmarks record these
failures rather than assuming every configuration will work. API tests disable
health checks where their purpose is to exercise buffering or synchronization;
separate tests cover health-check logic and live acceptance. Race instrumentation
changes timing and is not an entropy measurement.

The `stattest` package and benchmark commands also use only the standard
library. External tools such as `ent` are optional and are not run by CI.

## License

MIT. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
