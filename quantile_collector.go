package main

import (
	"math"
	"sort"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

var defaultQuantileTargets = []float64{0.20, 0.50, 0.90, 0.99}

// resolveQuantileTargets returns the effective quantile list. nil (unconfigured)
// falls back to defaults; an explicit empty list disables quantiles entirely.
func resolveQuantileTargets(configured []float64) []float64 {
	if configured == nil {
		return defaultQuantileTargets
	}
	return configured
}

// resolvePerDimensionTargets resolves a per-dimension target list, falling back
// to the blanket quantileTargets when the per-dimension list is nil.
func resolvePerDimensionTargets(perDimension, blanket []float64) []float64 {
	if perDimension != nil {
		return perDimension
	}
	return resolveQuantileTargets(blanket)
}

var quantileRefreshTS = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "quantile_last_refresh_success_ts",
		Help:      "Unix epoch seconds when quantile summaries were last successfully computed.",
	},
	[]string{"namespace", "set"},
)

// quantileCollector accumulates raw observations during a scan and, once
// finalized, exposes exact quantile summaries. Double-buffered: scrapes
// return the previous completed run while the current scan accumulates.
// Each dimension (TTL, size, TTL-weighted-by-bytes) is independently optional
// via its target slice: a nil slice means that dimension is disabled.
type quantileCollector struct {
	mu        sync.Mutex
	namespace string
	set       string

	stagingTTL     []float64
	stagingSize    []float64
	stagingTTLSize []weightedObs

	liveTTL     *quantileResult
	liveSize    *quantileResult
	liveTTLSize *quantileResult

	ttlUnit        string
	ttlTargets     []float64
	sizeTargets    []float64
	ttlSizeTargets []float64
	ttlD           *prometheus.Desc
	sizeD          *prometheus.Desc
	ttlSizeD       *prometheus.Desc
}

type quantileResult struct {
	count     uint64
	sum       float64
	quantiles map[float64]float64
}

type weightedObs struct {
	value  float64
	weight float64
}

// quantileDimTargets holds the per-dimension target slices used to construct a
// quantileCollector. A nil slice disables that dimension entirely.
type quantileDimTargets struct {
	ttl     []float64
	size    []float64
	ttlSize []float64
}

// anyEnabled returns true when at least one dimension has targets.
func (d quantileDimTargets) anyEnabled() bool {
	return len(d.ttl) > 0 || len(d.size) > 0 || len(d.ttlSize) > 0
}

func newQuantileCollector(namespace, set, ttlUnit string, dims quantileDimTargets) *quantileCollector {
	qc := &quantileCollector{
		namespace:      namespace,
		set:            set,
		ttlUnit:        ttlUnit,
		ttlTargets:     dims.ttl,
		sizeTargets:    dims.size,
		ttlSizeTargets: dims.ttlSize,
	}
	if len(dims.ttl) > 0 {
		qc.ttlD = prometheus.NewDesc(
			"aerospike_ttl_expiry_count_quantiles",
			"Exact quantile summary of record TTLs from the most recent complete scan.",
			nil,
			prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttlUnit},
		)
	}
	if len(dims.size) > 0 {
		qc.sizeD = prometheus.NewDesc(
			"aerospike_ttl_size_bytes_quantiles",
			"Exact quantile summary of record sizes (bytes) from the most recent complete scan.",
			nil,
			prometheus.Labels{"namespace": namespace, "set": set},
		)
	}
	if len(dims.ttlSize) > 0 {
		qc.ttlSizeD = prometheus.NewDesc(
			"aerospike_ttl_expiry_bytes_quantiles",
			"Exact quantile summary of record TTLs weighted by record size (bytes) from the most recent complete scan.",
			nil,
			prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttlUnit},
		)
	}
	return qc
}

func (q *quantileCollector) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stagingTTL = q.stagingTTL[:0]
	q.stagingSize = q.stagingSize[:0]
	q.stagingTTLSize = q.stagingTTLSize[:0]
}

func (q *quantileCollector) observeTTL(v float64) {
	if len(q.ttlTargets) > 0 {
		q.stagingTTL = append(q.stagingTTL, v)
	}
}

func (q *quantileCollector) observeSize(v float64) {
	if len(q.sizeTargets) > 0 {
		q.stagingSize = append(q.stagingSize, v)
	}
}

