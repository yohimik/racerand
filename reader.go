package racerand

import (
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"runtime"
	"sync"
)

var (
	// ErrUninitialized is returned by a zero-value Reader. Construct readers with New.
	ErrUninitialized = errors.New("racerand: reader must be created with New")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("racerand: reader is closed")
	// ErrNotEnoughProcs is returned by New when GOMAXPROCS is 1.
	ErrNotEnoughProcs = errors.New("racerand: GOMAXPROCS must be at least 2 for goroutines to race")
	// ErrInsufficientEntropy is returned by New when the startup estimate is
	// below the configured minimum.
	ErrInsufficientEntropy = errors.New("racerand: startup min-entropy estimate below the configured minimum")
	// ErrHealthTestFailed is matched by every *HealthError.
	ErrHealthTestFailed = errors.New("racerand: continuous health test failed")
)

// Mode selects what a Reader outputs.
type Mode uint8

const (
	// ModeDRBG outputs from a deterministic generator seeded with conditioned
	// race entropy and periodically reseeded. Fast; workers only run during
	// reseeds.
	ModeDRBG Mode = iota
	// ModeTRNG outputs fresh SHA-512 digests of raw samples. Every 64-byte
	// block uses fresh samples; independence between blocks is not established.
	ModeTRNG
	// ModeRaw outputs one folded byte per raw sample with no conditioning.
	// Health tests still run. Research only.
	ModeRaw
)

func (m Mode) String() string {
	switch m {
	case ModeDRBG:
		return "drbg"
	case ModeTRNG:
		return "trng"
	case ModeRaw:
		return "raw"
	default:
		return fmt.Sprintf("Mode(%d)", uint8(m))
	}
}

const (
	// safetyFactor multiplies the number of raw samples fed to the
	// conditioner beyond what the assumed min-entropy strictly requires.
	safetyFactor = 2.0
	seedDomain   = "racerand/v1/seed\x00"
	trngDomain   = "racerand/v1/trng\x00"
	aptWindow    = 512
	maxSamples   = 1 << 20
)

type config struct {
	mode           Mode
	drbg           DRBGKind
	harvester      HarvesterConfig
	minEntropy     float64
	reseedInterval int
	startupSamples int
	alpha          float64
	mixOS          bool
	persistent     bool
	skipHealth     bool
}

func defaultConfig() config {
	return config{
		mode:           ModeDRBG,
		drbg:           DRBGChaCha8,
		minEntropy:     0.5,
		reseedInterval: 1 << 20,
		startupSamples: 4096,
		alpha:          math.Exp2(-30),
	}
}

func (c *config) validate() error {
	if c.mode > ModeRaw {
		return fmt.Errorf("racerand: invalid mode %v", c.mode)
	}
	if c.drbg > DRBGAESCTR {
		return fmt.Errorf("racerand: invalid DRBG %v", c.drbg)
	}
	if c.harvester.Source > SourceUnsynchronized {
		return fmt.Errorf("racerand: invalid source %v", c.harvester.Source)
	}
	if c.harvester.Workers < 0 || c.harvester.Workers > 256 {
		return errors.New("racerand: workers must be in [0, 256]")
	}
	if !(c.minEntropy >= 1.0/1024 && c.minEntropy <= 56) {
		return errors.New("racerand: min-entropy must be in [1/1024, 56] bits per sample")
	}
	if c.reseedInterval <= 0 {
		return errors.New("racerand: reseed interval must be positive")
	}
	if c.startupSamples < 1024 || c.startupSamples > maxSamples {
		return errors.New("racerand: startup samples must be in [1024, 1048576]")
	}
	if !(c.alpha > 0 && c.alpha < 1) {
		return errors.New("racerand: health alpha must be in (0, 1)")
	}
	return nil
}

// Option configures a Reader.
type Option func(*config)

// WithMode selects the output mode. The default is ModeDRBG.
func WithMode(m Mode) Option { return func(c *config) { c.mode = m } }

// WithDRBG selects the deterministic generator for ModeDRBG. The default is
// DRBGChaCha8.
func WithDRBG(k DRBGKind) Option { return func(c *config) { c.drbg = k } }

// WithWorkers sets the number of racing goroutines. The default is
// max(1, GOMAXPROCS-1).
func WithWorkers(n int) Option { return func(c *config) { c.harvester.Workers = n } }

// WithSource selects the access discipline of the raw source. The default is
// SourceAtomic. SourceUnsynchronized is a data race and is for research only.
func WithSource(k SourceKind) Option { return func(c *config) { c.harvester.Source = k } }

