package main

import (
	"flag"
	"os"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// buildVersion and buildCommit are injected at release time by goreleaser
// (ldflags -X main.buildVersion={{.Version}} -X main.buildCommit={{.ShortCommit}}).
// "dev"/"unknown" for local/non-release builds. buildCommit disambiguates two
// builds that share a tag (e.g. a re-cut release): the version label alone can't.
var buildVersion = "dev"
var buildCommit = "unknown"
var configFile = flag.String("configFile", "/etc/ttl-aerospike-exporter.yaml", "The yaml config file for the exporter")

// ns_set_to_histSet maps "ns:set" to the collectors built at startup for the
// legacy (non-discovery) path. Written once during setup, read-only afterwards.
var ns_set_to_histSet = make(map[string]*histSet)

var buildInfo = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "build_info",
		Help:      "Build info",
	},
	[]string{"version", "commit"},
)

var scanTimes = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "scan_time_seconds",
		Help:      "Scan times in seconds.",
	},
	[]string{"namespace", "set"},
)

var scanLastUpdated = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "scan_last_updated",
		Help:      "Epoch time that scan last finished.",
	},
	[]string{"namespace", "set"},
)

var defaultTTLGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "default_ttl_seconds",
		Help:      "Namespace default-ttl in seconds as reported by Aerospike (0 = never expire).",
	},
	[]string{"namespace"},
)

// min/max observed TTL gauges expose the lowest and highest record TTL (in raw
// seconds) seen during the most recent scan of a set. Together they bound the
// live TTL range, giving an "age by proxy" signal: how close the youngest and
// oldest-by-expiry records are to expiring. Non-expirable records are excluded.
var minTTLGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "min_ttl_seconds",
		Help:      "Lowest record TTL in seconds observed in the most recent scan of this set.",
	},
	[]string{"namespace", "set"},
)

var maxTTLGauge = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "max_ttl_seconds",
		Help:      "Highest record TTL in seconds observed in the most recent scan of this set.",
	},
	[]string{"namespace", "set"},
)

// these are global because im lazy
var running atomic.Bool              // guards against overlapping scans (scheduler may fire while a scan is still running).
var localIps = make(map[string]bool) // map to prevent duplicates, and a list of what our local ips are
var config conf

type conf struct {
	Service serviceConf
	Monitor []monconfOverride
}

type serviceConf struct {
	ListenPort    string `yaml:"listenPort"`
	SkipNodeCheck bool   `yaml:"skipNodeCheck"`
	FrequencySecs int    `yaml:"frequencySecs"`
	Verbose       bool   `yaml:"verbose"`
	Username      string `yaml:"username"`
	Password      string `yaml:"password"`
	AerospikeAddr string `yaml:"aerospikeAddr"`
	AerospikePort int    `yaml:"aerospikePort"`
	// Auto-discovery mode: enumerate namespaces/sets from Aerospike and build
	// TTL histogram config automatically. When false, behavior is unchanged and
	// only the explicit `monitor:` entries are scanned.
	AutoDiscover          bool `yaml:"autoDiscover"`
	DiscoveryIntervalSecs int  `yaml:"discoveryIntervalSecs"`
	DiscoveryBucketCount  int  `yaml:"discoveryBucketCount"`
	// DiscoveryRangePaddingPct extends each fitted TTL range's top edge by this
	// percentage, leaving headroom for the Aerospike ttl histogram to rescale
	// (and live TTLs to drift past the observed max) between discovery passes
	// without spilling into +Inf. 0 = no padding.
	DiscoveryRangePaddingPct int `yaml:"discoveryRangePaddingPct"`
	// DiscoveryOutlierPct sets the minimum percentage of total records a
	// histogram bucket must hold to count as "populated" when fitting the TTL
	// range. Buckets below this threshold are treated as outlier noise and
	// excluded from the min/max range computation, preventing a handful of
	// stray records from blowing out the bucket resolution. 0 = no filtering.
	DiscoveryOutlierPct float64 `yaml:"discoveryOutlierPct"`
	DiscoveryDefaults   monconf `yaml:"discoveryDefaults"`
}

