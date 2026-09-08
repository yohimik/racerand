package racerand_test

import (
	"fmt"
	"io"
	mrand "math/rand/v2"

	"github.com/yohimik/racerand"
)

func ExampleNew() {
	r, err := racerand.New()
	if err != nil {
		// GOMAXPROCS is 1 or the startup estimate rejected the source.
		panic(err)
	}
	defer r.Close()

	sample := make([]byte, 32)
	if _, err := io.ReadFull(r, sample); err != nil {
		panic(err)
	}
	fmt.Println(len(sample))
}

func ExampleReader_Uint64() {
	r, err := racerand.New()
	if err != nil {
		panic(err)
	}
	defer r.Close()

	rng := mrand.New(r)
	n := rng.IntN(6) + 1
	fmt.Println(n >= 1 && n <= 6)
}

func ExampleWithMode() {
	r, err := racerand.New(racerand.WithMode(racerand.ModeTRNG))
	if err != nil {
		panic(err)
	}
	defer r.Close()

	block := make([]byte, 64)
	_, err = r.Read(block)
	fmt.Println(err)
}
