package main

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

// countingRegisterer wraps a real prometheus.Registerer and tallies how many
// times each individual collector pointer is registered vs unregistered. It lets
// tests assert "register once, never unregister" (no churn) or "unregister then
// re-register" (rebuild) without inspecting exported series. Delegating to a real
// registry preserves duplicate-detection and pointer semantics.
type countingRegisterer struct {
	inner       prometheus.Registerer
	registers   map[prometheus.Collector]int
	unregisters map[prometheus.Collector]int
}

func newCountingRegisterer() *countingRegisterer {
	return &countingRegisterer{
		inner:       prometheus.NewRegistry(),
		registers:   make(map[prometheus.Collector]int),
		unregisters: make(map[prometheus.Collector]int),
	}
}

func (c *countingRegisterer) Register(col prometheus.Collector) error {
	c.registers[col]++
	return c.inner.Register(col)
}

func (c *countingRegisterer) MustRegister(cols ...prometheus.Collector) {
	for _, col := range cols {
		c.registers[col]++
	}
	c.inner.MustRegister(cols...)
}

func (c *countingRegisterer) Unregister(col prometheus.Collector) bool {
	c.unregisters[col]++
	return c.inner.Unregister(col)
}

func boolPtr(b bool) *bool { return &b }

func expirableSet(ns, set string, buckets []float64) effectiveSet {
	return effectiveSet{
		namespace: ns,
		set:       set,
		buckets:   buckets,
		ttlUnit:   "seconds",
		modifier:  1,
		expirable: true,
		cfg:       monconf{SizeHistogramEnabled: false},
	}
}

func TestRegistryReconcileAddsSet(t *testing.T) {
	r := newHistRegistry(prometheus.NewRegistry())
	r.reconcile([]effectiveSet{expirableSet("ns1", "foo", []float64{0, 10, 20})})

	hs, ok := r.get("ns1:foo")
	if !ok {
		t.Fatal("expected ns1:foo present after reconcile")
	}
	if hs.counts == nil {
		t.Error("expirable set must have counts histogram")
	}
	if hs.modifier != 1 {
		t.Errorf("modifier = %d, want 1", hs.modifier)
	}
}

func TestRegistryUnchangedSigKeepsPointer(t *testing.T) {
	r := newHistRegistry(prometheus.NewRegistry())
	set := expirableSet("ns1", "foo", []float64{0, 10, 20})
	r.reconcile([]effectiveSet{set})
	first, _ := r.get("ns1:foo")

	r.reconcile([]effectiveSet{set}) // identical -> no churn
	second, _ := r.get("ns1:foo")

	if first != second {
		t.Error("unchanged signature must keep the same *histSet pointer (no re-register churn)")
	}
}

func TestRegistryChangedBucketsRebuilds(t *testing.T) {
	r := newHistRegistry(prometheus.NewRegistry())
	r.reconcile([]effectiveSet{expirableSet("ns1", "foo", []float64{0, 10, 20})})
	first, _ := r.get("ns1:foo")

	r.reconcile([]effectiveSet{expirableSet("ns1", "foo", []float64{0, 5, 10, 15})})
	second, _ := r.get("ns1:foo")

	if first == second {
		t.Error("changed buckets must produce a new *histSet (signature changed)")
	}
}

func TestRegistryVanishedSetDropped(t *testing.T) {
	r := newHistRegistry(prometheus.NewRegistry())
	r.reconcile([]effectiveSet{
		expirableSet("ns1", "foo", []float64{0, 10}),
		expirableSet("ns1", "bar", []float64{0, 10}),
	})
	r.reconcile([]effectiveSet{expirableSet("ns1", "foo", []float64{0, 10})})

	if _, ok := r.get("ns1:bar"); ok {
		t.Error("set absent from latest reconcile must be dropped")
	}
	if _, ok := r.get("ns1:foo"); !ok {
		t.Error("surviving set must remain")
	}
}

