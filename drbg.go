package racerand

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"io"
	mrand "math/rand/v2"
)

var (
	_ io.Reader    = (*Reader)(nil)
	_ mrand.Source = (*Reader)(nil)
)

// DRBGKind selects the deterministic generator used in ModeDRBG.
type DRBGKind uint8

const (
	// DRBGChaCha8 uses math/rand/v2.ChaCha8. This wrapper does not establish
	// cryptographic security or SP 800-90A conformance.
	DRBGChaCha8 DRBGKind = iota

	// DRBGAESCTR is AES-256 in counter mode with a fresh key at every reseed.
	// It uses the hardware AES instructions where available.
	DRBGAESCTR
)

func (k DRBGKind) String() string {
	switch k {
	case DRBGChaCha8:
		return "chacha8"
	case DRBGAESCTR:
		return "aes-256-ctr"
	default:
		return fmt.Sprintf("DRBGKind(%d)", uint8(k))
	}
}

type drbg interface {
	read(p []byte)
	uint64() uint64
	reseed(key *[32]byte)
}

func newDRBG(kind DRBGKind, key *[32]byte) drbg {
	switch kind {
	case DRBGAESCTR:
		d := &aesCTRDRBG{}
		d.reseed(key)
		return d
	default:
		return &chacha8DRBG{c: mrand.NewChaCha8(*key)}
	}
}

type chacha8DRBG struct{ c *mrand.ChaCha8 }

func (d *chacha8DRBG) read(p []byte)        { d.c.Read(p) }
func (d *chacha8DRBG) uint64() uint64       { return d.c.Uint64() }
func (d *chacha8DRBG) reseed(key *[32]byte) { d.c.Seed(*key) }

type aesCTRDRBG struct {
	stream cipher.Stream
	buf    [8]byte
}

func (d *aesCTRDRBG) reseed(key *[32]byte) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		// A 32-byte key is always valid for AES-256.
		panic("racerand: aes.NewCipher: " + err.Error())
	}
	var iv [aes.BlockSize]byte
	d.stream = cipher.NewCTR(block, iv[:])
}

func (d *aesCTRDRBG) read(p []byte) {
	clear(p)
	d.stream.XORKeyStream(p, p)
}

func (d *aesCTRDRBG) uint64() uint64 {
	clear(d.buf[:])
	d.stream.XORKeyStream(d.buf[:], d.buf[:])
	return binary.LittleEndian.Uint64(d.buf[:])
}
