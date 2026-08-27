package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestExpiryBytesCollectorEmptyCollect(t *testing.T) {
	c := newExpiryBytesCollector("ns1", "foo", "days", []float64{10, 20, 30})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	count := testutil.CollectAndCount(c)
	if count != 1 {
		t.Errorf("expected 1 metric (the histogram), got %d", count)
	}
}

func TestExpiryBytesCollectorSingleRecord(t *testing.T) {
	c := newExpiryBytesCollector("ns1", "foo", "days", []float64{10, 20, 30})
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	c.addWeight(15.0, 2048) // 2048 bytes, TTL=15 days -> bucket le=20

	expected := `
# HELP aerospike_ttl_expiry_bytes_hist Size-weighted TTL histogram: bucket counts represent total bytes of records in each TTL bucket.
# TYPE aerospike_ttl_expiry_bytes_hist histogram
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="foo",ttlUnit="days",le="10"} 0
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="foo",ttlUnit="days",le="20"} 2048
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="foo",ttlUnit="days",le="30"} 2048
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="foo",ttlUnit="days",le="+Inf"} 2048
aerospike_ttl_expiry_bytes_hist_sum{namespace="ns1",set="foo",ttlUnit="days"} 30720
aerospike_ttl_expiry_bytes_hist_count{namespace="ns1",set="foo",ttlUnit="days"} 2048
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestExpiryBytesCollectorMultipleRecordsDifferentBuckets(t *testing.T) {
	c := newExpiryBytesCollector("ns1", "bar", "seconds", []float64{100, 200, 300})

	c.addWeight(50.0, 1024)  // 1024 bytes -> bucket le=100
	c.addWeight(150.0, 2048) // 2048 bytes -> bucket le=200
	c.addWeight(250.0, 3072) // 3072 bytes -> bucket le=300

	expected := `
# HELP aerospike_ttl_expiry_bytes_hist Size-weighted TTL histogram: bucket counts represent total bytes of records in each TTL bucket.
# TYPE aerospike_ttl_expiry_bytes_hist histogram
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="bar",ttlUnit="seconds",le="100"} 1024
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="bar",ttlUnit="seconds",le="200"} 3072
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="bar",ttlUnit="seconds",le="300"} 6144
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="bar",ttlUnit="seconds",le="+Inf"} 6144
aerospike_ttl_expiry_bytes_hist_sum{namespace="ns1",set="bar",ttlUnit="seconds"} 1126400
aerospike_ttl_expiry_bytes_hist_count{namespace="ns1",set="bar",ttlUnit="seconds"} 6144
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestExpiryBytesCollectorOverflowToInf(t *testing.T) {
	c := newExpiryBytesCollector("ns1", "baz", "days", []float64{10, 20})

	c.addWeight(25.0, 1024) // 1024 bytes, TTL=25 > all bounds -> only in +Inf and count

	expected := `
# HELP aerospike_ttl_expiry_bytes_hist Size-weighted TTL histogram: bucket counts represent total bytes of records in each TTL bucket.
# TYPE aerospike_ttl_expiry_bytes_hist histogram
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="baz",ttlUnit="days",le="10"} 0
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="baz",ttlUnit="days",le="20"} 0
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="baz",ttlUnit="days",le="+Inf"} 1024
aerospike_ttl_expiry_bytes_hist_sum{namespace="ns1",set="baz",ttlUnit="days"} 25600
aerospike_ttl_expiry_bytes_hist_count{namespace="ns1",set="baz",ttlUnit="days"} 1024
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestExpiryBytesCollectorZeroSizeSkipped(t *testing.T) {
	c := newExpiryBytesCollector("ns1", "z", "days", []float64{10})
	c.addWeight(5.0, 0)
	c.addWeight(5.0, -1)

	expected := `
# HELP aerospike_ttl_expiry_bytes_hist Size-weighted TTL histogram: bucket counts represent total bytes of records in each TTL bucket.
# TYPE aerospike_ttl_expiry_bytes_hist histogram
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="z",ttlUnit="days",le="10"} 0
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="z",ttlUnit="days",le="+Inf"} 0
aerospike_ttl_expiry_bytes_hist_sum{namespace="ns1",set="z",ttlUnit="days"} 0
aerospike_ttl_expiry_bytes_hist_count{namespace="ns1",set="z",ttlUnit="days"} 0
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestExpiryBytesCollectorBoundaryValue(t *testing.T) {
	c := newExpiryBytesCollector("ns1", "edge", "days", []float64{10, 20})

	c.addWeight(10.0, 1024) // 1024 bytes, exactly on boundary le=10

	expected := `
# HELP aerospike_ttl_expiry_bytes_hist Size-weighted TTL histogram: bucket counts represent total bytes of records in each TTL bucket.
# TYPE aerospike_ttl_expiry_bytes_hist histogram
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="edge",ttlUnit="days",le="10"} 1024
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="edge",ttlUnit="days",le="20"} 1024
aerospike_ttl_expiry_bytes_hist_bucket{namespace="ns1",set="edge",ttlUnit="days",le="+Inf"} 1024
aerospike_ttl_expiry_bytes_hist_sum{namespace="ns1",set="edge",ttlUnit="days"} 10240
aerospike_ttl_expiry_bytes_hist_count{namespace="ns1",set="edge",ttlUnit="days"} 1024
`
	if err := testutil.CollectAndCompare(c, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}