func TestRegistryNonExpirableSkipsCounts(t *testing.T) {
	r := newHistRegistry(prometheus.NewRegistry())
	es := effectiveSet{
		namespace: "ns1",
		set:       "neverexpire",
		expirable: false,
		cfg:       monconf{SizeHistogramEnabled: true, SizeBuckets: bucketConfig{Mode: "exponential", Min: "1", Max: "8388608", Count: 5}},
	}
	r.reconcile([]effectiveSet{es})

	hs, ok := r.get("ns1:neverexpire")
	if !ok {
		t.Fatal("expected set present")
	}
	if hs.counts != nil {
		t.Error("non-expirable set must not have counts histogram")
	}
	if hs.sizes == nil {
		t.Error("non-expirable set with SizeHistogramEnabled must still have sizes histogram")
	}
}

// TestRegistryVanishedSetUnregistersCollectors locks in the "drop on vanish"
// hygiene: when a previously-discovered set disappears from the set list, its
// collectors must be Unregister'd (its Prometheus series vanish) while the
// surviving set's collectors stay registered and are never unregistered.
func TestRegistryVanishedSetUnregistersCollectors(t *testing.T) {
	cr := newCountingRegisterer()
	r := newHistRegistry(cr)

	r.reconcile([]effectiveSet{
		expirableSet("ns1", "A", []float64{0, 10}),
		expirableSet("ns1", "B", []float64{0, 10}),
	})
	aHS, ok := r.get("ns1:A")
	if !ok {
		t.Fatal("expected ns1:A present after first reconcile")
	}
	bHS, ok := r.get("ns1:B")
	if !ok {
		t.Fatal("expected ns1:B present after first reconcile")
	}

	// B vanishes; A survives.
	r.reconcile([]effectiveSet{expirableSet("ns1", "A", []float64{0, 10})})

	if _, ok := r.get("ns1:B"); ok {
		t.Error("vanished set ns1:B must be dropped from the registry map")
	}
	if _, ok := r.get("ns1:A"); !ok {
		t.Error("surviving set ns1:A must remain in the registry map")
	}
	if got := cr.unregisters[bHS.counts]; got != 1 {
		t.Errorf("vanished set B counts collector: unregister count = %d, want 1", got)
	}
	if got := cr.unregisters[aHS.counts]; got != 0 {
		t.Errorf("surviving set A counts collector: unregister count = %d, want 0", got)
	}
	if got := cr.registers[aHS.counts]; got != 1 {
		t.Errorf("surviving set A counts collector: register count = %d, want 1", got)
	}
}

// TestRegistryUnchangedSigNoChurn asserts that a set whose signature is unchanged
// across two reconcile calls is registered exactly once and never unregistered
// (no unregister+re-register churn).
func TestRegistryUnchangedSigNoChurn(t *testing.T) {
	cr := newCountingRegisterer()
	r := newHistRegistry(cr)
	set := expirableSet("ns1", "foo", []float64{0, 10, 20})

	r.reconcile([]effectiveSet{set})
	hs, ok := r.get("ns1:foo")
	if !ok {
		t.Fatal("expected ns1:foo present after first reconcile")
	}

	r.reconcile([]effectiveSet{set}) // identical signature -> must not churn

	if got := cr.registers[hs.counts]; got != 1 {
		t.Errorf("unchanged set counts collector: register count = %d, want 1 (no re-register)", got)
	}
	if got := cr.unregisters[hs.counts]; got != 0 {
		t.Errorf("unchanged set counts collector: unregister count = %d, want 0 (no churn)", got)
	}
}

// TestRegistryChangedSigUnregistersThenReRegisters asserts that when a set's
// signature changes, the old collectors are unregistered and new ones registered.
func TestRegistryChangedSigUnregistersThenReRegisters(t *testing.T) {
	cr := newCountingRegisterer()
	r := newHistRegistry(cr)

	r.reconcile([]effectiveSet{expirableSet("ns1", "foo", []float64{0, 10, 20})})
	first, _ := r.get("ns1:foo")

	r.reconcile([]effectiveSet{expirableSet("ns1", "foo", []float64{0, 5, 10, 15})})
	second, _ := r.get("ns1:foo")

	if first.counts == second.counts {
		t.Fatal("changed signature must build a new counts collector")
	}
	if got := cr.unregisters[first.counts]; got != 1 {
		t.Errorf("old counts collector: unregister count = %d, want 1", got)
	}
	if got := cr.registers[second.counts]; got != 1 {
		t.Errorf("new counts collector: register count = %d, want 1", got)
	}
}

