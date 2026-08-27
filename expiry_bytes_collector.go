package main

import (
	"math"
	"sort"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// expiryBytesCollector is a custom prometheus.Collector that accumulates a
// size-weighted TTL histogram in O(1) per record. Bucket counts represent
// total bytes of records falling into each TTL bucket.
//
// Weights are accumulated as float64 bytes; the prometheus exposition format
// requires uint64 bucket counts, so values are rounded once at Collect time.
type expiryBytesCollector struct {
	mu      sync.Mutex
	desc    *prometheus.Desc
	bounds  []float64 // sorted bucket upper bounds (mirrors the TTL histogram)
	weights []float64 // non-cumulative bytes per bucket (index matches bounds)
	count   float64   // total bytes across all buckets (incl. +Inf overflow)
	sum     float64   // bytes-weighted TTL sum: Σ(bytes × expireTime)
}

func newExpiryBytesCollector(namespace, set, ttlUnit string, buckets []float64) *expiryBytesCollector {
	sorted := make([]float64, len(buckets))
	copy(sorted, buckets)
	sort.Float64s(sorted)

	desc := prometheus.NewDesc(
		"aerospike_ttl_expiry_bytes_hist",
		"Size-weighted TTL histogram: bucket counts represent total bytes of records in each TTL bucket.",
		nil,
		prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttlUnit},
	)

	return &expiryBytesCollector{
		desc:    desc,
		bounds:  sorted,
		weights: make([]float64, len(sorted)),
	}
}

func (c *expiryBytesCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *expiryBytesCollector) Collect(ch chan<- prometheus.Metric) {
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

	ch <- prometheus.MustNewConstHistogram(c.desc, count, sum, cumBuckets)
}

// addWeight records one record's size (raw bytes) into the TTL bucket
// matching expireTime. O(1) per record (binary search over bucket bounds).
func (c *expiryBytesCollector) addWeight(expireTime float64, sizeBytes int) {
	if sizeBytes <= 0 {
		return
	}
	w := float64(sizeBytes)
	idx := sort.SearchFloat64s(c.bounds, expireTime)

	c.mu.Lock()
	if idx < len(c.bounds) {
		c.weights[idx] += w
	}
	c.count += w
	c.sum += w * expireTime
	c.mu.Unlock()
}
