package main

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestDefaultTTLGauge(t *testing.T) {
	defaultTTLGauge.Reset()
	defaultTTLGauge.WithLabelValues("ns1").Set(432000)

	expected := `
# HELP aerospike_ttl_default_ttl_seconds Namespace default-ttl in seconds as reported by Aerospike (0 = never expire).
# TYPE aerospike_ttl_default_ttl_seconds gauge
aerospike_ttl_default_ttl_seconds{namespace="ns1"} 432000
`
	if err := testutil.CollectAndCompare(defaultTTLGauge, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestBuildInfoExposesVersionAndCommit(t *testing.T) {
	buildInfo.Reset()
	buildInfo.WithLabelValues("v5.1.1", "abc1234").Set(1)

	expected := `
# HELP aerospike_ttl_build_info Build info
# TYPE aerospike_ttl_build_info gauge
aerospike_ttl_build_info{commit="abc1234",version="v5.1.1"} 1
`
	if err := testutil.CollectAndCompare(buildInfo, strings.NewReader(expected)); err != nil {
		t.Error(err)
	}
}

func TestTTLRangePublishesMinMax(t *testing.T) {
	minTTLGauge.Reset()
	maxTTLGauge.Reset()

	var r ttlRange
	for _, ttl := range []uint32{500, 100, 900, 300} {
		r.observe(ttl)
	}
	r.publish("ns1", "foo")

	expectedMin := `
# HELP aerospike_ttl_min_ttl_seconds Lowest record TTL in seconds observed in the most recent scan of this set.
# TYPE aerospike_ttl_min_ttl_seconds gauge
aerospike_ttl_min_ttl_seconds{namespace="ns1",set="foo"} 100
`
	expectedMax := `
# HELP aerospike_ttl_max_ttl_seconds Highest record TTL in seconds observed in the most recent scan of this set.
# TYPE aerospike_ttl_max_ttl_seconds gauge
aerospike_ttl_max_ttl_seconds{namespace="ns1",set="foo"} 900
`
	if err := testutil.CollectAndCompare(minTTLGauge, strings.NewReader(expectedMin)); err != nil {
		t.Error(err)
	}
	if err := testutil.CollectAndCompare(maxTTLGauge, strings.NewReader(expectedMax)); err != nil {
		t.Error(err)
	}
}

func TestTTLRangeNoRecordsLeavesGaugesUntouched(t *testing.T) {
	minTTLGauge.Reset()
	maxTTLGauge.Reset()

	var r ttlRange // nothing observed
	r.publish("ns1", "empty")

	if n := testutil.CollectAndCount(minTTLGauge); n != 0 {
		t.Errorf("min gauge should have no series when no record observed, got %d", n)
	}
	if n := testutil.CollectAndCount(maxTTLGauge); n != 0 {
		t.Errorf("max gauge should have no series when no record observed, got %d", n)
	}
}