// WithoutFeedback disables the chaotic feedback loop in the workers.
func WithoutFeedback() Option { return func(c *config) { c.harvester.DisableFeedback = true } }

// WithMinEntropy sets the min-entropy, in bits per raw sample, that the
// conditioner assumes. It determines how many samples go into every seed and
// every TRNG block, and the startup estimate must reach it or New fails.
// The default is 0.5; the accepted range is [1/1024, 56]. This is an assumption,
// not a measured lower bound. The startup estimate does not account for all
// dependence between samples and cannot validate a security claim.
func WithMinEntropy(bitsPerSample float64) Option {
	return func(c *config) { c.minEntropy = bitsPerSample }
}

// WithReseedInterval sets how many bytes ModeDRBG outputs between reseeds.
// The default is 1 MiB.
func WithReseedInterval(bytes int) Option { return func(c *config) { c.reseedInterval = bytes } }

// WithOSEntropyMix folds 32 bytes from crypto/rand into every ModeDRBG seed.
// It does not affect ModeTRNG or ModeRaw. It does not turn this experimental
// construction into an audited cryptographic generator.
func WithOSEntropyMix() Option { return func(c *config) { c.mixOS = true } }

// WithPersistentWorkers keeps the racing goroutines running from New until
// Close instead of starting them for each harvest. Use it for ModeRaw and
// ModeTRNG readers that are read continuously. Workers busy-loop until Close
// or a source failure; they are not pinned to specific cores.
func WithPersistentWorkers() Option { return func(c *config) { c.persistent = true } }

// WithStartupSamples sets how many raw samples the startup estimate uses.
// The default is 4096 and the minimum is 1024.
func WithStartupSamples(n int) Option { return func(c *config) { c.startupSamples = n } }

// WithHealthAlpha sets the per-test false-positive probability of the
// continuous health tests. The default is 2^-30.
func WithHealthAlpha(alpha float64) Option { return func(c *config) { c.alpha = alpha } }

// WithoutHealthTests disables the continuous health tests and the startup
// rejection. The startup estimate is still computed and reported in Stats.
// Research only.
func WithoutHealthTests() Option { return func(c *config) { c.skipHealth = true } }

// Stats is a snapshot of a Reader's counters.
type Stats struct {
	Mode              Mode
	Source            SourceKind
	Workers           int
	MinEntropy        float64 // assumed bits per raw sample
	StartupMinEntropy float64 // estimated bits per raw sample at the last start
	SamplesPerSeed    int
	SamplesPerBlock   int
	RCTCutoff         int
	APTWindow         int
	APTCutoff         int
	Samples           uint64 // raw samples harvested
	Seeds             uint64 // DRBG seeds derived
	Bytes             uint64 // bytes returned to callers
	HealthFailures    uint64
}

// Reader produces random bytes from goroutine races. It implements io.Reader
// and math/rand/v2.Source and is safe for concurrent use.
type Reader struct {
	mu     sync.Mutex
	cfg    config
	h      *Harvester
	health *health
	drbg   drbg

	chain [32]byte
	buf   []byte

	seedSamples  int
	blockSamples int
	block        [64]byte
	blockN       int
	since        int

	err    error
	closed bool
	stats  Stats
}