func (q *quantileCollector) observeTTLWithSize(ttl, size float64) {
	if len(q.ttlSizeTargets) > 0 {
		q.stagingTTLSize = append(q.stagingTTLSize, weightedObs{value: ttl, weight: size})
	}
}

// needsSize returns true when any enabled dimension requires record size reads.
func (q *quantileCollector) needsSize() bool {
	return len(q.sizeTargets) > 0 || len(q.ttlSizeTargets) > 0
}

func (q *quantileCollector) finalize() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.ttlTargets) > 0 {
		q.liveTTL = computeQuantiles(q.stagingTTL, q.ttlTargets)
	}
	if len(q.sizeTargets) > 0 {
		q.liveSize = computeQuantiles(q.stagingSize, q.sizeTargets)
	}
	if len(q.ttlSizeTargets) > 0 {
		q.liveTTLSize = computeWeightedQuantiles(q.stagingTTLSize, q.ttlSizeTargets)
	}
	quantileRefreshTS.WithLabelValues(q.namespace, q.set).Set(float64(time.Now().Unix()))
}

func computeQuantiles(data []float64, targets []float64) *quantileResult {
	if len(data) == 0 {
		return nil
	}
	sort.Float64s(data)
	var sum float64
	for _, v := range data {
		sum += v
	}
	qm := make(map[float64]float64, len(targets))
	for _, qt := range targets {
		qm[qt] = exactQuantile(data, qt)
	}
	return &quantileResult{count: uint64(len(data)), sum: sum, quantiles: qm}
}

func computeWeightedQuantiles(data []weightedObs, targets []float64) *quantileResult {
	if len(data) == 0 {
		return nil
	}
	sort.Slice(data, func(i, j int) bool { return data[i].value < data[j].value })
	var totalWeight float64
	for _, o := range data {
		totalWeight += o.weight
	}
	qm := make(map[float64]float64, len(targets))
	for _, qt := range targets {
		qm[qt] = weightedQuantile(data, totalWeight, qt)
	}
	return &quantileResult{count: uint64(len(data)), sum: totalWeight, quantiles: qm}
}

func weightedQuantile(sorted []weightedObs, totalWeight, q float64) float64 {
	threshold := q * totalWeight
	var cum float64
	for _, o := range sorted {
		cum += o.weight
		if cum >= threshold {
			return o.value
		}
	}
	return sorted[len(sorted)-1].value
}

func exactQuantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	idx := q * float64(len(sorted)-1)
	lower := int(math.Floor(idx))
	upper := int(math.Ceil(idx))
	if lower == upper || upper >= len(sorted) {
		return sorted[lower]
	}
	frac := idx - float64(lower)
	return sorted[lower]*(1-frac) + sorted[upper]*frac
}

func (q *quantileCollector) Describe(ch chan<- *prometheus.Desc) {
	if q.ttlD != nil {
		ch <- q.ttlD
	}
	if q.sizeD != nil {
		ch <- q.sizeD
	}
	if q.ttlSizeD != nil {
		ch <- q.ttlSizeD
	}
}

func (q *quantileCollector) Collect(ch chan<- prometheus.Metric) {
	q.mu.Lock()
	liveTTL := q.liveTTL
	liveSize := q.liveSize
	liveTTLSize := q.liveTTLSize
	q.mu.Unlock()

	if liveTTL != nil && q.ttlD != nil {
		ch <- summaryMetric(q.ttlD, liveTTL, q.ttlTargets)
	}
	if liveSize != nil && q.sizeD != nil {
		ch <- summaryMetric(q.sizeD, liveSize, q.sizeTargets)
	}
	if liveTTLSize != nil && q.ttlSizeD != nil {
		ch <- summaryMetric(q.ttlSizeD, liveTTLSize, q.ttlSizeTargets)
	}
}

func summaryMetric(desc *prometheus.Desc, r *quantileResult, targets []float64) prometheus.Metric {
	qv := make(map[float64]float64, len(targets))
	for _, qt := range targets {
		if v, ok := r.quantiles[qt]; ok {
			qv[qt] = v
		}
	}
	m, err := prometheus.NewConstSummary(desc, r.count, r.sum, qv)
	if err != nil {
		panic(err)
	}
	return m
}
