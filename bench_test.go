package racerand

import (
	crand "crypto/rand"
	"errors"
	"fmt"
	"io"
	mrand1 "math/rand"
	mrand "math/rand/v2"
	"sync"
	"testing"
)

var sizes = []int{32, 4096, 1 << 16}

func benchRead(b *testing.B, newReader func(b *testing.B) (interface{ Read([]byte) (int, error) }, func())) {
	for _, size := range sizes {
		b.Run(fmt.Sprintf("%dB", size), func(b *testing.B) {
			r, closer := newReader(b)
			defer closer()
			buf := make([]byte, size)
			b.SetBytes(int64(size))
			b.ResetTimer()
			for b.Loop() {
				if _, err := r.Read(buf); err != nil {
					benchError(b, err)
				}
			}
		})
	}
}

func racerandReader(b *testing.B, opts ...Option) func(b *testing.B) (interface{ Read([]byte) (int, error) }, func()) {
	return func(b *testing.B) (interface{ Read([]byte) (int, error) }, func()) {
		r, err := New(opts...)
		if err != nil {
			benchError(b, err)
		}
		return r, func() { r.Close() }
	}
}

func BenchmarkRead(b *testing.B) {
	b.Run("racerand/drbg-chacha8", func(b *testing.B) { benchRead(b, racerandReader(b)) })
	b.Run("racerand/drbg-aes-ctr", func(b *testing.B) { benchRead(b, racerandReader(b, WithDRBG(DRBGAESCTR))) })
	b.Run("racerand/drbg-chacha8-osmix", func(b *testing.B) { benchRead(b, racerandReader(b, WithOSEntropyMix())) })
	b.Run("racerand/trng", func(b *testing.B) {
		benchRead(b, racerandReader(b, WithMode(ModeTRNG), WithPersistentWorkers()))
	})
	b.Run("racerand/raw", func(b *testing.B) {
		benchRead(b, racerandReader(b, WithMode(ModeRaw), WithPersistentWorkers()))
	})
	if !raceEnabled {
		b.Run("racerand/raw-unsync", func(b *testing.B) {
			benchRead(b, racerandReader(b, WithMode(ModeRaw), WithPersistentWorkers(), WithSource(SourceUnsynchronized)))
		})
	}
	b.Run("crypto-rand", func(b *testing.B) {
		benchRead(b, func(b *testing.B) (interface{ Read([]byte) (int, error) }, func()) { return crand.Reader, func() {} })
	})
	b.Run("math-rand-v2-chacha8", func(b *testing.B) {
		benchRead(b, func(b *testing.B) (interface{ Read([]byte) (int, error) }, func()) {
			return mrand.NewChaCha8([32]byte{1}), func() {}
		})
	})
	b.Run("math-rand-v1", func(b *testing.B) {
		benchRead(b, func(b *testing.B) (interface{ Read([]byte) (int, error) }, func()) {
			return mrand1.New(mrand1.NewSource(1)), func() {}
		})
	})
}

func BenchmarkUint64(b *testing.B) {
	b.Run("racerand/drbg-chacha8", func(b *testing.B) {
		defer benchPanic(b)
		r, err := New()
		if err != nil {
			benchError(b, err)
		}
		defer r.Close()
		b.ResetTimer()
		for b.Loop() {
			r.Uint64()
		}
	})
	b.Run("racerand/drbg-aes-ctr", func(b *testing.B) {
		defer benchPanic(b)
		r, err := New(WithDRBG(DRBGAESCTR))
		if err != nil {
			benchError(b, err)
		}
		defer r.Close()
		b.ResetTimer()
		for b.Loop() {
			r.Uint64()
		}
	})
	b.Run("math-rand-v2-pcg", func(b *testing.B) {
		r := mrand.New(mrand.NewPCG(1, 2))
		for b.Loop() {
			r.Uint64()
		}
	})
	b.Run("math-rand-v2-chacha8", func(b *testing.B) {
		r := mrand.NewChaCha8([32]byte{1})
		for b.Loop() {
			r.Uint64()
		}
	})
	b.Run("math-rand-v2-global", func(b *testing.B) {
		for b.Loop() {
			mrand.Uint64()
		}
	})
	b.Run("math-rand-v1", func(b *testing.B) {
		r := mrand1.New(mrand1.NewSource(1))
		for b.Loop() {
			r.Uint64()
		}
	})
	b.Run("crypto-rand", func(b *testing.B) {
		var buf [8]byte
		for b.Loop() {
			crand.Read(buf[:])
		}
	})
}

func BenchmarkHarvesterSample(b *testing.B) {
	for _, src := range []SourceKind{SourceAtomic, SourceUnsynchronized} {
		if src == SourceUnsynchronized && raceEnabled {
			continue
		}
		for _, w := range []int{1, 2, 4, 0} {
			name := fmt.Sprintf("%v/workers=%d", src, w)
			if w == 0 {
				name = fmt.Sprintf("%v/workers=default", src)
			}
			b.Run(name, func(b *testing.B) {
				h := NewHarvester(HarvesterConfig{Workers: w, Source: src})
				h.Start()
				defer h.Close()
				b.ResetTimer()
				for b.Loop() {
					h.Sample()
				}
			})
		}
	}
}

func BenchmarkReseed(b *testing.B) {
	for _, h := range []float64{0.5, 2, 4} {
		b.Run(fmt.Sprintf("min-entropy=%v", h), func(b *testing.B) {
			r, err := New(WithMinEntropy(h))
			if err != nil {
				benchError(b, err)
			}
			defer r.Close()
			b.ResetTimer()
			for b.Loop() {
				if err := r.Reseed(); err != nil {
					benchError(b, err)
				}
			}
		})
	}
}

func BenchmarkNew(b *testing.B) {
	for b.Loop() {
		r, err := New()
		if err != nil {
			benchError(b, err)
		}
		r.Close()
	}
}
func benchError(b *testing.B, err error) {
	b.Helper()
	if errors.Is(err, ErrHealthTestFailed) || errors.Is(err, ErrInsufficientEntropy) || errors.Is(err, ErrNotEnoughProcs) {
		b.Skipf("source rejected this configuration/run: %v", err)
	}
	b.Fatal(err)
}

func benchPanic(b *testing.B) {
	if p := recover(); p != nil {
		if err, ok := p.(error); ok {
			benchError(b, err)
		}
		panic(p)
	}
}

func BenchmarkParallelRead(b *testing.B) {
	for _, size := range []int{32, 4096} {
		for _, useOS := range []bool{false, true} {
			b.Run(fmt.Sprintf("os=%v/%dB", useOS, size), func(b *testing.B) {
				var src io.Reader = crand.Reader
				if !useOS {
					r, err := New()
					if err != nil {
						benchError(b, err)
					}
					defer r.Close()
					src = r
				}
				b.SetBytes(int64(size))
				var firstErr error
				var errMu sync.Mutex
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					buf := make([]byte, size)
					failed := false
					for pb.Next() {
						if failed {
							continue
						}
						if _, err := io.ReadFull(src, buf); err != nil {
							errMu.Lock()
							if firstErr == nil {
								firstErr = err
							}
							errMu.Unlock()
							failed = true
						}
					}
				})
				if firstErr != nil {
					benchError(b, firstErr)
				}
			})
		}
	}
}
