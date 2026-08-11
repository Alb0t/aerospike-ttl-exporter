package main

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// registry.go owns the lifecycle of the per-set Prometheus collectors in
// auto-discovery mode. Discovery recomputes the effective set list each cycle
// and calls reconcile; the registry registers collectors for new/changed sets,
// unregisters collectors for sets that changed shape or vanished, and hands out
// the current collector pointers to the scanner via get. All map access is
// guarded so discovery (writer) and updateStats (reader) can run concurrently.

// effectiveSet is the fully-resolved configuration for one ns:set after merging
// discovery defaults, the observed TTL fit, and any per-set monitor override.
type effectiveSet struct {
	namespace string
	set       string
	buckets   []float64 // TTL histogram bucket boundaries, in display unit
	ttlUnit   string    // "days"/"hours"/"seconds" label value
	modifier  int       // seconds-per-unit divisor applied before observing
	expirable bool      // false => skip TTL histograms (counts/bytes)
	cfg       monconf   // scan/perf/feature settings (merged)
}

// key returns the "ns:set" registry key for this set.
func (e effectiveSet) key() string {
	return nsSetKey(e.namespace, e.set)
}

// nsSetKey builds the "ns:set" key used to index per-set collectors and the
// legacy lookup maps. Single definition so every call site agrees on the format.
func nsSetKey(ns, set string) string {
	return ns + ":" + set
}

// signature is a stable fingerprint of everything that affects collector shape.
// Two effectiveSets with equal signatures produce identical collectors, so the
// registry can skip unregister/re-register churn when the signature is unchanged.
func (e effectiveSet) signature() string {
	return fmt.Sprintf("exp=%t|unit=%s|buckets=%v|kb=%t|size=%t|sb=%+v",
		e.expirable, e.ttlUnit, e.buckets,
		e.cfg.KByteHistogramEnabled,
		e.cfg.SizeHistogramEnabled, e.cfg.SizeBuckets)
}

// histSet holds the live collectors for one ns:set plus the signature they were
// built from. Any of counts/bytes/sizes may be nil if that histogram is disabled
// for the set (e.g. counts/bytes are nil for non-expirable sets).
type histSet struct {
	namespace string // retained so prune can delete this set's gauge series
	set       string
	counts    *prometheus.HistogramVec
	bytes     *kibCollector
	sizes     *prometheus.HistogramVec
	quantiles *quantileCollector
	modifier  int
	sig       string
}

type histRegistry struct {
	mu  sync.RWMutex
	reg prometheus.Registerer
	m   map[string]*histSet
}

func newHistRegistry(reg prometheus.Registerer) *histRegistry {
	return &histRegistry{reg: reg, m: make(map[string]*histSet)}
}

// get returns the current collectors for a ns:set key under a read lock.
func (r *histRegistry) get(key string) (*histSet, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hs, ok := r.m[key]
	return hs, ok
}

// reconcile makes the registry reflect exactly the supplied set list: it builds
// and registers collectors for new or shape-changed sets, and unregisters and
// drops sets whose signature changed or that are no longer present.
func (r *histRegistry) reconcile(sets []effectiveSet) {
	r.mu.Lock()
	defer r.mu.Unlock()

	wanted := make(map[string]bool, len(sets))
	for _, e := range sets {
		key := e.key()
		wanted[key] = true
		sig := e.signature()
		cur, ok := r.m[key]
		if ok && cur.sig == sig {
			continue // unchanged shape: keep existing collectors, no churn
		}
		if ok {
			r.unregister(cur)
		}
		r.m[key] = buildHistSet(r.reg, e, sig)
	}

	r.prune(wanted)
}

// prune unregisters collectors and deletes the gauge series for sets no longer
// present, plus the per-namespace default-ttl gauge once a namespace has no
// surviving sets. Without this, vanished sets leave stale values exported.
func (r *histRegistry) prune(wanted map[string]bool) {
	prunedNs := make(map[string]bool)
	for key, cur := range r.m {
		if !wanted[key] {
			r.unregister(cur)
			dropSetGauges(cur.namespace, cur.set)
			prunedNs[cur.namespace] = true
			delete(r.m, key)
		}
	}
	for ns := range prunedNs {
		if !r.namespaceLive(ns) {
			defaultTTLGauge.DeleteLabelValues(ns)
		}
	}
}