// bucketConfig is the unified histogram bucket specification shared by TTL
// histograms (expiry_count_hist/expiry_bytes_hist) and the size histogram (size_bytes_hist).
// Mode selects the strategy; the other fields supply that strategy's inputs.
// Min/Max/Start/Width are strings so TTL values can carry a d/h/s suffix
// (parsed via parseTimeValues); for size buckets they are numeric strings
// parsed as float64 (raw bytes).
type bucketConfig struct {
	Mode   string   `yaml:"mode"`
	Static []string `yaml:"static,omitempty"`
	Start  string   `yaml:"start,omitempty"`
	Width  string   `yaml:"width,omitempty"`
	Min    string   `yaml:"min,omitempty"`
	Max    string   `yaml:"max,omitempty"`
	Count  int      `yaml:"count,omitempty"`
	// Scale applies only to mode=auto (TTL discovery): "exponential" gives the
	// fitted range geometric spacing (better resolution for skewed TTLs); empty
	// or "linear" keeps evenly-spaced bins. Ignored by static/linear/exponential
	// modes, which spell their spacing out directly.
	Scale string `yaml:"scale,omitempty"`
}

type monconf struct {
	Namespace                 string       `yaml:"namespace"`
	Set                       string       `yaml:"set"`
	Recordcount               int          `yaml:"recordCount,omitempty"`
	ScanPercent               float64      `yaml:"scanPercent,omitempty"`
	ReportCount               int          `yaml:"reportCount,omitempty"`
	ScanTotalTimeout          string       `yaml:"scanTotalTimeout"`
	ScanSocketTimeout         string       `yaml:"scanSocketTimeout"`
	PolicyTotalTimeout        string       `yaml:"policyTotalTimeout"`
	PolicySocketTimeout       string       `yaml:"policySocketTimeout"`
	RecordsPerSecond          int          `yaml:"recordsPerSecond"`
	TTLCountsHistogramEnabled *bool        `yaml:"ttlCountsHistogramEnabled,omitempty"`
	TTLBytesHistogramEnabled  bool         `yaml:"ttlBytesHistogramEnabled,omitempty"`
	SizeHistogramEnabled      bool         `yaml:"sizeHistogramEnabled"`
	TTLBuckets                bucketConfig `yaml:"ttlBuckets"`
	SizeBuckets               bucketConfig `yaml:"sizeBuckets"`
	QuantileTargets           []float64    `yaml:"quantileTargets,omitempty"`
	TTLCountsQuantileTargets  []float64    `yaml:"ttlCountsQuantileTargets,omitempty"`
	SizeQuantileTargets       []float64    `yaml:"sizeQuantileTargets,omitempty"`
	TTLBytesQuantileTargets   []float64    `yaml:"ttlBytesQuantileTargets,omitempty"`
}

// monconfOverride mirrors monconf but with pointer fields for every
// override-eligible knob, so an absent yaml key (nil pointer) is distinguishable
// from a key explicitly set to a zero value. This lets a `monitor:` entry
// override a discovered/default set's config field-by-field. Namespace and Set
// are plain strings because they are the match key, not overridable values.
type monconfOverride struct {
	Namespace                 string        `yaml:"namespace"`
	Set                       string        `yaml:"set"`
	Recordcount               *int          `yaml:"recordCount,omitempty"`
	ScanPercent               *float64      `yaml:"scanPercent,omitempty"`
	ReportCount               *int          `yaml:"reportCount,omitempty"`
	ScanTotalTimeout          *string       `yaml:"scanTotalTimeout,omitempty"`
	ScanSocketTimeout         *string       `yaml:"scanSocketTimeout,omitempty"`
	PolicyTotalTimeout        *string       `yaml:"policyTotalTimeout,omitempty"`
	PolicySocketTimeout       *string       `yaml:"policySocketTimeout,omitempty"`
	RecordsPerSecond          *int          `yaml:"recordsPerSecond,omitempty"`
	TTLCountsHistogramEnabled *bool         `yaml:"ttlCountsHistogramEnabled,omitempty"`
	TTLBytesHistogramEnabled  *bool         `yaml:"ttlBytesHistogramEnabled,omitempty"`
	SizeHistogramEnabled      *bool         `yaml:"sizeHistogramEnabled,omitempty"`
	TTLBuckets                *bucketConfig `yaml:"ttlBuckets,omitempty"`
	SizeBuckets               *bucketConfig `yaml:"sizeBuckets,omitempty"`
	QuantileTargets           *[]float64    `yaml:"quantileTargets,omitempty"`
	TTLCountsQuantileTargets  *[]float64    `yaml:"ttlCountsQuantileTargets,omitempty"`
	SizeQuantileTargets       *[]float64    `yaml:"sizeQuantileTargets,omitempty"`
	TTLBytesQuantileTargets   *[]float64    `yaml:"ttlBytesQuantileTargets,omitempty"`
}

