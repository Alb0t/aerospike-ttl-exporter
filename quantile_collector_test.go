package main

import (
	"math"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestExactQuantile(t *testing.T) {
	data := []float64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	tests := []struct {
		q    float64
		want float64
	}{
		{0.50, 5.5},
		{0.90, 9.1},
		{0.99, 9.91},
		{0.0, 1.0},
		{1.0, 10.0},
	}
	for _, tt := range tests {
		got := exactQuantile(data, tt.q)
		if math.Abs(got-tt.want) > 0.01 {
			t.Errorf("exactQuantile(data, %.2f) = %.2f, want %.2f", tt.q, got, tt.want)
		}
	}
}

func TestComputeQuantilesEmpty(t *testing.T) {
	r := computeQuantiles(nil, defaultQuantileTargets)
	if r != nil {
		t.Error("expected nil for empty data")
	}
}

func TestQuantileCollectorDoubleBuffer(t *testing.T) {
	reg := prometheus.NewRegistry()
	qc := newQuantileCollector("testns", "testset", "seconds", quantileDimTargets{ttl: defaultQuantileTargets, size: defaultQuantileTargets})
	reg.MustRegister(qc)

	// before any finalize, should emit nothing
	count := testutil.CollectAndCount(qc)
	if count != 0 {
		t.Errorf("expected 0 metrics before finalize, got %d", count)
	}

	// feed data and finalize
	for i := 1; i <= 100; i++ {
		qc.observeTTL(float64(i))
		qc.observeSize(float64(i * 10))
	}
	qc.finalize()

	count = testutil.CollectAndCount(qc)
	if count != 2 {
		t.Errorf("expected 2 summary metrics after finalize, got %d", count)
	}

	// reset + partial data (no finalize) should still serve old values
	qc.reset()
	qc.observeTTL(999)
	count = testutil.CollectAndCount(qc)
	if count != 2 {
		t.Errorf("expected 2 metrics during partial scan, got %d", count)
	}
}

func TestFinalizeEmptyClearsLive(t *testing.T) {
	qc := newQuantileCollector("ns", "set", "seconds", quantileDimTargets{ttl: defaultQuantileTargets, size: defaultQuantileTargets})

	for i := 1; i <= 50; i++ {
		qc.observeTTL(float64(i))
		qc.observeSize(float64(i * 10))
	}
	qc.finalize()

	count := testutil.CollectAndCount(qc)
	if count != 2 {
		t.Fatalf("expected 2 metrics after first finalize, got %d", count)
	}

	qc.reset()
	qc.finalize()

	count = testutil.CollectAndCount(qc)
	if count != 0 {
		t.Errorf("expected 0 metrics after empty finalize, got %d", count)
	}
}