func TestBuildHistSetFeatureToggles(t *testing.T) {
	buckets := []float64{0, 10, 20}
	sizeBuckets := bucketConfig{Mode: "exponential", Min: "1", Max: "8389000", Count: 5}

	tests := []struct {
		name          string
		cfg           monconf
		expirable     bool
		wantCounts    bool
		wantBytes     bool
		wantSizes     bool
		wantQuantiles bool
		wantNeedsSize bool
	}{
		{
			name:          "all defaults (expirable)",
			cfg:           monconf{},
			expirable:     true,
			wantCounts:    true,  // ttlCountsHistogramEnabled nil → true
			wantBytes:     false, // ttlBytesHistogramEnabled false
			wantSizes:     false, // sizeHistogramEnabled false
			wantQuantiles: true,  // quantileTargets nil → defaults
			wantNeedsSize: true,  // size+ttlBytes quantiles enabled by default
		},
		{
			name:          "counts disabled",
			cfg:           monconf{TTLCountsHistogramEnabled: boolPtr(false)},
			expirable:     true,
			wantCounts:    false,
			wantQuantiles: true,
			wantNeedsSize: true,
		},
		{
			name:          "counts explicitly enabled",
			cfg:           monconf{TTLCountsHistogramEnabled: boolPtr(true)},
			expirable:     true,
			wantCounts:    true,
			wantQuantiles: true,
			wantNeedsSize: true,
		},
		{
			name:          "all histograms enabled",
			cfg:           monconf{TTLCountsHistogramEnabled: boolPtr(true), TTLBytesHistogramEnabled: true, SizeHistogramEnabled: true, SizeBuckets: sizeBuckets},
			expirable:     true,
			wantCounts:    true,
			wantBytes:     true,
			wantSizes:     true,
			wantQuantiles: true,
			wantNeedsSize: true,
		},
		{
			name:          "all quantiles disabled via blanket",
			cfg:           monconf{QuantileTargets: []float64{}},
			expirable:     true,
			wantCounts:    true,
			wantQuantiles: false,
		},
		{
			name:          "only ttl counts quantiles enabled",
			cfg:           monconf{TTLCountsQuantileTargets: []float64{0.50}, SizeQuantileTargets: []float64{}, TTLBytesQuantileTargets: []float64{}},
			expirable:     true,
			wantCounts:    true,
			wantQuantiles: true,
			wantNeedsSize: false,
		},
		{
			name:          "only size quantiles enabled",
			cfg:           monconf{TTLCountsQuantileTargets: []float64{}, SizeQuantileTargets: []float64{0.50, 0.99}, TTLBytesQuantileTargets: []float64{}},
			expirable:     true,
			wantCounts:    true,
			wantQuantiles: true,
			wantNeedsSize: true,
		},
		{
			name:          "only ttl bytes quantiles enabled",
			cfg:           monconf{TTLCountsQuantileTargets: []float64{}, SizeQuantileTargets: []float64{}, TTLBytesQuantileTargets: []float64{0.90}},
			expirable:     true,
			wantCounts:    true,
			wantQuantiles: true,
			wantNeedsSize: true,
		},
		{
			name:          "per-dimension overrides blanket",
			cfg:           monconf{QuantileTargets: []float64{0.50}, TTLCountsQuantileTargets: []float64{0.20, 0.80}, SizeQuantileTargets: []float64{}},
			expirable:     true,
			wantCounts:    true,
			wantQuantiles: true,
			wantNeedsSize: true, // ttlBytes falls back to blanket [0.50]
		},
		{
			name:          "non-expirable with size quantiles only",
			cfg:           monconf{TTLCountsQuantileTargets: []float64{}, SizeQuantileTargets: []float64{0.50}, TTLBytesQuantileTargets: []float64{}},
			expirable:     false,
			wantCounts:    false,
			wantQuantiles: true,
			wantNeedsSize: true,
		},
		{
			name:          "everything disabled",
			cfg:           monconf{TTLCountsHistogramEnabled: boolPtr(false), QuantileTargets: []float64{}},
			expirable:     true,
			wantCounts:    false,
			wantBytes:     false,
			wantSizes:     false,
			wantQuantiles: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			es := effectiveSet{
				namespace: "ns", set: "s", buckets: buckets,
				ttlUnit: "seconds", modifier: 1,
				expirable: tt.expirable, cfg: tt.cfg,
			}
			hs := buildHistSet(reg, es, "")

			if got := (hs.counts != nil); got != tt.wantCounts {
				t.Errorf("counts: got %v, want %v", got, tt.wantCounts)
			}
			if got := (hs.bytes != nil); got != tt.wantBytes {
				t.Errorf("bytes: got %v, want %v", got, tt.wantBytes)
			}
			if got := (hs.sizes != nil); got != tt.wantSizes {
				t.Errorf("sizes: got %v, want %v", got, tt.wantSizes)
			}
			if got := (hs.quantiles != nil); got != tt.wantQuantiles {
				t.Errorf("quantiles: got %v, want %v", got, tt.wantQuantiles)
			}
			if tt.wantQuantiles && tt.wantNeedsSize {
				if !hs.quantiles.needsSize() {
					t.Error("quantiles.needsSize() = false, want true")
				}
			}
			if tt.wantQuantiles && !tt.wantNeedsSize {
				if hs.quantiles.needsSize() {
					t.Error("quantiles.needsSize() = true, want false")
				}
			}
		})
	}
}