// resolve produces a concrete monconf by starting from base and overwriting only
// the fields the user explicitly set (non-nil pointers). Namespace and Set are
// always taken from the override. In the legacy (non-discovery) path base is the
// zero monconf, so resolve simply materializes whatever the user configured.
func (o monconfOverride) resolve(base monconf) monconf {
	m := base
	m.Namespace = o.Namespace
	m.Set = o.Set
	setIfPresent(&m.Recordcount, o.Recordcount)
	setIfPresent(&m.ScanPercent, o.ScanPercent)
	setIfPresent(&m.ReportCount, o.ReportCount)
	setIfPresent(&m.ScanTotalTimeout, o.ScanTotalTimeout)
	setIfPresent(&m.ScanSocketTimeout, o.ScanSocketTimeout)
	setIfPresent(&m.PolicyTotalTimeout, o.PolicyTotalTimeout)
	setIfPresent(&m.PolicySocketTimeout, o.PolicySocketTimeout)
	setIfPresent(&m.RecordsPerSecond, o.RecordsPerSecond)
	if o.TTLCountsHistogramEnabled != nil {
		m.TTLCountsHistogramEnabled = o.TTLCountsHistogramEnabled
	}
	setIfPresent(&m.TTLBytesHistogramEnabled, o.TTLBytesHistogramEnabled)
	setIfPresent(&m.SizeHistogramEnabled, o.SizeHistogramEnabled)
	// Bucket configs replace wholesale (no field-by-field merge within a block):
	// a non-nil override block entirely supplants the default.
	setIfPresent(&m.TTLBuckets, o.TTLBuckets)
	setIfPresent(&m.SizeBuckets, o.SizeBuckets)
	setIfPresent(&m.QuantileTargets, o.QuantileTargets)
	setIfPresent(&m.TTLCountsQuantileTargets, o.TTLCountsQuantileTargets)
	setIfPresent(&m.SizeQuantileTargets, o.SizeQuantileTargets)
	setIfPresent(&m.TTLBytesQuantileTargets, o.TTLBytesQuantileTargets)
	return m
}

// setIfPresent overwrites *dst with *src when src is non-nil — i.e. when the
// user explicitly set that override field. A nil src leaves the base value.
func setIfPresent[T any](dst, src *T) {
	if src != nil {
		*dst = *src
	}
}

func (c *conf) setConf() {
	flag.Parse()
	yamlFile, err := os.ReadFile(*configFile)
	if err != nil {
		log.Fatal("Failed to read configfile: ", *configFile)
	}
	dec := yaml.NewDecoder(strings.NewReader(string(yamlFile)))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil {
		log.Fatalf("Failed to unmarshal configfile %s: %v (if you see an unknown field, check the Migration section in README.md — countsHistogramEnabled→ttlCountsHistogramEnabled, kbyteHistogramEnabled→ttlBytesHistogramEnabled)", *configFile, err)
	}
	c.setDefaults()
	c.validate()
}

func setDefault[T comparable](dst *T, val T) {
	var zero T
	if *dst == zero {
		*dst = val
	}
}

func (c *conf) setDefaults() {
	s := &c.Service
	setDefault(&s.ListenPort, ":9634")
	setDefault(&s.AerospikeAddr, "127.0.0.1")
	setDefault(&s.AerospikePort, 3000)
	setDefault(&s.FrequencySecs, 300)
	setDefault(&s.DiscoveryIntervalSecs, 10800)
	setDefault(&s.DiscoveryBucketCount, 10)
	d := &s.DiscoveryDefaults
	setDefault(&d.ScanTotalTimeout, "5m")
	setDefault(&d.ScanSocketTimeout, "5m")
	setDefault(&d.PolicyTotalTimeout, "5m")
	setDefault(&d.PolicySocketTimeout, "5m")
	setDefault(&d.TTLBuckets.Mode, "auto")
}