// New creates a Reader, runs the startup tests and, in ModeDRBG, derives the
// first seed. It returns ErrNotEnoughProcs when GOMAXPROCS is 1 and
// ErrInsufficientEntropy when the startup estimate is below the configured
// minimum.
func New(opts ...Option) (*Reader, error) {
	cfg := defaultConfig()
	for _, o := range opts {
		if o == nil {
			return nil, errors.New("racerand: nil option")
		}
		o(&cfg)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if runtime.GOMAXPROCS(0) < 2 {
		return nil, ErrNotEnoughProcs
	}
	r := &Reader{cfg: cfg, h: NewHarvester(cfg.harvester)}
	r.seedSamples = samplesFor(256, cfg.minEntropy)
	r.blockSamples = samplesFor(512, cfg.minEntropy)
	r.buf = make([]byte, 8*max(r.seedSamples, r.blockSamples, cfg.startupSamples))
	if !cfg.skipHealth {
		r.health = newHealth(cfg.minEntropy, cfg.alpha, aptWindow)
	}
	if cfg.persistent {
		r.h.Start()
	}
	if err := r.start(); err != nil {
		r.h.Close()
		return nil, err
	}
	return r, nil
}

func samplesFor(bits int, minEntropy float64) int {
	return int(math.Ceil(float64(bits) * safetyFactor / minEntropy))
}

// start runs the startup tests and the initial seeding. The caller holds mu
// or owns the Reader exclusively.
func (r *Reader) start() error {
	return r.withWorkers(func() error {
		n := r.cfg.startupSamples
		samples := make([]uint64, n)
		defer clear(samples)
		r.h.Fill(samples)
		r.stats.Samples += uint64(n)
		est := EstimateMinEntropy(samples)
		r.stats.StartupMinEntropy = est
		if !r.cfg.skipHealth && est < r.cfg.minEntropy {
			return fmt.Errorf("%w: estimated %.3f bits/sample, configured %.3f",
				ErrInsufficientEntropy, est, r.cfg.minEntropy)
		}
		if r.health != nil {
			for _, s := range samples {
				if err := r.health.check(s); err != nil {
					r.stats.HealthFailures++
					return err
				}
			}
		}
		if r.cfg.mode == ModeDRBG {
			return r.reseedLocked()
		}
		return nil
	})
}

func (r *Reader) withWorkers(f func() error) error {
	if runtime.GOMAXPROCS(0) < 2 {
		if r.cfg.persistent {
			r.h.Stop()
		}
		return ErrNotEnoughProcs
	}
	if !r.cfg.persistent {
		r.h.Start()
		defer r.h.Stop()
	}
	err := f()
	if err != nil && r.cfg.persistent {
		r.h.Stop()
	}
	return err
}

// harvest fills the first n*8 bytes of r.buf with health-checked samples.
func (r *Reader) harvest(n int) error {
	buf := r.buf[:8*n]
	for i := 0; i < n; i++ {
		s := r.h.Sample()
		r.stats.Samples++
		if r.health != nil {
			if err := r.health.check(s); err != nil {
				r.stats.HealthFailures++
				clear(buf)
				return err
			}
		}
		binary.LittleEndian.PutUint64(buf[8*i:], s)
	}
	return nil
}

// reseedLocked derives a new DRBG key. Workers must be running.
func (r *Reader) reseedLocked() error {
	if err := r.harvest(r.seedSamples); err != nil {
		return err
	}
	raw := r.buf[:8*r.seedSamples]
	defer clear(raw)

	h := sha512.New()
	h.Write([]byte(seedDomain))
	h.Write(r.chain[:])
	h.Write(raw)
	if r.cfg.mixOS {
		var os [32]byte
		if _, err := rand.Read(os[:]); err != nil {
			return fmt.Errorf("racerand: crypto/rand: %w", err)
		}
		h.Write(os[:])
		clear(os[:])
	}
	var d [64]byte
	h.Sum(d[:0])
	var key [32]byte
	copy(key[:], d[:32])
	copy(r.chain[:], d[32:])
	clear(d[:])

	if r.drbg == nil {
		r.drbg = newDRBG(r.cfg.drbg, &key)
	} else {
		r.drbg.reseed(&key)
	}
	clear(key[:])
	r.since = 0
	r.stats.Seeds++
	return nil
}

func (r *Reader) reseed() error {
	return r.withWorkers(r.reseedLocked)
}

// Read fills p with random bytes. It always fills all of p or returns an
// error, in which case p has been zeroed. Source errors remain until Reset.
func (r *Reader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.read(p); err != nil {
		clear(p)
		return 0, err
	}
	return len(p), nil
}

func (r *Reader) read(p []byte) error {
	if r.closed {
		return ErrClosed
	}
	if r.h == nil {
		return ErrUninitialized
	}
	if r.err != nil {
		return r.err
	}
	if len(p) == 0 {
		return nil
	}
	var err error
	switch r.cfg.mode {
	case ModeDRBG:
		err = r.readDRBG(p)
	case ModeTRNG:
		err = r.withWorkers(func() error { return r.readTRNG(p) })
	case ModeRaw:
		err = r.withWorkers(func() error { return r.readRaw(p) })
	}
	if err != nil {
		r.err = err
		return err
	}
	r.stats.Bytes += uint64(len(p))
	return nil
}

func (r *Reader) readDRBG(p []byte) error {
	for len(p) > 0 {
		if r.since >= r.cfg.reseedInterval {
			if err := r.reseed(); err != nil {
				return err
			}
		}
		n := min(len(p), r.cfg.reseedInterval-r.since)
		r.drbg.read(p[:n])
		r.since += n
		p = p[n:]
	}
	return nil
}

func (r *Reader) readTRNG(p []byte) error {
	for len(p) > 0 {
		if r.blockN == 0 {
			if err := r.fillBlock(); err != nil {
				return err
			}
		}
		start := len(r.block) - r.blockN
		n := copy(p, r.block[start:])
		clear(r.block[start : start+n])
		r.blockN -= n
		p = p[n:]
	}
	return nil
}

func (r *Reader) fillBlock() error {
	if err := r.harvest(r.blockSamples); err != nil {
		return err
	}
	raw := r.buf[:8*r.blockSamples]
	h := sha512.New()
	h.Write([]byte(trngDomain))
	h.Write(raw)
	h.Sum(r.block[:0])
	clear(raw)
	r.blockN = len(r.block)
	return nil
}

func (r *Reader) readRaw(p []byte) error {
	for i := range p {
		s := r.h.Sample()
		r.stats.Samples++
		if r.health != nil {
			if err := r.health.check(s); err != nil {
				r.stats.HealthFailures++
				return err
			}
		}
		p[i] = Fold(s)
	}
	return nil
}

// Uint64 returns a uniformly distributed 64-bit value so that a Reader can be
// used as a math/rand/v2.Source. Because that interface cannot report errors,
// Uint64 panics if the Reader is closed or has failed a health test, in the
// same way crypto/rand treats an unreadable operating system generator as
// fatal. In ModeRaw the value is eight folded raw bytes and is not uniform.
func (r *Reader) Uint64() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.h != nil && r.cfg.mode == ModeDRBG && r.err == nil && !r.closed && r.cfg.reseedInterval >= 8 {
		if r.since > r.cfg.reseedInterval-8 {
			if err := r.reseed(); err != nil {
				r.err = err
				panic(err)
			}
		}
		r.since += 8
		r.stats.Bytes += 8
		return r.drbg.uint64()
	}
	var b [8]byte
	if err := r.read(b[:]); err != nil {
		panic(err)
	}
	return binary.LittleEndian.Uint64(b[:])
}

