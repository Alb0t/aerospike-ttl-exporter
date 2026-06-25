package main

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	as "github.com/aerospike/aerospike-client-go/v8"
	logrus "github.com/sirupsen/logrus"
)

// NullSet is the Prometheus label value for set-less (null-set) records.
// Aerospike represents the null set as "", but an empty Prometheus label is
// indistinguishable from an absent label, so we use an explicit sentinel.
const NullSet = "NULLSET"

// scanSet returns the Aerospike set name to pass to the client.  NullSet is
// mapped back to "" because the Aerospike client treats "" as "no set filter".
func scanSet(set string) string {
	if set == NullSet {
		return ""
	}
	return set
}

// discoveryRegistry owns the lifecycle of dynamically-discovered collectors.
// effectiveSets holds the latest []effectiveSet for the scanner to iterate; it
// is swapped atomically by runDiscovery and read by the scan runner.
// INVARIANT: runDiscovery must reconcile discoveryRegistry BEFORE storing into
// effectiveSets. The runner looks collectors up by the published list, so
// publishing first would let a scan cycle see a set whose collectors aren't
// registered yet (it would skip that set for the cycle).
var discoveryRegistry *histRegistry
var effectiveSets atomic.Value

// discovery.go implements auto-discovery mode: it asks Aerospike which
// namespaces and sets exist, reads each set's TTL distribution, and builds
// histogram bucket configuration automatically (bucket resolution itself
// lives in buckets.go).

// parseNamespaces parses the semicolon-delimited response of the Aerospike
// "namespaces" info call into a list of namespace names, dropping empties.
func parseNamespaces(raw string) []string {
	var out []string
	for _, ns := range strings.Split(raw, ";") {
		if ns != "" {
			out = append(out, ns)
		}
	}
	return out
}

// parseSetNames parses the response of "sets/<ns>" (semicolon-delimited
// records, each a colon-delimited list of k=v pairs) into the set names.
func parseSetNames(raw string) []string {
	var out []string
	for _, rec := range strings.Split(raw, ";") {
		if name, ok := setNameFromRecord(rec); ok {
			out = append(out, name)
		}
	}
	return out
}

// setNameFromRecord extracts the set= value from one colon-delimited record of
// a "sets/<ns>" response; ok is false when the record carries no set name
// (including the empty record a trailing semicolon produces).
func setNameFromRecord(rec string) (string, bool) {
	for _, kv := range strings.Split(rec, ":") {
		if name, ok := strings.CutPrefix(kv, "set="); ok {
			return name, true
		}
	}
	return "", false
}