// validate fatals on any malformed bucketConfig in the discovery defaults or in
// the per-set monitor overrides. Bad config is unrecoverable, matching the
// exporter's existing fail-fast behavior on startup.
func (c *conf) validate() {
	// discoveryBucketCount is the bin count fitBuckets divides the fitted range
	// by; 0 would be a divide-by-zero producing a single degenerate +Inf bucket.
	if c.Service.AutoDiscover && c.Service.DiscoveryBucketCount <= 0 {
		log.Fatal("autoDiscover requires discoveryBucketCount > 0")
	}
	// Recordcount's zero value (unset) is a degenerate cap: drainScan stops once
	// exported >= Recordcount, so 0 would terminate every scan after a single
	// record. In discovery mode the cap comes solely from DiscoveryDefaults, so
	// coerce an unset value to the -1 "no cap" sentinel instead of silently
	// scanning one record per set.
	if c.Service.AutoDiscover && c.Service.DiscoveryDefaults.Recordcount == 0 {
		c.Service.DiscoveryDefaults.Recordcount = -1
	}
	validateQuantileTargets(c.Service.DiscoveryDefaults.QuantileTargets)
	validateQuantileTargets(c.Service.DiscoveryDefaults.TTLCountsQuantileTargets)
	validateQuantileTargets(c.Service.DiscoveryDefaults.SizeQuantileTargets)
	validateQuantileTargets(c.Service.DiscoveryDefaults.TTLBytesQuantileTargets)
	c.Service.DiscoveryDefaults.TTLBuckets.validate("ttl")
	c.Service.DiscoveryDefaults.SizeBuckets.validate("size")
	c.Service.DiscoveryDefaults.validateSizeHistBuckets()
	for i := range c.Monitor {
		if c.Monitor[i].TTLBuckets != nil {
			c.Monitor[i].TTLBuckets.validate("ttl")
		}
		if c.Monitor[i].SizeBuckets != nil {
			c.Monitor[i].SizeBuckets.validate("size")
		}
		c.Monitor[i].validateSizeHistBuckets(c.Service.DiscoveryDefaults)
		if c.Monitor[i].QuantileTargets != nil {
			validateQuantileTargets(*c.Monitor[i].QuantileTargets)
		}
		if c.Monitor[i].TTLCountsQuantileTargets != nil {
			validateQuantileTargets(*c.Monitor[i].TTLCountsQuantileTargets)
		}
		if c.Monitor[i].SizeQuantileTargets != nil {
			validateQuantileTargets(*c.Monitor[i].SizeQuantileTargets)
		}
		if c.Monitor[i].TTLBytesQuantileTargets != nil {
			validateQuantileTargets(*c.Monitor[i].TTLBytesQuantileTargets)
		}
	}
	if !c.Service.AutoDiscover {
		c.validateLegacyTTLModes()
	}
}

// validateLegacyTTLModes fatals on monitor entries whose ttlBuckets mode is
// unset or "auto" when autoDiscover is off. The legacy path has no live ttl
// histogram to fit against, so those modes would silently fall through to
// prometheus.DefBuckets — buckets that are meaningless for TTLs.
func (c *conf) validateLegacyTTLModes() {
	for i := range c.Monitor {
		m := &c.Monitor[i]
		if m.TTLBuckets == nil || m.TTLBuckets.Mode == "" || m.TTLBuckets.Mode == "auto" {
			log.Fatalf("monitor entry %s:%s requires explicit ttlBuckets mode (static|linear|exponential) when autoDiscover is off",
				m.Namespace, m.Set)
		}
	}
}

// validate fatals if the bucketConfig is malformed for its kind ("ttl"/"size").
// An empty Mode is allowed (means "not configured"): TTL falls back to auto-fit
// and size histograms are gated by sizeHistogramEnabled instead.
func (b bucketConfig) validate(kind string) {
	b.validateScale(kind)
	switch b.Mode {
	case "":
		return
	case "auto":
		if kind == "size" {
			log.Fatal("sizeBuckets does not support mode: auto")
		}
	case "static":
		if len(b.Static) == 0 {
			log.Fatalf("%sBuckets mode=static requires a non-empty static list", kind)
		}
	case "linear":
		if b.Start == "" || b.Width == "" || b.Count <= 0 {
			log.Fatalf("%sBuckets mode=linear requires start, width, and count>0", kind)
		}
	case "exponential":
		b.validateExponential(kind)
	default:
		log.Fatalf("%sBuckets unknown mode %q (want static|linear|exponential|auto)", kind, b.Mode)
	}
}

