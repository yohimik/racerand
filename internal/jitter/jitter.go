// Package jitter is a small timing-noise baseline for this experiment.
// It is not an implementation of jitterentropy or HAVEGE and has no entropy
// assessment or health tests. It uses time.Now rather than racing goroutines.
package jitter

import (
	"crypto/sha512"
	"encoding/binary"
	"io"
	"time"
)

const memSize = 1 << 16

// Source measures how long a short, data-dependent memory walk takes.
type Source struct {
	mem []byte
	x   uint64
}

// New creates a Source.
func New() *Source {
	return &Source{mem: make([]byte, memSize), x: 0x9E3779B97F4A7C15}
}

// Sample returns the duration in nanoseconds of one memory walk. The walk's
// addresses depend on the previous timing so the source feeds back on
// itself, as racerand's workers do.
func (s *Source) Sample() uint64 {
	t0 := time.Now()
	x := s.x
	for i := 0; i < 64; i++ {
		idx := (x >> 32) & (memSize - 1)
		x = x*6364136223846793005 + 1442695040888963407 + uint64(s.mem[idx])
		s.mem[idx] ^= byte(x)
	}
	d := uint64(time.Since(t0))
	s.x = x ^ d
	return d
}

// Fold compresses a sample to one byte.
func Fold(s uint64) byte {
	s ^= s >> 32
	s ^= s >> 16
	s ^= s >> 8
	return byte(s)
}

type rawReader struct{ s *Source }

// RawReader returns one folded byte per sample with no conditioning.
func RawReader() io.Reader { return rawReader{New()} }

func (r rawReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = Fold(r.s.Sample())
	}
	return len(p), nil
}

type condReader struct {
	s       *Source
	samples int
	buf     []byte
	block   [64]byte
	n       int
}

// ConditionedReader returns SHA-512 digests of samplesPerBlock raw samples,
// 64 bytes per block, without racerand's health checks or domain prefix.
func ConditionedReader(samplesPerBlock int) io.Reader {
	if samplesPerBlock < 1 || samplesPerBlock > 1<<20 {
		panic("jitter: samples per block must be in [1, 1048576]")
	}
	return &condReader{s: New(), samples: samplesPerBlock, buf: make([]byte, 8*samplesPerBlock)}
}

func (r *condReader) Read(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		if r.n == 0 {
			for i := 0; i < r.samples; i++ {
				binary.LittleEndian.PutUint64(r.buf[8*i:], r.s.Sample())
			}
			r.block = sha512.Sum512(r.buf)
			r.n = len(r.block)
		}
		c := copy(p, r.block[len(r.block)-r.n:])
		r.n -= c
		p = p[c:]
	}
	return total, nil
}
