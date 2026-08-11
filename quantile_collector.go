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
type quantileCollector struct {
	mu        sync.Mutex
	namespace string
	set       string

	stagingTTL  []float64
	stagingSize []float64

	liveTTL  *quantileResult
	liveSize *quantileResult

	ttlUnit string
	targets []float64
}

type quantileResult struct {
	count     uint64
	sum       float64
	quantiles map[float64]float64
}

func newQuantileCollector(namespace, set, ttlUnit string, targets []float64) *quantileCollector {
	return &quantileCollector{
		namespace: namespace,
		set:       set,
		ttlUnit:   ttlUnit,
		targets:   targets,
	}
}

func (q *quantileCollector) reset() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.stagingTTL = q.stagingTTL[:0]
	q.stagingSize = q.stagingSize[:0]
}

func (q *quantileCollector) observeTTL(v float64) {
	q.mu.Lock()
	q.stagingTTL = append(q.stagingTTL, v)
	q.mu.Unlock()
}

func (q *quantileCollector) observeSize(v float64) {
	q.mu.Lock()
	q.stagingSize = append(q.stagingSize, v)
	q.mu.Unlock()
}

func (q *quantileCollector) finalize() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.liveTTL = computeQuantiles(q.stagingTTL, q.targets)
	q.liveSize = computeQuantiles(q.stagingSize, q.targets)
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
	ch <- q.ttlDesc()
	ch <- q.sizeDesc()
}

func (q *quantileCollector) Collect(ch chan<- prometheus.Metric) {
	q.mu.Lock()
	liveTTL := q.liveTTL
	liveSize := q.liveSize
	q.mu.Unlock()

	if liveTTL != nil {
		ch <- q.summaryMetric(q.ttlDesc(), liveTTL)
	}
	if liveSize != nil {
		ch <- q.summaryMetric(q.sizeDesc(), liveSize)
	}
}

func (q *quantileCollector) ttlDesc() *prometheus.Desc {
	return prometheus.NewDesc(
		"aerospike_ttl_ttl_quantiles",
		"Exact quantile summary of record TTLs from the most recent complete scan.",
		nil,
		prometheus.Labels{"namespace": q.namespace, "set": q.set, "ttlUnit": q.ttlUnit},
	)
}

func (q *quantileCollector) sizeDesc() *prometheus.Desc {
	return prometheus.NewDesc(
		"aerospike_ttl_size_bytes_quantiles",
		"Exact quantile summary of record sizes (bytes) from the most recent complete scan.",
		nil,
		prometheus.Labels{"namespace": q.namespace, "set": q.set},
	)
}

func (q *quantileCollector) summaryMetric(desc *prometheus.Desc, r *quantileResult) prometheus.Metric {
	qv := make(map[float64]float64, len(q.targets))
	for _, qt := range q.targets {
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
