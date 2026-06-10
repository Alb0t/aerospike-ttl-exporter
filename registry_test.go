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
