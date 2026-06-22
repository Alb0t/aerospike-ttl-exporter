package main

import (
	"reflect"
	"testing"
)

func TestPickUnit(t *testing.T) {
	cases := []struct {
		name         string
		maxSec       int
		wantUnit     string
		wantModifier int
	}{
		{"three days -> days", 3 * 86400, "days", 86400},
		{"just over two days -> days", 2*86400 + 1, "days", 86400},
		{"exactly two days -> hours", 2 * 86400, "hours", 3600},
		{"six hours -> hours", 6 * 3600, "hours", 3600},
		{"just over two hours -> hours", 2*3600 + 1, "hours", 3600},
		{"exactly two hours -> seconds", 2 * 3600, "seconds", 1},
		{"ninety seconds -> seconds", 90, "seconds", 1},
		{"zero -> seconds", 0, "seconds", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			unit, mod := pickUnit(c.maxSec)
			if unit != c.wantUnit || mod != c.wantModifier {
				t.Fatalf("pickUnit(%d) = (%q, %d), want (%q, %d)",
					c.maxSec, unit, mod, c.wantUnit, c.wantModifier)
			}
		})
	}
}

func TestParseNamespaces(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"test;bar;baz", []string{"test", "bar", "baz"}},
		{"test;bar;", []string{"test", "bar"}}, // trailing empty dropped
		{"solo", []string{"solo"}},
		{"", nil},
	}
	for _, c := range cases {
		got := parseNamespaces(c.raw)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseNamespaces(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParseSetNames(t *testing.T) {
	raw := "ns=test:set=foo:objects=5:tombstones=0;ns=test:set=bar:objects=3;"
	want := []string{"foo", "bar"}
	got := parseSetNames(raw)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("parseSetNames = %v, want %v", got, want)
	}
	if got := parseSetNames(""); got != nil {
		t.Errorf("parseSetNames(empty) = %v, want nil", got)
	}
}

func TestParseNamespaceField(t *testing.T) {
	raw := "ns_cluster_size=4;default-ttl=2592000;repl-factor=2"
	v, ok := parseNamespaceField(raw, "default-ttl")
	if !ok || v != 2592000 {
		t.Errorf("parseNamespaceField default-ttl = %d,%v want 2592000,true", v, ok)
	}
	if _, ok := parseNamespaceField(raw, "missing-key"); ok {
		t.Error("expected ok=false for missing key")
	}
}

func TestMonconfOverrideResolve(t *testing.T) {
	base := monconf{
		ScanPercent:          10,
		Recordcount:          5,
		ReportCount:          1000,
		ScanTotalTimeout:     "30s",
		SizeHistogramEnabled: true,
	}

	t.Run("empty override leaves base untouched (except ns/set)", func(t *testing.T) {
		ovr := monconfOverride{Namespace: "ns1", Set: "foo"}
		got := ovr.resolve(base)
		want := base
		want.Namespace = "ns1"
		want.Set = "foo"
		if !reflect.DeepEqual(got, want) {
			t.Errorf("resolve(empty) = %+v, want %+v", got, want)
		}
	})

	t.Run("only explicitly-set fields override", func(t *testing.T) {
		sp := 50.0
		dis := false
		ovr := monconfOverride{
			Namespace:            "ns1",
			Set:                  "bar",
			ScanPercent:          &sp,
			SizeHistogramEnabled: &dis,
		}
		got := ovr.resolve(base)
		if got.ScanPercent != 50 {
			t.Errorf("ScanPercent = %v, want 50", got.ScanPercent)
		}
		if got.SizeHistogramEnabled != false {
			t.Errorf("SizeHistogramEnabled = %v, want false (explicit override to zero value)", got.SizeHistogramEnabled)
		}
		if got.Recordcount != 5 || got.ReportCount != 1000 || got.ScanTotalTimeout != "30s" {
			t.Errorf("unset fields changed: %+v", got)
		}
	})
}

func TestFitBuckets(t *testing.T) {
	t.Run("fits observed min/max into linear buckets", func(t *testing.T) {
		// width 10s, populated indices 2..5 -> minSec=20, maxSec=60.
		// n=4 bins must SPAN [20,60] inclusive: top edge == maxSec, else the
		// (maxSec-width, maxSec] slice overflows into +Inf. -> N+1 edges.
		hist := []int64{0, 0, 3, 5, 0, 2, 0}
		got, unit, mod, expirable := fitBuckets(10, hist, 999, 4, 0)
		if !expirable {
			t.Fatal("expected expirable=true")
		}
		if unit != "seconds" || mod != 1 {
			t.Errorf("unit/mod = %q/%d, want seconds/1", unit, mod)
		}
		want := []float64{20, 30, 40, 50, 60}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("buckets = %v, want %v", got, want)
		}
	})

	t.Run("padding extends the top edge above observed max for drift headroom", func(t *testing.T) {
		// width 10s, populated 2..5 -> minSec=20, maxSec=60. paddingPct=50 pushes
		// the top edge to 90 so a TTL that drifts up to 90s still lands in a real
		// bin instead of +Inf. Bins still start at the observed min (20).
		hist := []int64{0, 0, 3, 5, 0, 2, 0}
		got, _, _, expirable := fitBuckets(10, hist, 999, 4, 50)
		if !expirable {
			t.Fatal("expected expirable=true")
		}
		if got[0] != 20 {
			t.Errorf("first edge = %v, want 20 (observed min, unpadded)", got[0])
		}
		if top := got[len(got)-1]; top != 90 {
			t.Errorf("top edge = %v, want 90 (60 + 50%%); no headroom = +Inf overflow on drift", top)
		}
	})

	t.Run("falls back to 0..default-ttl when histogram empty", func(t *testing.T) {
		hist := []int64{0, 0, 0}
		got, unit, mod, expirable := fitBuckets(10, hist, 3*86400, 3, 0)
		if !expirable {
			t.Fatal("expected expirable=true via default-ttl fallback")
		}
		if unit != "days" || mod != 86400 {
			t.Errorf("unit/mod = %q/%d, want days/86400", unit, mod)
		}
		// n=3 bins -> n+1=4 edges spanning [0, 3] days inclusive.
		if len(got) != 4 {
			t.Fatalf("len(buckets) = %d, want 4", len(got))
		}
		if got[0] != 0 {
			t.Errorf("first bucket = %v, want 0", got[0])
		}
		if got[len(got)-1] != 3 {
			t.Errorf("top bucket = %v, want 3 (maxSec=3d); short top edge = +Inf overflow", got[len(got)-1])
		}
	})

	t.Run("non-expirable when histogram empty and default-ttl zero", func(t *testing.T) {
		got, _, _, expirable := fitBuckets(10, []int64{0, 0, 0}, 0, 4, 0)
		if expirable {
			t.Fatal("expected expirable=false")
		}
		if got != nil {
			t.Errorf("expected nil buckets, got %v", got)
		}
	})
}

