package jitter

import (
	"io"
	"testing"
)

func TestReaders(t *testing.T) {
	for _, r := range []io.Reader{RawReader(), ConditionedReader(64)} {
		buf := make([]byte, 300)
		if _, err := io.ReadFull(r, buf); err != nil {
			t.Fatal(err)
		}
		same := true
		for _, b := range buf[1:] {
			if b != buf[0] {
				same = false
			}
		}
		if same {
			t.Error("all bytes identical")
		}
	}
}
