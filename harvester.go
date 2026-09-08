package racerand

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// cacheLine is the padding used to keep the contended word away from
// everything else. 128 covers both x86-64 (64) and Apple silicon (128).
const cacheLine = 128

// Sample layout: bits 0-47 interleave count, bits 48-55 poll count, bits
// 56-63 worker id.
const (
	deltaBits = 48
	deltaMask = 1<<deltaBits - 1
	pollShift = 48
	idShift   = 56

	// yieldEvery is the number of polls between calls to runtime.Gosched
	// while the shared word has not changed. It bounds how long the sampler
	// can starve a worker that shares its P.
	yieldEvery = 1 << 12

	// maxPolls bounds the spin so that a stalled set of workers produces a
	// stream of identical samples for the health tests to reject instead of
	// a livelock.
	maxPolls = 1 << 20
)

// SourceKind selects how workers and the sampler access the shared word.
type SourceKind uint8

const (
	// SourceAtomic uses atomic operations. Interleaving is still
	// nondeterministic, but every access is well defined under the Go
	// memory model. This is the default source.
	SourceAtomic SourceKind = iota

	// SourceUnsynchronized uses plain loads and stores. Worker increments race
	// with each other and updates are lost nondeterministically, which is the
	// textbook race condition. It is a genuine data race, flagged by -race,
	// and provided for research only.
	SourceUnsynchronized
)

func (k SourceKind) String() string {
	switch k {
	case SourceAtomic:
		return "atomic"
	case SourceUnsynchronized:
		return "unsynchronized"
	default:
		return fmt.Sprintf("SourceKind(%d)", uint8(k))
	}
}

// HarvesterConfig configures a Harvester.
type HarvesterConfig struct {
	// Workers is the number of racing goroutines. Zero selects
	// max(1, GOMAXPROCS-1) so that the consumer keeps a P of its own.
	Workers int

	// Source selects the access discipline. The default is SourceAtomic.
	Source SourceKind

	// DisableFeedback turns off the chaotic feedback loop in the workers.
	// With feedback on (the default) each worker's cadence depends on the
	// shared state it observed, so the timing of the whole system feeds back
	// on itself. Disabling it makes the workers run a fixed tight loop.
	DisableFeedback bool
}

func (c HarvesterConfig) withDefaults() HarvesterConfig {
	if c.Workers <= 0 {
		c.Workers = min(256, max(1, runtime.GOMAXPROCS(0)-1))
	}
	return c
}

// Harvester runs the racing goroutines and lets a single consumer sample the
// interleaving between them. It is the raw noise source; it performs no
// conditioning and no health testing. Reader adds those stages.
//
// Sample and Fill require one consumer and must not overlap Start.
// Lifecycle methods serialize with each other; Stop and Close may overlap
// sampling when SourceAtomic is used. SourceUnsynchronized deliberately
// violates the Go memory model regardless of the caller's synchronization.
type Harvester struct {
	_       [cacheLine]byte
	counter atomic.Uint64
	last    atomic.Uint32
	_       [cacheLine - 12]byte

	ucounter uint64
	ulast    uint32
	_        [cacheLine - 12]byte

	stop  atomic.Bool
	ready atomic.Int32
	_     [cacheLine - 8]byte

	cfg  HarvesterConfig
	prev uint64

	mu      sync.Mutex
	running bool
	closed  bool
	wg      sync.WaitGroup
}

// NewHarvester creates a Harvester. Workers are not started until Start is
// called. It panics for a negative worker count, more than 256 workers, or
// an unknown Source. New returns errors for these configurations instead.
func NewHarvester(cfg HarvesterConfig) *Harvester {
	if cfg.Workers < 0 || cfg.Workers > 256 || cfg.Source > SourceUnsynchronized {
		panic("racerand: invalid harvester configuration")
	}
	return &Harvester{cfg: cfg.withDefaults()}
}

// Config returns the effective configuration.
func (h *Harvester) Config() HarvesterConfig { return h.cfg }

// Start launches the worker goroutines and returns once every worker is
// running. It is a no-op if the workers are already running.
func (h *Harvester) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running || h.closed {
		return
	}
	h.stop.Store(false)
	h.ready.Store(0)
	h.wg.Add(h.cfg.Workers)
	for i := range h.cfg.Workers {
		go h.worker(uint32(i))
	}
	for h.ready.Load() < int32(h.cfg.Workers) {
		runtime.Gosched()
	}
	if h.cfg.Source == SourceUnsynchronized {
		h.prev = loadU64(&h.ucounter)
	} else {
		h.prev = h.counter.Load()
	}
	h.running = true
}