// validateScale rejects an unknown scale value and an exponential scale on
// anything but mode=auto (only the auto-fit path consults scale; spelling it
// out elsewhere would silently do nothing).
func (b bucketConfig) validateScale(kind string) {
	switch b.Scale {
	case "", "linear", "exponential":
	default:
		log.Fatalf("%sBuckets unknown scale %q (want linear|exponential)", kind, b.Scale)
	}
	if b.Scale != "" && b.Mode != "" && b.Mode != "auto" {
		log.Fatalf("%sBuckets scale=%q only applies to mode=auto, not mode=%q", kind, b.Scale, b.Mode)
	}
}

// validateExponential enforces min>0, max>min, count>0 for exponential mode.
func (b bucketConfig) validateExponential(kind string) {
	if b.Min == "" || b.Max == "" || b.Count <= 0 {
		log.Fatalf("%sBuckets mode=exponential requires min, max, and count>0", kind)
	}
	var mn, mx float64
	if kind == "ttl" {
		vals, _, _ := parseTimeValues([]string{b.Min, b.Max})
		mn, mx = vals[0], vals[1]
	} else {
		mn, mx = parseFloat(b.Min), parseFloat(b.Max)
	}
	if mn <= 0 || mx <= mn {
		log.Fatalf("%sBuckets mode=exponential requires min>0 and max>min", kind)
	}
}

func (m monconf) validateSizeHistBuckets() {
	if m.SizeHistogramEnabled && m.SizeBuckets.Mode == "" {
		log.Fatal("sizeHistogramEnabled requires sizeBuckets with an explicit mode (static|linear|exponential)")
	}
}

func (o monconfOverride) validateSizeHistBuckets(defaults monconf) {
	resolved := o.resolve(defaults)
	if resolved.SizeHistogramEnabled && resolved.SizeBuckets.Mode == "" {
		log.Fatalf("monitor entry %s:%s enables sizeHistogramEnabled but has no sizeBuckets mode (need static|linear|exponential)",
			o.Namespace, o.Set)
	}
}

// ttlCountsHistEnabled resolves the tri-state *bool: nil (unset) → true (default on).
func ttlCountsHistEnabled(p *bool) bool {
	if p == nil {
		return true
	}
	return *p
}

func validateQuantileTargets(targets []float64) {
	for _, q := range targets {
		if q <= 0 || q >= 1 {
			log.Fatalf("quantileTargets values must be in (0, 1), got %f", q)
		}
	}
}

// parseFloat parses a raw numeric bucket boundary (bytes, for size buckets),
// fataling on malformed input.
func parseFloat(s string) float64 {
	v, perr := strconv.ParseFloat(s, 64)
	if perr != nil {
		log.Fatalf("bad numeric bucket value %q: %v", s, perr)
	}
	return v
}

// parseFloats parses a list of raw numeric bucket boundaries.
func parseFloats(arr []string) []float64 {
	out := make([]float64, 0, len(arr))
	for _, s := range arr {
		out = append(out, parseFloat(s))
	}
	return out
}

func parseTimeValues(arr []string) ([]float64, string, int) {
	if len(arr) == 0 {
		log.Fatal("Empty static bucket list?")
	}

	// Extract the unit from the first string to ensure consistency
	unit := arr[0][len(arr[0])-1:]

	// Check all strings in the array to ensure they use the same unit
	for _, s := range arr {
		if !strings.HasSuffix(s, string(unit)) {
			log.Fatal("Only 1 time suffix supported at a time, cannot be mixed.")
		}
	}

	// Parse the numerical parts
	var values []float64
	for _, s := range arr {
		val, err := strconv.ParseFloat(s[:len(s)-1], 64)
		if err != nil {
			log.Fatal("String conversion to float failure")
		}
		values = append(values, val)
	}

	// Convert the unit to its descriptive form
	var unitDesc string
	var secondsPerUnit int
	switch unit {
	case "d":
		unitDesc = "days"
		secondsPerUnit = 86400
	case "h":
		unitDesc = "hours"
		secondsPerUnit = 3600
	case "s":
		unitDesc = "seconds"
		secondsPerUnit = 1
	default:
		log.Fatal("Unknown unit used")
	}

	return values, unitDesc, secondsPerUnit
}

