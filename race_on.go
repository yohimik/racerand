//go:build race

package racerand

// raceEnabled reports whether the binary was built with -race. Tests that
// exercise SourceUnsynchronized skip themselves when it is set.
const raceEnabled = true
