package jitter

import (
	"io"
	"testing"
)

func TestReaders(t *testing.T) {
	for _, r := range []io.Reader{RawReader(), ConditionedReader(64)} {
		// Timer resolution can make every raw sample identical. This test
		// checks io.Reader behavior, not the entropy of the host clock.
		for _, size := range []int{0, 1, 63, 64, 65, 300} {
			buf := make([]byte, size)
			if n, err := r.Read(buf); n != size || err != nil {
				t.Fatalf("Read(%d) returned %d, %v", size, n, err)
			}
			if n, err := io.ReadFull(r, buf); n != size || err != nil {
				t.Fatalf("ReadFull(%d) returned %d, %v", size, n, err)
			}
		}
	}
}