// Reseed forces a reseed in ModeDRBG. It is a no-op in other modes.
func (r *Reader) Reseed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.h == nil {
		return ErrUninitialized
	}
	if r.err != nil {
		return r.err
	}
	if r.cfg.mode != ModeDRBG {
		return nil
	}
	if err := r.reseed(); err != nil {
		r.err = err
		return err
	}
	return nil
}

// Reset clears a failed state by rerunning the startup tests and, in
// ModeDRBG, reseeding.
func (r *Reader) Reset() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrClosed
	}
	if r.h == nil {
		return ErrUninitialized
	}
	r.err = nil
	r.blockN = 0
	clear(r.block[:])
	if r.health != nil {
		r.health.reset()
	}
	if r.cfg.persistent {
		r.h.Start()
	}
	if err := r.start(); err != nil {
		r.err = err
		return err
	}
	return nil
}

// Err returns the sticky error, if any.
func (r *Reader) Err() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.err
}

// Close stops workers, clears owned buffers and releases the generator.
// Go and the standard library do not guarantee erasure of all key copies.
// Close is idempotent.
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.h != nil {
		r.h.Close()
	}
	r.drbg = nil
	clear(r.chain[:])
	clear(r.block[:])
	clear(r.buf)
	r.blockN = 0
	return nil
}

// Stats returns a snapshot of the Reader's counters.
func (r *Reader) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.stats
	s.Mode = r.cfg.mode
	s.Source = r.cfg.harvester.Source
	if r.h != nil {
		s.Workers = r.h.Config().Workers
	}
	s.MinEntropy = r.cfg.minEntropy
	s.SamplesPerSeed = r.seedSamples
	s.SamplesPerBlock = r.blockSamples
	if r.health != nil {
		s.RCTCutoff = r.health.rctCutoff
		s.APTWindow = r.health.aptWindow
		s.APTCutoff = r.health.aptCutoff
	}
	return s
}

var (
	defaultOnce   sync.Once
	defaultReader *Reader
	defaultErr    error
)

// Default returns a process-wide Reader with default options, created on
// first use. The error is sticky.
func Default() (*Reader, error) {
	defaultOnce.Do(func() {
		defaultReader, defaultErr = New()
	})
	return defaultReader, defaultErr
}

// Read fills p from the Default reader.
func Read(p []byte) (int, error) {
	r, err := Default()
	if err != nil {
		clear(p)
		return 0, err
	}
	return r.Read(p)
}