// parseNamespaceField extracts an integer-valued field (e.g. "default-ttl")
// from the semicolon-delimited "namespace/<ns>" info response.
func parseNamespaceField(raw, key string) (int, bool) {
	v, ok := infoKV(raw, ";")[key]
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

// infoKV splits an Aerospike info response into its key=value pairs, using sep
// as the record delimiter (";" for the namespace/<ns> response, ":" for a
// histogram response). Pairs without an "=" are skipped.
func infoKV(raw, sep string) map[string]string {
	m := make(map[string]string)
	for _, kv := range strings.Split(raw, sep) {
		if k, v, ok := strings.Cut(kv, "="); ok {
			m[k] = v
		}
	}
	return m
}

// discoveryInfoRetryWindow bounds how long reqInfo retries a failing info call
// before giving up. Transient node/network blips would otherwise drop the set's
// series for a cycle (flapping); we retry with backoff instead, and once the
// window is exhausted the error propagates up so the discovery pass is skipped
// and the previous registry kept — a blip must not kill a healthy exporter.
const discoveryInfoRetryWindow = 30 * time.Second

// reqInfo issues a single asinfo command, retrying with exponential backoff on
// error until discoveryInfoRetryWindow elapses, then returning the last error.
func reqInfo(n *as.Node, cmd string) (string, error) {
	deadline := time.Now().Add(discoveryInfoRetryWindow)
	backoff := 500 * time.Millisecond
	for {
		info, err := n.RequestInfo(infoPolicy, cmd)
		if err == nil {
			return info[cmd], nil
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("info command %q failed after %s: %w", cmd, discoveryInfoRetryWindow, err)
		}
		logrus.Warnf("Discovery: info command %q failed (%v); retrying in %s", cmd, err, backoff)
		time.Sleep(backoff)
		if backoff < 4*time.Second {
			backoff *= 2
		}
	}
}

// discoverNamespaces lists the namespaces present on the node.
func discoverNamespaces(n *as.Node) ([]string, error) {
	raw, err := reqInfo(n, "namespaces")
	if err != nil {
		return nil, err
	}
	return parseNamespaces(raw), nil
}

// discoverSets lists the named sets in a namespace (excludes the null set).
func discoverSets(n *as.Node, ns string) ([]string, error) {
	raw, err := reqInfo(n, "sets/"+ns)
	if err != nil {
		return nil, err
	}
	return parseSetNames(raw), nil
}

// hasSetlessRecords reports whether the namespace holds records that belong to
// no set (the null set), computed as namespace total objects minus the sum of
// per-set objects. Returns false if any count is unavailable (info error) so we
// never publish a null-set series we can't substantiate; it re-evaluates on the
// next discovery pass.
func hasSetlessRecords(n *as.Node, ns string, namedSets []string) bool {
	nsObj := getCount(n, "objects", "namespace/"+ns, true)
	if nsObj < 0 {
		return false
	}
	var setObj int64
	for _, s := range namedSets {
		c := getCount(n, "objects", "sets/"+ns+"/"+s, true)
		if c < 0 {
			return false
		}
		setObj += c
	}
	return nsObj-setObj > 0
}

// discoverDefaultTTL reads the namespace default-ttl in seconds (0 = never).
func discoverDefaultTTL(n *as.Node, ns string) (int, error) {
	raw, err := reqInfo(n, "namespace/"+ns)
	if err != nil {
		return 0, err
	}
	ttl, _ := parseNamespaceField(raw, "default-ttl")
	return ttl, nil
}

// discoverTTLHistogram fetches the ttl-type histogram for a set. An empty set
// name selects the namespace-level histogram (used for the null set).
func discoverTTLHistogram(n *as.Node, ns, set string) (bucketWidthSec int, buckets []int64, err error) {
	cmd := "histogram:namespace=" + ns + ";type=ttl"
	if set != NullSet {
		cmd = "histogram:namespace=" + ns + ";set=" + set + ";type=ttl"
	}
	raw, err := reqInfo(n, cmd)
	if err != nil {
		return 0, nil, err
	}
	return parseTTLHistogram(raw)
}

// runDiscovery performs one full discovery pass: enumerate namespaces and sets,
// read each set's default-ttl and ttl histogram, build the effective set list
// (applying defaults + per-set overrides), reconcile the collector registry,
// and atomically publish the list for the scan runner. It is the scheduled
// discovery tick and is also run once synchronously at startup.
func runDiscovery() {
	if c := client.Load(); c == nil || !c.IsConnected() {
		if e := aeroInit(); e != nil {
			logrus.Error("Discovery: aeroInit failed:", e)
			return
		}
	}
	n := getLocalNode()
	if n == nil {
		logrus.Error("Discovery: did not find local node, skipping pass")
		return
	}

	namespaces, err := discoverNamespaces(n)
	if err != nil {
		logrus.Error("Discovery: failed to list namespaces:", err)
		return
	}

	var sets []effectiveSet
	for _, ns := range namespaces {
		nsSets, err := discoverNamespaceSets(n, ns)
		if err != nil {
			logrus.Errorf("Discovery: pass aborted (keeping previous registry): %v", err)
			return
		}
		sets = append(sets, nsSets...)
	}

	discoveryRegistry.reconcile(sets)
	effectiveSets.Store(sets)
	logrus.Infof("Discovery: reconciled %d set(s) across %d namespace(s)", len(sets), len(namespaces))
}

// discoverNamespaceSets builds the effective set list for one namespace: every
// named set plus the synthetic null set, each fit to its observed ttl histogram.
// Any info failure (reqInfo already retried for the full window) returns an
// error so the caller skips the whole pass and keeps the previous registry,
// rather than reconciling against a partial set list and pruning healthy sets.
func discoverNamespaceSets(n *as.Node, ns string) ([]effectiveSet, error) {
	defaultTTL, err := discoverDefaultTTL(n, ns)
	if err != nil {
		return nil, fmt.Errorf("default-ttl lookup failed for %s: %w", ns, err)
	}
	defaultTTLGauge.WithLabelValues(ns).Set(float64(defaultTTL))
	setNames, err := discoverSets(n, ns)
	if err != nil {
		return nil, fmt.Errorf("set listing failed for %s: %w", ns, err)
	}
	// Only add the synthetic null set when the namespace actually has set-less
	// records; otherwise a set="" scan would just re-cover the named sets (it is
	// namespace-wide) and double-count. The scan itself is filtered to set-less
	// records server-side (see applyScanPolicy).
	if hasSetlessRecords(n, ns, setNames) {
		setNames = append(setNames, NullSet)
	} else {
		logrus.Debugf("Discovery: %s — no set-less records, skipping null set", ns)
	}

	out := make([]effectiveSet, 0, len(setNames))
	for _, set := range setNames {
		es, err := buildSetEntry(n, ns, set, defaultTTL)
		if err != nil {
			return nil, err
		}
		out = append(out, es)
	}
	return out, nil
}

// buildSetEntry resolves one discovered ns:set into its effectiveSet: fetch the
// set's ttl histogram, overlay any monitor override on the discovery defaults,
// and resolve its buckets.
func buildSetEntry(n *as.Node, ns, set string, defaultTTL int) (effectiveSet, error) {
	width, hist, err := discoverTTLHistogram(n, ns, set)
	if err != nil {
		return effectiveSet{}, fmt.Errorf("ttl histogram failed for %s:%s: %w", ns, set, err)
	}
	ovr := findOverride(config.Monitor, ns, set)
	if ovr != nil {
		logrus.Debugf("Discovery: %s:%s — found monitor override (ttlBuckets=%+v)", ns, set, ovr.TTLBuckets)
	}
	paddingPct := config.Service.DiscoveryRangePaddingPct
	outlierPct := config.Service.DiscoveryOutlierPct
	es := buildEffectiveSet(ns, set, width, hist, defaultTTL,
		config.Service.DiscoveryDefaults, ovr, config.Service.DiscoveryBucketCount,
		paddingPct, outlierPct)
	logDiscoveredSet(ns, set, defaultTTL, width, hist, paddingPct, outlierPct, es)
	return es, nil
}

// logDiscoveredSet emits the one-line discovery summary for a ns:set, plus
// debug-level detail (when auto-fitting) about how its TTL bucket range was
// derived. set=="" is rendered as the synthetic null set.
func logDiscoveredSet(ns, set string, defaultTTL, bucketWidthSec int, hist []int64, paddingPct int, outlierPct float64, es effectiveSet) {
	label := ns + ":" + set
	if set == NullSet {
		label = ns + ":(null)"
	}
	if !es.expirable {
		logrus.Infof("Discovery: %s — non-expirable (default-ttl=0, histogram empty), skipping TTL histogram", label)
		return
	}
	lo, hi := es.buckets[0], es.buckets[len(es.buckets)-1]
	if mode := es.cfg.TTLBuckets.Mode; mode != "" && mode != "auto" {
		logrus.Infof("Discovery: %s — %s ttlBuckets: %d buckets [%.1f..%.1f] %s (from config)",
			label, mode, len(es.buckets), lo, hi, es.ttlUnit)
		return
	}
	logAutoFitDetails(label, defaultTTL, bucketWidthSec, hist, paddingPct, outlierPct)
	logrus.Infof("Discovery: %s — %d buckets [%.1f..%.1f] %s (default-ttl=%ds)",
		label, len(es.buckets), lo, hi, es.ttlUnit, defaultTTL)
}

// logAutoFitDetails debug-logs the derivation of an auto-fit TTL range: the
// populated histogram span, the padding applied, and the resulting display unit.
func logAutoFitDetails(label string, defaultTTL, bucketWidthSec int, hist []int64, paddingPct int, outlierPct float64) {
	minSec, maxSec, ok := populatedRangeSec(bucketWidthSec, hist, outlierPct)
	if !ok {
		logrus.Debugf("Discovery: %s — histogram empty, falling back to default-ttl=%ds as range [0, %d]",
			label, defaultTTL, defaultTTL)
	} else {
		logrus.Debugf("Discovery: %s — histogram bucket-width=%ds, populated range [%d, %d]s (buckets %d–%d of %d, outlierPct=%.1f%%)",
			label, bucketWidthSec, minSec, maxSec, minSec/bucketWidthSec, maxSec/bucketWidthSec, len(hist), outlierPct)
	}
	unpaddedMax := maxSec
	if !ok {
		unpaddedMax = defaultTTL
	}
	paddedMax := unpaddedMax + unpaddedMax*paddingPct/100
	logrus.Debugf("Discovery: %s — padding %d%% extends max %ds → %ds", label, paddingPct, unpaddedMax, paddedMax)
	unit, mod := pickUnit(paddedMax)
	logrus.Debugf("Discovery: %s — pickUnit(%ds) → %s (÷%d)", label, paddedMax, unit, mod)
}

// findOverride returns the monitor entry matching ns:set exactly, or nil if the
// user configured no override for that set.
func findOverride(list []monconfOverride, ns, set string) *monconfOverride {
	for i := range list {
		if list[i].Namespace == ns && list[i].Set == set {
			return &list[i]
		}
	}
	return nil
}

// buildEffectiveSet combines the observed TTL histogram fit with the merged
// scan/perf/feature config to produce the fully-resolved effectiveSet for one
// ns:set. defaults supply the base config; override (if non-nil) overlays it
// field-by-field. n is the discovery bucket count. A non-expirable set carries
// nil buckets and expirable=false so the registry skips its TTL histograms.
func buildEffectiveSet(ns, set string, bucketWidthSec int, histBuckets []int64, defaultTTL int, defaults monconf, override *monconfOverride, n, paddingPct int, outlierPct float64) effectiveSet {
	cfg := defaults
	if override != nil {
		cfg = override.resolve(defaults)
	}
	cfg.Namespace = ns
	cfg.Set = set

	buckets, unit, modifier, expirable := ttlBucketsFrom(
		cfg.TTLBuckets, bucketWidthSec, histBuckets, defaultTTL, n, paddingPct, outlierPct)

	return effectiveSet{
		namespace: ns,
		set:       set,
		buckets:   buckets,
		ttlUnit:   unit,
		modifier:  modifier,
		expirable: expirable,
		cfg:       cfg,
	}
}

// parseTTLHistogram parses the colon-delimited response of an Aerospike
// "histogram:...;type=ttl" info call, e.g.
//
//	units=seconds:hist-width=86400:bucket-width=864:buckets=0,0,5,10,0
//
// It returns the per-bucket width in seconds and the per-bucket record counts.
// Field order is not assumed. An error is returned if bucket-width or buckets
// are absent or malformed.
func parseTTLHistogram(raw string) (bucketWidthSec int, buckets []int64, err error) {
	fields := infoKV(raw, ":")

	widthStr, ok := fields["bucket-width"]
	if !ok {
		return 0, nil, fmt.Errorf("ttl histogram missing bucket-width: %q", raw)
	}
	bucketWidthSec, err = strconv.Atoi(widthStr)
	if err != nil {
		return 0, nil, fmt.Errorf("bad bucket-width %q: %w", widthStr, err)
	}

	bucketsStr, ok := fields["buckets"]
	if !ok || bucketsStr == "" {
		return 0, nil, fmt.Errorf("ttl histogram missing buckets: %q", raw)
	}
	for _, c := range strings.Split(bucketsStr, ",") {
		n, perr := strconv.ParseInt(c, 10, 64)
		if perr != nil {
			return 0, nil, fmt.Errorf("bad bucket count %q: %w", c, perr)
		}
		buckets = append(buckets, n)
	}
	return bucketWidthSec, buckets, nil
}