func TestQuantileCollectorPerDimensionObserve(t *testing.T) {
	qc := newQuantileCollector("ns", "s", "seconds", quantileDimTargets{
		ttl:  []float64{0.50},
		size: nil, // disabled
	})

	for i := 1; i <= 10; i++ {
		qc.observeTTL(float64(i))
		qc.observeSize(float64(i * 100)) // should be no-op
	}
	qc.finalize()

	if qc.liveTTL == nil {
		t.Error("TTL quantile should be computed")
	}
	if qc.liveSize != nil {
		t.Error("size quantile should be nil when dimension disabled")
	}
}

func TestResolveQuantileDims(t *testing.T) {
	tests := []struct {
		name        string
		cfg         monconf
		wantTTLLen  int
		wantSizeLen int
		wantBytLen  int
	}{
		{"all defaults", monconf{}, 4, 4, 4},
		{"blanket override", monconf{QuantileTargets: []float64{0.50}}, 1, 1, 1},
		{"blanket empty disables all", monconf{QuantileTargets: []float64{}}, 0, 0, 0},
		{"per-dim overrides blanket", monconf{QuantileTargets: []float64{0.50}, TTLCountsQuantileTargets: []float64{0.20, 0.80}}, 2, 1, 1},
		{"per-dim empty overrides blanket", monconf{QuantileTargets: []float64{0.50}, SizeQuantileTargets: []float64{}}, 1, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dims := resolveQuantileDims(tt.cfg)
			if len(dims.ttl) != tt.wantTTLLen {
				t.Errorf("ttl targets len = %d, want %d", len(dims.ttl), tt.wantTTLLen)
			}
			if len(dims.size) != tt.wantSizeLen {
				t.Errorf("size targets len = %d, want %d", len(dims.size), tt.wantSizeLen)
			}
			if len(dims.ttlSize) != tt.wantBytLen {
				t.Errorf("ttlSize targets len = %d, want %d", len(dims.ttlSize), tt.wantBytLen)
			}
		})
	}
}

func TestCountsHistEnabled(t *testing.T) {
	if !ttlCountsHistEnabled(nil) {
		t.Error("nil should default to true")
	}
	if !ttlCountsHistEnabled(boolPtr(true)) {
		t.Error("explicit true should be true")
	}
	if ttlCountsHistEnabled(boolPtr(false)) {
		t.Error("explicit false should be false")
	}
}