func TestFindOverride(t *testing.T) {
	a := monconfOverride{Namespace: "ns1", Set: "foo"}
	b := monconfOverride{Namespace: "ns1", Set: ""} // null-set override
	list := []monconfOverride{a, b}

	if got := findOverride(list, "ns1", "foo"); got == nil || got.Set != "foo" {
		t.Errorf("expected to match ns1:foo, got %+v", got)
	}
	if got := findOverride(list, "ns1", ""); got == nil || got.Set != "" {
		t.Errorf("expected to match null set, got %+v", got)
	}
	if got := findOverride(list, "ns2", "foo"); got != nil {
		t.Errorf("expected no match for ns2:foo, got %+v", got)
	}
}

func TestBuildEffectiveSet(t *testing.T) {
	defaults := monconf{ScanPercent: 10, ReportCount: 1000, SizeHistogramEnabled: true}

	t.Run("expirable: fits buckets, applies defaults", func(t *testing.T) {
		// width 10s, populated 2..5 -> 20..60s -> seconds unit
		es := buildEffectiveSet("ns1", "foo", 10, []int64{0, 0, 3, 5, 0, 2}, 999, defaults, nil, 4, 0)
		if es.key() != "ns1:foo" {
			t.Errorf("key = %q", es.key())
		}
		if !es.expirable {
			t.Fatal("want expirable")
		}
		if es.ttlUnit != "seconds" || es.modifier != 1 {
			t.Errorf("unit/mod = %q/%d", es.ttlUnit, es.modifier)
		}
		if len(es.buckets) != 5 { // n=4 bins -> n+1 edges
			t.Errorf("len(buckets) = %d, want 5", len(es.buckets))
		}
		if es.cfg.ScanPercent != 10 || !es.cfg.SizeHistogramEnabled {
			t.Errorf("cfg defaults not carried: %+v", es.cfg)
		}
	})

	t.Run("non-expirable: empty hist + zero default-ttl", func(t *testing.T) {
		es := buildEffectiveSet("ns1", "bar", 10, []int64{0, 0, 0}, 0, defaults, nil, 4, 0)
		if es.expirable {
			t.Fatal("want non-expirable")
		}
		if es.buckets != nil {
			t.Errorf("want nil buckets, got %v", es.buckets)
		}
		if !es.cfg.SizeHistogramEnabled {
			t.Error("size histogram default must survive on non-expirable set")
		}
	})

	t.Run("override wins field-by-field", func(t *testing.T) {
		sp := 50.0
		ovr := &monconfOverride{Namespace: "ns1", Set: "foo", ScanPercent: &sp}
		es := buildEffectiveSet("ns1", "foo", 10, []int64{0, 1}, 999, defaults, ovr, 4, 0)
		if es.cfg.ScanPercent != 50 {
			t.Errorf("ScanPercent = %v, want 50 (override)", es.cfg.ScanPercent)
		}
		if es.cfg.ReportCount != 1000 {
			t.Errorf("ReportCount = %v, want 1000 (default kept)", es.cfg.ReportCount)
		}
	})
}

