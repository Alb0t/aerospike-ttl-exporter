package main

import (
	"math"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// kibCollector is a custom prometheus.Collector that accumulates a
// size-weighted TTL histogram in O(1) per record. Bucket counts represent
// total KiB of records falling into each TTL bucket, eliminating the
// former O(size/resolution) Observe() loop and the resolution config knob.
//
// Weights are accumulated as fractional KiB (float64) so sub-KiB records sum
// without loss; the prometheus exposition format requires uint64 bucket
// counts, so values are rounded once at Collect time (max <1 KiB error per
// bucket, negligible at real scale).
type kibCollector struct {
	mu      sync.Mutex
	desc    *prometheus.Desc
	bounds  []float64 // sorted bucket upper bounds (mirrors the TTL histogram)
	weights []float64 // non-cumulative KiB per bucket (index matches bounds)
	count   float64   // total KiB across all buckets (incl. +Inf overflow)
	sum     float64   // KiB-weighted TTL sum: Σ(KiB × expireTime)
}

func newKibCollector(namespace, set, ttlUnit string, buckets []float64) *kibCollector {
	sorted := make([]float64, len(buckets))
	copy(sorted, buckets)
	sort.Float64s(sorted)

	desc := prometheus.NewDesc(
		"aerospike_ttl_kib_hist",
		"Size-weighted TTL histogram: bucket counts represent total KiB of records in each TTL bucket. Counter — rate()/increase() over a window to read the per-window TTL-size distribution, not the raw value.",
		[]string{"storage_type"},
		prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttlUnit},
	)

	return &kibCollector{
		desc:    desc,
		bounds:  sorted,
		weights: make([]float64, len(sorted)),
	}
}

func (c *kibCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *kibCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	cumBuckets := make(map[float64]uint64, len(c.bounds))
	var cum float64
	for i, bound := range c.bounds {
		cum += c.weights[i]
		cumBuckets[bound] = uint64(math.Round(cum))
	}
	count := uint64(math.Round(c.count))
	sum := c.sum
	c.mu.Unlock()

	ch <- prometheus.MustNewConstHistogram(c.desc, count, sum, cumBuckets, "recordsize")
}

// addWeight records one record's size (converted to KiB) into the TTL bucket
// matching expireTime. O(1) per record (binary search over bucket bounds).
func (c *kibCollector) addWeight(expireTime float64, sizeBytes int) {
	if sizeBytes <= 0 {
		return
	}
	w := float64(sizeBytes) / 1024.0
	idx := sort.SearchFloat64s(c.bounds, expireTime)

	c.mu.Lock()
	if idx < len(c.bounds) {
		c.weights[idx] += w
	}
	c.count += w
	c.sum += w * expireTime
	c.mu.Unlock()
}