// Stop asks the workers to exit and waits for them. It is a no-op if the
// workers are not running.
func (h *Harvester) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked()
}

func (h *Harvester) stopLocked() {
	if !h.running {
		return
	}
	h.stop.Store(true)
	h.wg.Wait()
	h.running = false
}

// Close stops the workers permanently.
func (h *Harvester) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked()
	h.closed = true
}

// Running reports whether the workers are running.
func (h *Harvester) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.running
}

// Sample polls the shared word until a worker changes it and returns one raw
// sample. Bits 0-47 hold the number of worker operations that landed since
// the previous sample (the interleave count), bits 48-55 hold the low byte
// of the number of polls the sampler needed before it saw a change (a
// timing measurement whose clock is the racing workers themselves), and the
// top byte holds the last observed worker id. The counter and id are separate
// observations, not a consistent snapshot of one worker operation.
//
// If the workers are not running the poll is bounded and Sample eventually
// returns a saturated sample. Sample must not be called concurrently with
// itself or Fill.
func (h *Harvester) Sample() uint64 {
	if h.cfg.Source == SourceUnsynchronized {
		return h.sampleUnsync()
	}
	return h.sampleAtomic()
}

// Fill fills dst with raw samples.
func (h *Harvester) Fill(dst []uint64) {
	if h.cfg.Source == SourceUnsynchronized {
		for i := range dst {
			dst[i] = h.sampleUnsync()
		}
		return
	}
	for i := range dst {
		dst[i] = h.sampleAtomic()
	}
}

func (h *Harvester) sampleAtomic() uint64 {
	prev := h.prev
	var polls uint64
	for {
		v := h.counter.Load()
		if v != prev || polls >= maxPolls {
			w := h.last.Load()
			h.prev = v
			return pack(v-prev, polls, w)
		}
		polls++
		if polls&(yieldEvery-1) == 0 {
			runtime.Gosched()
		}
	}
}

func (h *Harvester) sampleUnsync() uint64 {
	prev := h.prev
	var polls uint64
	for {
		v := loadU64(&h.ucounter)
		if v != prev || polls >= maxPolls {
			w := loadU32(&h.ulast)
			h.prev = v
			return pack(v-prev, polls, w)
		}
		polls++
		if polls&(yieldEvery-1) == 0 {
			runtime.Gosched()
		}
	}
}

func pack(delta, polls uint64, id uint32) uint64 {
	return delta&deltaMask | (polls&0xFF)<<pollShift | uint64(id)<<idShift
}

// Fold compresses a raw sample to a single byte for ModeRaw output by XORing
// all eight bytes of the sample together, so the interleave count, the poll
// count and the worker id all contribute.
func Fold(s uint64) byte {
	s ^= s >> 32
	s ^= s >> 16
	s ^= s >> 8
	return byte(s)
}

func (h *Harvester) worker(id uint32) {
	defer h.wg.Done()
	h.ready.Add(1)
	feedback := !h.cfg.DisableFeedback
	if h.cfg.Source == SourceUnsynchronized {
		h.workerUnsync(id, feedback)
		return
	}
	x := uint64(id+1) * 0x9E3779B97F4A7C15
	for !h.stop.Load() {
		n := h.counter.Add(1)
		h.last.Store(id)
		if feedback {
			x = chaoticSpin(x ^ n)
		}
	}
}

func (h *Harvester) workerUnsync(id uint32, feedback bool) {
	x := uint64(id+1) * 0x9E3779B97F4A7C15
	for !h.stop.Load() {
		n := loadU64(&h.ucounter) + 1
		storeU64(&h.ucounter, n)
		storeU32(&h.ulast, id)
		if feedback {
			x = chaoticSpin(x ^ n)
		}
	}
}

// chaoticSpin performs between 0 and 31 iterations of local arithmetic; the
// count depends on the value observed in the shared word. This makes each
// worker's cadence a function of the global interleaving, so timing feeds
// back on itself.
func chaoticSpin(x uint64) uint64 {
	x *= 0x9E3779B97F4A7C15
	for i := x >> 59; i > 0; i-- {
		x = x*6364136223846793005 + 1442695040888963407
	}
	return x
}

// The unsynchronized accessors are deliberately not inlined so that the
// compiler cannot hoist a load out of a loop or merge stores. They are plain
// memory operations and racing on them is a data race by definition.

//go:noinline
func loadU64(p *uint64) uint64 { return *p }

//go:noinline
func storeU64(p *uint64, v uint64) { *p = v }

//go:noinline
func loadU32(p *uint32) uint32 { return *p }

//go:noinline
func storeU32(p *uint32, v uint32) { *p = v }
