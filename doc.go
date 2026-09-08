// Package racerand explores goroutine scheduling as a source of random noise.
// Workers contend for a counter; a sampler records changes, poll counts, and
// worker ids. The default source uses atomics and has no intentional data race.
// SourceUnsynchronized is a separate, deliberately unsafe experiment.
//
// ModeDRBG seeds a ChaCha8 or AES-256-CTR generator from SHA-512-conditioned
// samples and reseeds after a configurable number of output bytes. ModeTRNG
// hashes fresh samples for each output block. The name does not imply that
// true randomness or full entropy has been established. ModeRaw exposes folded,
// biased samples for inspection.
//
// Reader implements io.Reader and math/rand/v2.Source and serializes callers.
// New requires GOMAXPROCS >= 2, checks a startup frequency estimate, and runs
// repetition-count and adaptive-proportion checks. Source failures remain
// until Reset; callers must handle them. Close stops the workers.
//
// This is an experimental library, not a validated cryptographic entropy source.
// Statistical tests cannot prove unpredictability, and hashing cannot create
// missing entropy. Use crypto/rand for secrets. WithOSEntropyMix adds OS bytes
// to DRBG seeds but does not establish the security of this construction.
package racerand