// setup performs all runtime initialization: load config, register metrics,
// connect to Aerospike, and start the scan scheduler. It is called from main()
// rather than init() so the package can be imported by tests without parsing
// flags, reading a config file, or opening network connections.
// It returns the scheduler jobs it started so main() can stop them on shutdown.
func setup() []*scheduler.Job {
	config.setConf()
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetOutput(os.Stdout)

	if config.Service.Verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	// In auto-discovery mode the per-set histograms are built dynamically by the
	// discovery tick (see setupDiscovery below), so skip the static loop.
	if !config.Service.AutoDiscover {
		for i := range config.Monitor {
			buildLegacyHistograms(config.Monitor[i])
		}
	}
	registerCoreMetrics()

	// create a list of local ips to compare against and ensure we are checking the local node only
	// this should only need to happen once
	err := findLocalIps()
	if err != nil {
		log.Error("Exception in findLocalIps:", err)
	}

	// create client connection and setup policy
	aeroInit()

	return startSchedulers()
}

// registerCoreMetrics registers the process-wide collectors present in both
// discovery and legacy modes; per-set histograms are registered separately.
func registerCoreMetrics() {
	prometheus.MustRegister(scanTimes)
	prometheus.MustRegister(scanLastUpdated)
	prometheus.MustRegister(defaultTTLGauge)
	prometheus.MustRegister(minTTLGauge)
	prometheus.MustRegister(maxTTLGauge)
	prometheus.MustRegister(quantileRefreshTS)
	prometheus.MustRegister(buildInfo)
	buildInfo.WithLabelValues(buildVersion, buildCommit).Set(1)
}

// startSchedulers starts the discovery (if enabled) and scan scheduler jobs and
// returns them so main() can stop them on shutdown. Discovery is started first
// so its initial synchronous pass populates metrics before the first scan.
func startSchedulers() []*scheduler.Job {
	var jobs []*scheduler.Job

	// In discovery mode, build the initial set list synchronously so metrics
	// exist before the first scan, then re-discover on its own cadence.
	if config.Service.AutoDiscover {
		if job := setupDiscovery(); job != nil {
			jobs = append(jobs, job)
		}
	}

	if config.Service.Verbose {
		log.Info("Starting scheduler..")
	}
	// start process to start polling for stats
	scanJob, err := scheduler.Every(config.Service.FrequencySecs).Seconds().Run(runner)
	if err != nil {
		log.Error("Failed to schedule scan job:", err)
	} else {
		jobs = append(jobs, scanJob)
	}
	return jobs
}

// buildLegacyHistograms registers the per-set collectors for one explicit
// monitor entry (non-discovery mode) and records them in the lookup map the
// legacy scan path reads. Buckets come from the entry's explicit config;
// auto/empty TTL modes are rejected at config-validation time
// (validateLegacyTTLModes) since this path has no live histogram to fit.
func buildLegacyHistograms(override monconfOverride) {
	cfg := override.resolve(monconf{})
	buckets, ttlUnit, modifier, expirable := ttlBucketsFrom(cfg.TTLBuckets, 0, nil, 0, 0, 0, 0)
	es := effectiveSet{
		namespace: cfg.Namespace,
		set:       cfg.Set,
		buckets:   buckets,
		ttlUnit:   ttlUnit,
		modifier:  modifier,
		expirable: expirable,
		cfg:       cfg,
	}
	ns_set_to_histSet[es.key()] = buildHistSet(prometheus.DefaultRegisterer, es, "")
}

// setupDiscovery initializes the discovery registry, runs one synchronous
// discovery pass (so metrics are populated promptly), and schedules periodic
// re-discovery on its own interval independent of the scan cadence. It returns
// the scheduled job so main() can stop it on shutdown (nil if scheduling fails).
func setupDiscovery() *scheduler.Job {
	discoveryRegistry = newHistRegistry(prometheus.DefaultRegisterer)
	interval := config.Service.DiscoveryIntervalSecs
	if interval <= 0 {
		interval = config.Service.FrequencySecs
	}
	log.Info("Auto-discovery enabled. Running initial discovery pass..")
	runDiscovery()
	job, err := scheduler.Every(interval).Seconds().Run(runDiscovery)
	if err != nil {
		log.Error("Failed to schedule discovery job:", err)
		return nil
	}
	return job
}