func TestTTLBucketsFrom(t *testing.T) {
	t.Run("static parses suffixed values", func(t *testing.T) {
		b := bucketConfig{Mode: "static", Static: []string{"1d", "3d", "7d"}}
		got, unit, mod, exp := ttlBucketsFrom(b, 0, nil, 0, 0, 0)
		if !exp || unit != "days" || mod != 86400 {
			t.Fatalf("got exp=%v unit=%q mod=%d", exp, unit, mod)
		}
		if !reflect.DeepEqual(got, []float64{1, 3, 7}) {
			t.Errorf("buckets = %v, want [1 3 7]", got)
		}
	})

	t.Run("linear builds start/width/count", func(t *testing.T) {
		b := bucketConfig{Mode: "linear", Start: "10s", Width: "5s", Count: 3}
		got, unit, mod, exp := ttlBucketsFrom(b, 0, nil, 0, 0, 0)
		if !exp || unit != "seconds" || mod != 1 {
			t.Fatalf("got exp=%v unit=%q mod=%d", exp, unit, mod)
		}
		if !reflect.DeepEqual(got, []float64{10, 15, 20}) {
			t.Errorf("buckets = %v, want [10 15 20]", got)
		}
	})

	t.Run("exponential builds min/max/count", func(t *testing.T) {
		b := bucketConfig{Mode: "exponential", Min: "1h", Max: "4h", Count: 3}
		got, unit, mod, exp := ttlBucketsFrom(b, 0, nil, 0, 0, 0)
		if !exp || unit != "hours" || mod != 3600 {
			t.Fatalf("got exp=%v unit=%q mod=%d", exp, unit, mod)
		}
		if len(got) != 3 || got[0] != 1 || got[len(got)-1] != 4 {
			t.Errorf("buckets = %v, want span [1..4]", got)
		}
	})

	t.Run("auto falls back to fitBuckets on live histogram", func(t *testing.T) {
		b := bucketConfig{Mode: "auto"}
		// width 10s, populated 2..5 -> 20..60s; matches fitBuckets behavior.
		got, unit, _, exp := ttlBucketsFrom(b, 10, []int64{0, 0, 3, 5, 0, 2}, 999, 4, 0)
		if !exp || unit != "seconds" {
			t.Fatalf("got exp=%v unit=%q", exp, unit)
		}
		if !reflect.DeepEqual(got, []float64{20, 30, 40, 50, 60}) {
			t.Errorf("buckets = %v, want [20 30 40 50 60]", got)
		}
	})
}

func TestSizeBucketsFrom(t *testing.T) {
	t.Run("static parses raw floats", func(t *testing.T) {
		got := sizeBucketsFrom(bucketConfig{Mode: "static", Static: []string{"100", "1000", "10000"}})
		if !reflect.DeepEqual(got, []float64{100, 1000, 10000}) {
			t.Errorf("buckets = %v", got)
		}
	})

	t.Run("linear builds start/width/count", func(t *testing.T) {
		got := sizeBucketsFrom(bucketConfig{Mode: "linear", Start: "0", Width: "1000", Count: 3})
		if !reflect.DeepEqual(got, []float64{0, 1000, 2000}) {
			t.Errorf("buckets = %v", got)
		}
	})

	t.Run("exponential builds min/max/count", func(t *testing.T) {
		got := sizeBucketsFrom(bucketConfig{Mode: "exponential", Min: "1", Max: "8389000", Count: 10})
		if len(got) != 10 || got[0] != 1 {
			t.Errorf("buckets = %v, want 10 buckets starting at 1", got)
		}
	})
}

func TestParseTTLHistogram(t *testing.T) {
	t.Run("parses width and buckets", func(t *testing.T) {
		raw := "units=seconds:hist-width=86400:bucket-width=864:buckets=0,0,5,10,0"
		width, buckets, err := parseTTLHistogram(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if width != 864 {
			t.Errorf("width = %d, want 864", width)
		}
		want := []int64{0, 0, 5, 10, 0}
		if !reflect.DeepEqual(buckets, want) {
			t.Errorf("buckets = %v, want %v", buckets, want)
		}
	})

	t.Run("tolerates field reordering", func(t *testing.T) {
		raw := "buckets=1,2,3:units=seconds:bucket-width=10:hist-width=30"
		width, buckets, err := parseTTLHistogram(raw)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if width != 10 || !reflect.DeepEqual(buckets, []int64{1, 2, 3}) {
			t.Errorf("got width=%d buckets=%v", width, buckets)
		}
	})

	t.Run("errors when bucket-width missing", func(t *testing.T) {
		_, _, err := parseTTLHistogram("units=seconds:buckets=1,2,3")
		if err == nil {
			t.Fatal("expected error for missing bucket-width")
		}
	})

	t.Run("errors when buckets missing", func(t *testing.T) {
		_, _, err := parseTTLHistogram("units=seconds:bucket-width=10")
		if err == nil {
			t.Fatal("expected error for missing buckets")
		}
	})
}
