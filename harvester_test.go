package racerand

import (
	"testing"
)

func TestHarvesterLifecycle(t *testing.T) {
	h := NewHarvester(HarvesterConfig{Workers: 2})
	if h.Config().Workers != 2 {
		t.Fatal("config")
	}
	if h.Running() {
		t.Fatal("running before Start")
	}
	h.Start()
	h.Start() // idempotent
	if !h.Running() {
		t.Fatal("not running after Start")
	}
	s := make([]uint64, 1024)
	h.Fill(s)
	t.Logf("observed MCV diagnostic: %v", EstimateMinEntropy(s))
	h.Stop()
	h.Stop()
	if h.Running() {
		t.Fatal("running after Stop")
	}
	// Sampling with stopped workers terminates thanks to the poll bound and
	// yields a saturated, constant sample once the last change has been
	// consumed.
	h.Sample()
	a, b := h.Sample(), h.Sample()
	if a != b {
		t.Errorf("stopped workers gave varying samples %#x %#x", a, b)
	}
	h.Close()
	h.Start()
	if h.Running() {
		t.Fatal("Start after Close should be a no-op")
	}
}

func TestHarvesterDefaults(t *testing.T) {
	h := NewHarvester(HarvesterConfig{})
	if h.Config().Workers < 1 {
		t.Error("workers")
	}
	if h.Config().Source != SourceAtomic {
		t.Error("source")
	}
}

func TestFold(t *testing.T) {
	if Fold(0) != 0 {
		t.Error("Fold(0)")
	}
	if Fold(0x0102030405060708) != 0x01^0x02^0x03^0x04^0x05^0x06^0x07^0x08 {
		t.Error("Fold mixes all bytes")
	}
	if Fold(1<<idShift) != 1 {
		t.Error("worker id contributes")
	}
}

func TestPack(t *testing.T) {
	s := pack(0x123456789ABC, 0x1FF, 7)
	if s&deltaMask != 0x123456789ABC {
		t.Errorf("delta %#x", s&deltaMask)
	}
	if (s>>pollShift)&0xFF != 0xFF {
		t.Errorf("polls %#x", (s>>pollShift)&0xFF)
	}
	if s>>idShift != 7 {
		t.Errorf("id %d", s>>idShift)
	}
}

func TestEstimateMinEntropy(t *testing.T) {
	if EstimateMinEntropy(nil) != 0 {
		t.Error("empty")
	}
	c := make([]uint64, 4096)
	if EstimateMinEntropy(c) != 0 {
		t.Error("constant")
	}
	u := make([]uint64, 1<<16)
	for i := range u {
		u[i] = uint64(i & 0xFF)
	}
	if h := EstimateMinEntropy(u); h < 7.7 || h > 8 {
		t.Errorf("uniform bytes: %v", h)
	}
}
