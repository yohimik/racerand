package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// NoiseRow holds example output from one generator rendered as images.
type NoiseRow struct {
	Name   string
	Family string
	// Hex is the first 32 bytes of output.
	Hex string
	// Gray, Bits and Lag are PNG paths relative to the report: 64 KiB as
	// 256x256 grayscale pixels, 8 KiB as 256x256 black-and-white pixels, and
	// a log-scaled 256x256 density map of consecutive byte pairs.
	Gray, Bits, Lag string
	Err             string
}

const noiseSide = 256

func slugify(name string) string {
	var b strings.Builder
	last := '-'
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			last = r
		default:
			if last != '-' {
				b.WriteRune('-')
				last = '-'
			}
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func measureNoise(gens []generator, dir string, n int) ([]NoiseRow, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	n = max(n, noiseSide*noiseSide)
	var rows []NoiseRow
	for _, g := range gens {
		row := NoiseRow{Name: g.name, Family: g.family}
		r, closer, err := g.open()
		if err != nil {
			row.Err = err.Error()
			rows = append(rows, row)
			continue
		}
		data := make([]byte, n)
		_, err = io.ReadFull(r, data)
		closer()
		if err != nil {
			row.Err = err.Error()
			rows = append(rows, row)
			continue
		}
		row.Hex = hex.EncodeToString(data[:32])
		slug := slugify(g.name)
		row.Gray = filepath.ToSlash(filepath.Join(dir, slug+"-bytes.png"))
		row.Bits = filepath.ToSlash(filepath.Join(dir, slug+"-bits.png"))
		row.Lag = filepath.ToSlash(filepath.Join(dir, slug+"-lag.png"))
		for _, e := range []error{
			writePNG(row.Gray, grayImage(data)),
			writePNG(row.Bits, bitImage(data)),
			writePNG(row.Lag, lagImage(data)),
		} {
			if e != nil {
				return nil, e
			}
		}
		rows = append(rows, row)
		fmt.Fprintf(os.Stderr, "  noise %-42s %s\n", g.name, row.Hex[:16])
	}
	return rows, nil
}

// grayImage renders one byte per pixel.
func grayImage(data []byte) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, noiseSide, noiseSide))
	copy(img.Pix, data[:noiseSide*noiseSide])
	return img
}

// bitImage renders one bit per pixel, most significant bit first.
func bitImage(data []byte) *image.Gray {
	img := image.NewGray(image.Rect(0, 0, noiseSide, noiseSide))
	for i := range img.Pix {
		if data[i/8]>>(7-uint(i%8))&1 == 1 {
			img.Pix[i] = 255
		}
	}
	return img
}

// lagImage renders the density of consecutive byte pairs (x = byte i,
// y = byte i+1) on a log scale. Uniform, independent bytes give even noise;
// serial structure shows up as lines, bands and hot spots.
func lagImage(data []byte) *image.Gray {
	counts := new([noiseSide][noiseSide]int)
	maxc := 0
	for i := 1; i < len(data); i++ {
		c := &counts[data[i]][data[i-1]]
		*c++
		maxc = max(maxc, *c)
	}
	img := image.NewGray(image.Rect(0, 0, noiseSide, noiseSide))
	if maxc == 0 {
		return img
	}
	scale := 255 / math.Log1p(float64(maxc))
	for y := 0; y < noiseSide; y++ {
		for x := 0; x < noiseSide; x++ {
			v := math.Log1p(float64(counts[y][x])) * scale
			// y grows downward in the image; flip so byte value 0 is at the bottom.
			img.SetGray(x, noiseSide-1-y, color.Gray{Y: uint8(v)})
		}
	}
	return img
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	return errors.Join(enc.Encode(f, img), f.Close())
}
