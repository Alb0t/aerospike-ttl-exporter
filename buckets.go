package main

import (
	"github.com/prometheus/client_golang/prometheus"
	logrus "github.com/sirupsen/logrus"
)

// buckets.go resolves histogram bucket boundaries from config. It is shared by
// the discovery path (which can additionally auto-fit TTL buckets to a live
// Aerospike ttl histogram) and the legacy explicit-monitor path, so the two
// modes can never disagree on what a bucketConfig means.

const (
	secondsPerHour = 3600
	secondsPerDay  = 86400
)

// pickUnit chooses a human-friendly display unit for a TTL range based on the
// magnitude of the largest observed TTL (in seconds). It mirrors how an
// operator would pick the d/h/s suffix by hand. Returns the unit label used on
// the ttlUnit metric label and the seconds-per-unit modifier used to convert
// raw expiration seconds before observing.
func pickUnit(maxSec int) (unit string, modifier int) {
	switch {
	case maxSec > 2*secondsPerDay:
		return "days", secondsPerDay
	case maxSec > 2*secondsPerHour:
		return "hours", secondsPerHour
	default:
		return "seconds", 1
	}
}

// populatedRangeSec returns the seconds-range [minSec, maxSec) spanned by the
// populated (non-zero) buckets of an Aerospike ttl histogram, given the
// per-bucket width. ok is false when no bucket holds any records.
func populatedRangeSec(bucketWidthSec int, buckets []int64) (minSec, maxSec int, ok bool) {
	first, last := -1, -1
	for i, c := range buckets {
		if c <= 0 {
			continue
		}
		if first == -1 {
			first = i
		}
		last = i
	}
	if first == -1 {
		return 0, 0, false
	}
	return first * bucketWidthSec, (last + 1) * bucketWidthSec, true
}

// fitBuckets builds a linear set of TTL histogram bucket boundaries (expressed
// in the chosen display unit) for a set, from its observed ttl histogram. The
// range is fit to the lowest/highest populated buckets. When no bucket is
// populated it falls back to 0..defaultTTL; when defaultTTL is also zero the
// set is treated as non-expirable (expirable=false, nil buckets). n is the
// number of linear bins; the returned slice has n+1 edges spanning
// [minSec, maxSec] inclusive (top edge == maxSec). paddingPct extends maxSec by
// that percentage to leave headroom above the observed max: Aerospike's ttl
// histogram rescales dynamically, so live record TTLs can drift past the fitted
// top edge between discovery passes; the padding keeps that drift out of +Inf.
func fitBuckets(bucketWidthSec int, buckets []int64, defaultTTL, n, paddingPct int) (bucketFloats []float64, unit string, modifier int, expirable bool) {
	minSec, maxSec, ok := populatedRangeSec(bucketWidthSec, buckets)
	if !ok {
		if defaultTTL <= 0 {
			return nil, "", 0, false
		}
		minSec, maxSec = 0, defaultTTL
	}
	maxSec += maxSec * paddingPct / 100

	unit, modifier = pickUnit(maxSec)
	start := float64(minSec) / float64(modifier)
	width := (float64(maxSec) - float64(minSec)) / float64(modifier) / float64(n)
	// n+1 edges so the bins SPAN [minSec, maxSec] inclusive: top edge == maxSec.
	// Emitting only n edges leaves the top edge one width short of maxSec, so the
	// (maxSec-width, maxSec] slice — where the densest TTLs sit — falls into +Inf.
	return prometheus.LinearBuckets(start, width, n+1), unit, modifier, true
}

// bucketsFromMode resolves the static/linear/exponential bucketConfig modes
// into boundaries, using parse to turn the config's value strings into floats
// (parseTimeValues-derived for TTL buckets, parseFloats for size buckets). Any
// other mode returns nil; the caller decides whether that means auto-fit (TTL)
// or a config error (size).
func bucketsFromMode(b bucketConfig, parse func([]string) []float64) []float64 {
	switch b.Mode {
	case "static":
		return parse(b.Static)
	case "linear":
		sw := parse([]string{b.Start, b.Width})
		return prometheus.LinearBuckets(sw[0], sw[1], b.Count)
	case "exponential":
		mm := parse([]string{b.Min, b.Max})
		return prometheus.ExponentialBucketsRange(mm[0], mm[1], b.Count)
	default:
		return nil
	}
}

// ttlBucketsFrom resolves a ttlBuckets config into prometheus bucket boundaries
// (in the chosen display unit), the unit label, and the seconds-per-unit
// modifier. static/linear/exponential are computed directly from config (TTL
// values carry d/h/s suffixes parsed by parseTimeValues); auto and empty mode
// fall back to fitBuckets, deriving the range from the live ttl histogram.
func ttlBucketsFrom(b bucketConfig, bucketWidthSec int, histBuckets []int64, defaultTTL, n, paddingPct int) (buckets []float64, unit string, modifier int, expirable bool) {
	if b.Mode == "" || b.Mode == "auto" {
		return fitBuckets(bucketWidthSec, histBuckets, defaultTTL, n, paddingPct)
	}
	parse := func(arr []string) []float64 {
		vals, u, mod := parseTimeValues(arr)
		unit, modifier = u, mod
		return vals
	}
	return bucketsFromMode(b, parse), unit, modifier, true
}

// sizeBucketsFrom resolves a sizeBuckets config into prometheus bucket
// boundaries (raw bytes). auto is rejected at config-validation time, so only
// static/linear/exponential reach here; an unset/invalid mode while the size
// histogram is enabled is a config error and fatals.
func sizeBucketsFrom(b bucketConfig) []float64 {
	out := bucketsFromMode(b, parseFloats)
	if out == nil {
		logrus.Fatalf("sizeBuckets: invalid mode %q while size histogram enabled (want static|linear|exponential)", b.Mode)
	}
	return out
}