// namespaceLive reports whether any surviving set still belongs to ns.
func (r *histRegistry) namespaceLive(ns string) bool {
	for _, cur := range r.m {
		if cur.namespace == ns {
			return true
		}
	}
	return false
}

// dropSetGauges deletes the per-set gauge series for a vanished ns:set so stale
// values stop being exported. The per-namespace defaultTTLGauge is handled by
// prune once the namespace has no surviving sets.
func dropSetGauges(ns, set string) {
	minTTLGauge.DeleteLabelValues(ns, set)
	maxTTLGauge.DeleteLabelValues(ns, set)
	scanTimes.DeleteLabelValues(ns, set)
	scanLastUpdated.DeleteLabelValues(ns, set)
	quantileRefreshTS.DeleteLabelValues(ns, set)
}

// newCountsHist builds the counts_hist (records-per-ttl-bucket) collector. It is
// the single definition of this metric, shared by the discovery registry and the
// legacy setup path so their Name/Help/labels can never drift apart.
func newCountsHist(namespace, set, ttlUnit string, buckets []float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace:   "aerospike_ttl",
		Name:        "counts_hist",
		Help:        "Histogram of how many records fall into each ttl bucket.",
		Buckets:     buckets,
		ConstLabels: prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttlUnit},
	}, []string{})
}

// newSizesHist builds the size_bytes_hist (record-size distribution) collector.
// Single shared definition, see newCountsHist.
func newSizesHist(namespace, set string, buckets []float64) *prometheus.HistogramVec {
	return prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace:   "aerospike_ttl",
		Name:        "size_bytes_hist",
		Help:        "Histogram of device/memory/record sizes of records observed by Aerospike TTL Exporter.",
		Buckets:     buckets,
		ConstLabels: prometheus.Labels{"namespace": namespace, "set": set},
	}, []string{"metadata_op"})
}

// buildHistSet constructs and registers the collectors selected by an
// effectiveSet against reg. Shared by the discovery registry and the legacy
// startup path so collector selection and registration gating cannot drift
// between the two modes.
func buildHistSet(reg prometheus.Registerer, e effectiveSet, sig string) *histSet {
	hs := &histSet{namespace: e.namespace, set: e.set, modifier: e.modifier, sig: sig}

	if e.expirable {
		hs.counts = newCountsHist(e.namespace, e.set, e.ttlUnit, e.buckets)
		reg.MustRegister(hs.counts)

		if e.cfg.KByteHistogramEnabled {
			hs.bytes = newKibCollector(e.namespace, e.set, e.ttlUnit, e.buckets)
			reg.MustRegister(hs.bytes)
		}
	}

	if e.cfg.SizeHistogramEnabled {
		hs.sizes = newSizesHist(e.namespace, e.set, sizeBucketsFrom(e.cfg.SizeBuckets))
		reg.MustRegister(hs.sizes)
	}

	hs.quantiles = newQuantileCollector(e.namespace, e.set, e.ttlUnit)
	reg.MustRegister(hs.quantiles)

	return hs
}

// unregister removes any live collectors of a histSet from the registerer.
// Each field is checked as its concrete pointer type: a nil *HistogramVec boxed
// in a Collector interface is itself non-nil and would panic in Unregister.
func (r *histRegistry) unregister(hs *histSet) {
	if hs.counts != nil {
		r.reg.Unregister(hs.counts)
	}
	if hs.bytes != nil {
		r.reg.Unregister(hs.bytes)
	}
	if hs.sizes != nil {
		r.reg.Unregister(hs.sizes)
	}
	if hs.quantiles != nil {
		r.reg.Unregister(hs.quantiles)
	}
}
