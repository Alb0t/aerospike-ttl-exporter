package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestKibCollectorEmptyCollect(t *testing.T) {
	c := newKibCollector("ns1", "foo", "days", []float64{10, 20, 30})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	count := testutil.CollectAndCount(c)
	if count != 1 {
		t.Errorf("expected 1 metric (the histogram), got %d", count)
	}
}

func TestKibCollectorSingleRecord(t *testing.T) {
	c := newKibCollector("ns1", "foo", "days", []float64{10, 20, 30})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	c.addWeight(15.0, 2048) // 2 KiB, TTL=15 days -> bucket le=20

	expected := `
# HELP aerospike_ttl_kib_hist Size-weighted TTL histogram: bucket counts represent total KiB of records in each TTL bucket.
# TYPE aerospike_ttl_kib_hist histogram
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="foo",storage_type="recordsize",ttlUnit="days",le="10"} 0
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="foo",storage_type="recordsize",ttlUnit="days",le="20"} 2
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="foo",storage_type="recordsize",ttlUnit="days",le="30"} 2
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="foo",storage_type="recordsize",ttlUnit="days",le="+Inf"} 2
aerospike_ttl_kib_hist_sum{namespace="ns1",set="foo",storage_type="recordsize",ttlUnit="days"} 30
aerospike_ttl_kib_hist_count{namespace="ns1",set="foo",storage_type="recordsize",ttlUnit="days"} 2
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestKibCollectorMultipleRecordsDifferentBuckets(t *testing.T) {
	c := newKibCollector("ns1", "bar", "seconds", []float64{100, 200, 300})

	c.addWeight(50.0, 1024)  // 1 KiB -> bucket le=100
	c.addWeight(150.0, 2048) // 2 KiB -> bucket le=200
	c.addWeight(250.0, 3072) // 3 KiB -> bucket le=300

	expected := `
# HELP aerospike_ttl_kib_hist Size-weighted TTL histogram: bucket counts represent total KiB of records in each TTL bucket.
# TYPE aerospike_ttl_kib_hist histogram
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="bar",storage_type="recordsize",ttlUnit="seconds",le="100"} 1
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="bar",storage_type="recordsize",ttlUnit="seconds",le="200"} 3
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="bar",storage_type="recordsize",ttlUnit="seconds",le="300"} 6
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="bar",storage_type="recordsize",ttlUnit="seconds",le="+Inf"} 6
aerospike_ttl_kib_hist_sum{namespace="ns1",set="bar",storage_type="recordsize",ttlUnit="seconds"} 1100
aerospike_ttl_kib_hist_count{namespace="ns1",set="bar",storage_type="recordsize",ttlUnit="seconds"} 6
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestKibCollectorOverflowToInf(t *testing.T) {
	c := newKibCollector("ns1", "baz", "days", []float64{10, 20})

	c.addWeight(25.0, 1024) // 1 KiB, TTL=25 > all bounds -> only in +Inf and count

	expected := `
# HELP aerospike_ttl_kib_hist Size-weighted TTL histogram: bucket counts represent total KiB of records in each TTL bucket.
# TYPE aerospike_ttl_kib_hist histogram
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="baz",storage_type="recordsize",ttlUnit="days",le="10"} 0
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="baz",storage_type="recordsize",ttlUnit="days",le="20"} 0
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="baz",storage_type="recordsize",ttlUnit="days",le="+Inf"} 1
aerospike_ttl_kib_hist_sum{namespace="ns1",set="baz",storage_type="recordsize",ttlUnit="days"} 25
aerospike_ttl_kib_hist_count{namespace="ns1",set="baz",storage_type="recordsize",ttlUnit="days"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestKibCollectorZeroSizeSkipped(t *testing.T) {
	c := newKibCollector("ns1", "z", "days", []float64{10})
	c.addWeight(5.0, 0)
	c.addWeight(5.0, -1)

	expected := `
# HELP aerospike_ttl_kib_hist Size-weighted TTL histogram: bucket counts represent total KiB of records in each TTL bucket.
# TYPE aerospike_ttl_kib_hist histogram
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="z",storage_type="recordsize",ttlUnit="days",le="10"} 0
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="z",storage_type="recordsize",ttlUnit="days",le="+Inf"} 0
aerospike_ttl_kib_hist_sum{namespace="ns1",set="z",storage_type="recordsize",ttlUnit="days"} 0
aerospike_ttl_kib_hist_count{namespace="ns1",set="z",storage_type="recordsize",ttlUnit="days"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestKibCollectorBoundaryValue(t *testing.T) {
	c := newKibCollector("ns1", "edge", "days", []float64{10, 20})

	c.addWeight(10.0, 1024) // 1 KiB, exactly on boundary le=10 -> goes in that bucket

	expected := `
# HELP aerospike_ttl_kib_hist Size-weighted TTL histogram: bucket counts represent total KiB of records in each TTL bucket.
# TYPE aerospike_ttl_kib_hist histogram
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="edge",storage_type="recordsize",ttlUnit="days",le="10"} 1
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="edge",storage_type="recordsize",ttlUnit="days",le="20"} 1
aerospike_ttl_kib_hist_bucket{namespace="ns1",set="edge",storage_type="recordsize",ttlUnit="days",le="+Inf"} 1
aerospike_ttl_kib_hist_sum{namespace="ns1",set="edge",storage_type="recordsize",ttlUnit="days"} 10
aerospike_ttl_kib_hist_count{namespace="ns1",set="edge",storage_type="recordsize",ttlUnit="days"} 1
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}
