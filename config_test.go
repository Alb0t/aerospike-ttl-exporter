package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestExampleConfigsValid decodes and validates every examples/*.yaml so a
// malformed or drifted example fails CI instead of a user's startup. validate()
// log.Fatals on bad config (os.Exit), so it runs in a subprocess per file; a
// non-zero exit means that example is broken.
func TestExampleConfigsValid(t *testing.T) {
	files, err := filepath.Glob("examples/*.yaml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no example configs found (glob err=%v)", err)
	}
	if os.Getenv("GO_TEST_SUBPROCESS") == "1" {
		var c conf
		raw, rerr := os.ReadFile(os.Getenv("EXAMPLE_FILE"))
		if rerr != nil {
			os.Exit(1)
		}
		if yaml.Unmarshal(raw, &c) != nil {
			os.Exit(1)
		}
		c.validate() // fatals (os.Exit) on invalid config
		return
	}
	for _, f := range files {
		t.Run(f, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestExampleConfigsValid$")
			cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS=1", "EXAMPLE_FILE="+f)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s failed validation:\n%s", f, out)
			}
		})
	}
}

func TestConfigDecodeStaticMonitor(t *testing.T) {
	// A non-discovery config: explicit monitor entries using the unified
	// ttlBuckets/sizeBuckets blocks. Must decode and resolve to a concrete
	// monconf the legacy []monconf scan path can consume.
	raw := `
service:
  listenPort: ":9634"
  frequencySecs: 1
monitor:
  - namespace: mynamespace
    set: User
    recordCount: 50000
    scanPercent: 1.1
    reportCount: 300000
    scanTotalTimeout: 20m
    scanSocketTimeout: 20m
    policyTotalTimeout: 20m
    policySocketTimeout: 20m
    ttlBuckets:
      mode: static
      static:
        - 160d
        - 170d
  - namespace: someothernamespace
    set: null
    recordCount: -1
    scanPercent: 1.0
    ttlBuckets:
      mode: linear
      start: 180d
      width: 10d
      count: 10
    sizeHistogramEnabled: true
    sizeBuckets:
      mode: exponential
      min: 1
      max: 8389000
      count: 10
`
	var c conf
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Service.AutoDiscover {
		t.Error("autoDiscover must default to false")
	}

	m0 := c.Monitor[0].resolve(monconf{})
	if m0.Namespace != "mynamespace" || m0.Set != "User" || m0.Recordcount != 50000 ||
		m0.ScanPercent != 1.1 || m0.ScanTotalTimeout != "20m" {
		t.Errorf("entry0 resolved wrong: %+v", m0)
	}
	if m0.TTLBuckets.Mode != "static" || len(m0.TTLBuckets.Static) != 2 || m0.TTLBuckets.Static[0] != "160d" {
		t.Errorf("ttlBuckets lost: %+v", m0.TTLBuckets)
	}

	m1 := c.Monitor[1].resolve(monconf{})
	if m1.Set != "" { // `set: null` -> ""
		t.Errorf("null set should resolve to empty string, got %q", m1.Set)
	}
	if m1.Recordcount != -1 || m1.TTLBuckets.Mode != "linear" || m1.TTLBuckets.Start != "180d" ||
		m1.TTLBuckets.Width != "10d" || m1.TTLBuckets.Count != 10 {
		t.Errorf("entry1 ttlBuckets resolved wrong: %+v", m1)
	}
	if !m1.SizeHistogramEnabled || m1.SizeBuckets.Mode != "exponential" || m1.SizeBuckets.Max != "8389000" {
		t.Errorf("entry1 sizeBuckets resolved wrong: %+v", m1.SizeBuckets)
	}
}

func TestConfigDecodeDiscovery(t *testing.T) {
	raw := `
service:
  listenPort: ":9634"
  autoDiscover: true
  discoveryIntervalSecs: 300
  discoveryBucketCount: 10
  discoveryRangePaddingPct: 25
  discoveryDefaults:
    scanPercent: 1.0
    sizeHistogramEnabled: true
    ttlBuckets:
      mode: auto
    sizeBuckets:
      mode: exponential
      min: 1
      max: 8389000
      count: 10
monitor:
  - namespace: ns1
    set: foo
    scanPercent: 50.0
  - namespace: ns1
    set: null
    recordsPerSecond: 200
`
	var c conf
	if err := yaml.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !c.Service.AutoDiscover || c.Service.DiscoveryBucketCount != 10 {
		t.Errorf("service discovery fields wrong: %+v", c.Service)
	}
	if c.Service.DiscoveryRangePaddingPct != 25 {
		t.Errorf("discoveryRangePaddingPct = %d, want 25", c.Service.DiscoveryRangePaddingPct)
	}
	if c.Service.DiscoveryDefaults.ScanPercent != 1.0 || !c.Service.DiscoveryDefaults.SizeHistogramEnabled {
		t.Errorf("discoveryDefaults decoded wrong: %+v", c.Service.DiscoveryDefaults)
	}
	if c.Service.DiscoveryDefaults.TTLBuckets.Mode != "auto" {
		t.Errorf("discoveryDefaults ttlBuckets mode = %q, want auto", c.Service.DiscoveryDefaults.TTLBuckets.Mode)
	}
	if sb := c.Service.DiscoveryDefaults.SizeBuckets; sb.Mode != "exponential" || sb.Count != 10 {
		t.Errorf("discoveryDefaults sizeBuckets decoded wrong: %+v", sb)
	}

	// foo override: only scanPercent explicitly set -> pointer non-nil, others nil.
	foo := findOverride(c.Monitor, "ns1", "foo")
	if foo == nil || foo.ScanPercent == nil || *foo.ScanPercent != 50.0 {
		t.Fatalf("foo override scanPercent wrong: %+v", foo)
	}
	if foo.RecordsPerSecond != nil {
		t.Errorf("unset field should be nil pointer, got %v", *foo.RecordsPerSecond)
	}

	// null set matched by empty string.
	null := findOverride(c.Monitor, "ns1", "")
	if null == nil || null.RecordsPerSecond == nil || *null.RecordsPerSecond != 200 {
		t.Fatalf("null-set override decoded wrong: %+v", null)
	}
}

// An unset discoveryDefaults.recordCount (zero value) must normalize to the -1
// "no cap" sentinel, else drainScan's `exported >= Recordcount` cap fires after
// a single record and every discovered set scans exactly one record.
func TestValidateNormalizesUnsetDiscoveryRecordcount(t *testing.T) {
	var c conf
	c.Service.AutoDiscover = true
	c.Service.DiscoveryBucketCount = 10
	c.validate()
	if c.Service.DiscoveryDefaults.Recordcount != -1 {
		t.Errorf("unset discovery recordCount = %d, want -1 (no cap)", c.Service.DiscoveryDefaults.Recordcount)
	}
}

// An explicitly configured discoveryDefaults.recordCount must be preserved.
func TestValidateKeepsExplicitDiscoveryRecordcount(t *testing.T) {
	var c conf
	c.Service.AutoDiscover = true
	c.Service.DiscoveryBucketCount = 10
	c.Service.DiscoveryDefaults.Recordcount = 500
	c.validate()
	if c.Service.DiscoveryDefaults.Recordcount != 500 {
		t.Errorf("explicit discovery recordCount = %d, want 500", c.Service.DiscoveryDefaults.Recordcount)
	}
}

// runSubprocessTest re-runs a single test function in a subprocess so
// log.Fatal (which calls os.Exit) can be caught without killing the parent.
// Returns combined stderr+stdout and whether the subprocess exited non-zero.
func runSubprocessTest(t *testing.T, testName string) (string, bool) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(), "GO_TEST_SUBPROCESS=1")
	out, err := cmd.CombinedOutput()
	return string(out), err != nil
}

func TestValidateFatalsSizeHistEnabledWithoutMode(t *testing.T) {
	if os.Getenv("GO_TEST_SUBPROCESS") == "1" {
		var c conf
		c.Service.AutoDiscover = true
		c.Service.DiscoveryBucketCount = 10
		c.Service.DiscoveryDefaults.SizeHistogramEnabled = true
		c.validate()
		return
	}
	out, failed := runSubprocessTest(t, "TestValidateFatalsSizeHistEnabledWithoutMode")
	if !failed {
		t.Fatal("expected fatal exit when sizeHistogramEnabled=true with no sizeBuckets mode")
	}
	if !strings.Contains(out, "sizeHistogramEnabled requires sizeBuckets") {
		t.Errorf("unexpected fatal message: %s", out)
	}
}

func TestValidateFatalsUnknownTTLScale(t *testing.T) {
	if os.Getenv("GO_TEST_SUBPROCESS") == "1" {
		var c conf
		c.Service.AutoDiscover = true
		c.Service.DiscoveryBucketCount = 10
		c.Service.DiscoveryDefaults.TTLBuckets = bucketConfig{Mode: "auto", Scale: "bogus"}
		c.validate()
		return
	}
	out, failed := runSubprocessTest(t, "TestValidateFatalsUnknownTTLScale")
	if !failed {
		t.Fatal("expected fatal exit on unknown ttlBuckets scale")
	}
	if !strings.Contains(out, "unknown scale") {
		t.Errorf("unexpected fatal message: %s", out)
	}
}

func TestValidateFatalsScaleOnNonAutoMode(t *testing.T) {
	if os.Getenv("GO_TEST_SUBPROCESS") == "1" {
		var c conf
		c.Service.AutoDiscover = true
		c.Service.DiscoveryBucketCount = 10
		// scale only applies to mode=auto; pairing it with linear is a config error.
		c.Service.DiscoveryDefaults.TTLBuckets = bucketConfig{
			Mode: "linear", Start: "1d", Width: "1d", Count: 3, Scale: "exponential",
		}
		c.validate()
		return
	}
	out, failed := runSubprocessTest(t, "TestValidateFatalsScaleOnNonAutoMode")
	if !failed {
		t.Fatal("expected fatal exit when scale set on a non-auto mode")
	}
	if !strings.Contains(out, "only applies to mode=auto") {
		t.Errorf("unexpected fatal message: %s", out)
	}
}

func TestValidateFatalsSizeHistOverrideWithoutMode(t *testing.T) {
	if os.Getenv("GO_TEST_SUBPROCESS") == "1" {
		en := true
		var c conf
		c.Service.AutoDiscover = true
		c.Service.DiscoveryBucketCount = 10
		c.Monitor = []monconfOverride{{
			Namespace:            "ns1",
			Set:                  "s1",
			SizeHistogramEnabled: &en,
		}}
		c.validate()
		return
	}
	out, failed := runSubprocessTest(t, "TestValidateFatalsSizeHistOverrideWithoutMode")
	if !failed {
		t.Fatal("expected fatal exit when override enables sizeHistogramEnabled with no sizeBuckets mode")
	}
	if !strings.Contains(out, "enables sizeHistogramEnabled but has no sizeBuckets mode") {
		t.Errorf("unexpected fatal message: %s", out)
	}
}

func TestValidatePassesSizeHistWithMode(t *testing.T) {
	var c conf
	c.Service.AutoDiscover = true
	c.Service.DiscoveryBucketCount = 10
	c.Service.DiscoveryDefaults.SizeHistogramEnabled = true
	c.Service.DiscoveryDefaults.SizeBuckets = bucketConfig{
		Mode: "exponential", Min: "1", Max: "8389000", Count: 10,
	}
	c.validate()
}

func TestValidatePassesSizeHistDisabled(t *testing.T) {
	var c conf
	c.Service.AutoDiscover = true
	c.Service.DiscoveryBucketCount = 10
	c.validate()
}
