package main

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	asl "github.com/aerospike/aerospike-client-go/v6/logger"
	as "github.com/aerospike/aerospike-client-go/v8"
	"github.com/aerospike/aerospike-client-go/v8/types"
	logrus "github.com/sirupsen/logrus"
)

// client is read concurrently by the scan and discovery schedulers and
// replaced by aeroInit on reconnect; the atomic pointer makes the unlocked
// reads safe, while aeroMu serializes the (re)connects themselves.
var client atomic.Pointer[as.Client]
var scanpol = as.NewScanPolicy()
var policy = as.NewPolicy()
var infoPolicy = as.NewInfoPolicy()
var cp = as.NewClientPolicy()
var buf bytes.Buffer
var backoff = 1.0
var measureOps []*as.Operation
var opPolicy *as.WritePolicy
var aeroMu sync.Mutex // serializes aeroInit so the two schedulers don't race to reconnect

const NON_EXPIRABLE_TTL_VALUE = 4294967295

func findLocalIps() error {
	// this function is used to find the local node that the code is running on.
	// by default, this is client.getnodes[0] - but if the node stops/starts, we don't want it
	// to automatically fail over to a DIFFERENT node. That would be bad.
	// this should only be called once.
	// mostly copy pasta from stack overflow
	logrus.Info("Fetching local interfaces")
	ifaces, ierr := net.Interfaces()
	if ierr != nil {
		logrus.Error("Error while retrieving net.Interfaces:", ierr)
	}
	for _, i := range ifaces {
		logrus.Debug("Fetching addr for iface")
		addrs, errAd := i.Addrs()
		if errAd != nil {
			logrus.Error("Error while retrieving interface addresss:", errAd)
		}
		for _, addr := range addrs {
			var ip net.IP
			switch v := addr.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			localIps[ip.String()] = true // storing this as a map in case we call twice, don't want dupes
		}
	}
	logrus.Debug("Printing localIp map:", localIps)
	return nil
}

func aeroInit() error {
	// Both the scan scheduler and the discovery scheduler can call aeroInit
	// concurrently; aeroMu serializes the (re)connects while the atomic client
	// pointer keeps concurrent readers safe. If a peer already (re)connected
	// while we waited on the lock, reuse that healthy connection rather than
	// churning it.
	aeroMu.Lock()
	defer aeroMu.Unlock()
	if c := client.Load(); c != nil && c.IsConnected() {
		return nil
	}

	logger := log.New(&buf, "AerospikeLogger: ", log.LstdFlags|log.Lshortfile)
	logger.SetOutput(os.Stdout)
	asl.Logger.SetLogger(logger)

	if config.Service.Verbose {
		asl.Logger.SetLevel(asl.DEBUG)
	} else {
		asl.Logger.SetLevel(asl.OFF)
	}
	// TODO: make these configurable.
	// cp.ConnectionQueueSize = 20
	// cp.ConnectionQueueSize = 3
	// cp.MinConnectionsPerNode = 1
	// cp.TendInterval = 3
	cp.IdleTimeout = 55 * time.Second
	//function to define policies and connect to aerospike.
	logrus.Info("Connecting to ", config.Service.AerospikeAddr, "...")
	var c *as.Client
	var err error
	if config.Service.Username != "" {
		cp.User = config.Service.Username
		if config.Service.Password != "" {
			cp.Password = config.Service.Password
		}
		c, err = as.NewClientWithPolicy(cp, config.Service.AerospikeAddr, config.Service.AerospikePort)
	} else {
		c, err = as.NewClient(config.Service.AerospikeAddr, config.Service.AerospikePort)
	}

	if err != nil || !c.IsConnected() {
		logrus.Fatal("Exception while establishing connection:", err)
		return err
	}
	client.Store(c)
	logrus.Info("Connected:", c.IsConnected())
	scanpol.IncludeBinData = false
	return nil
}

func getReplicationFactor(n *as.Node, ns string) int64 {
	cmd := fmt.Sprintf("namespace/%s", ns)
	repl := getCount(n, "replication-factor", cmd, true)
	return repl
}

func countSet(n *as.Node, ns string, set string) int64 {
	repl := getReplicationFactor(n, ns)
	logrus.Debug("Found replication factor=", repl, " for ns ", ns)
	if repl == 0 {
		logrus.Warn("RF=0? Maybe namespace is typed wrong.")
		return 0
	}
	if set != NullSet {
		cmd := fmt.Sprintf("sets/%s/%s", ns, set)
		objCount := getCount(n, "objects", cmd, true)
		return (objCount / repl)
	} else {
		// no set specified — scan covers the entire namespace (all sets + null set),
		// so use total namespace object count for percentage calculations.
		cmd := fmt.Sprintf("namespace/%s", ns)
		totalNsObjects := getCount(n, "objects", cmd, true)
		logrus.Debug("Found total objects=", totalNsObjects, " for namespace ", ns)
		return (totalNsObjects / repl)
	}
}

func infoSanityCheck(n *as.Node) {
	info, err := n.RequestInfo(infoPolicy, "status")
	if backoff < 1 {
		backoff = 1 // dont let this go to 0
	}
	if err != nil || info["status"] != "ok" {
		logrus.Error("Sanity check failed, calling aeroInit. Status reported as:", info["status"], err)
		e := aeroInit()
		if e != nil {
			logrus.Fatal("AeroInit failed:", e)
		}
		n = getLocalNode()
		backoff = backoff * 1.2
		backoffTime := time.Duration(backoff) * time.Second
		logrus.Warn("Retrying sanityCheck with backoff:", backoff)
		time.Sleep(backoffTime)
		infoSanityCheck(n) // try again... forever?
	} else {
		backoff = backoff * 0.8
	}
}

func getCount(n *as.Node, statKey string, cmd string, single bool) int64 {
	// get count of some asinfo command
	// use single=true to break on the first match found, or single=false to get sum of all matches
	// infop := as.NewInfoPolicy()
	infoSanityCheck(n)
	var count int64
	info, err := n.RequestInfo(infoPolicy, cmd)
	if err != nil {
		logrus.Error("Info request error for getCount:", err)
		return -1
	}
	vals := strings.Split(info[cmd], ";")
	for _, v := range vals {
		innerVals := strings.Split(v, ":")
		for _, val := range innerVals {
			if i := strings.Index(val, statKey); i > -1 {
				if strings.Split(val, "=")[0] == statKey {
					cnt, err := strconv.Atoi(val[i+len(statKey)+1:])
					if err != nil {
						return -1
					}
					count += int64(cnt)
					if single {
						break // early-exit if we only wanted 1 count from this
					}
				}
			}
		}
	}
	return count
}

func nodeWarmup(n *as.Node) {
	logrus.Debug("Warming up node..")
	warmCount, err := n.WarmUp(5)
	if err != nil {
		logrus.Fatal("Error during node warmup", err)
	}
	logrus.Debug("Warmed up connections: ", warmCount)
}

func getLocalNode() *as.Node {
	logrus.Debug("Finding local node.")
	var localNode *as.Node
	c := client.Load()
	if c == nil {
		return nil
	}
	logrus.Debug("Fetching membership list..")
	nodes := c.GetNodes()
	logrus.Debug("Looping through active cluster nodes")
	if config.Service.SkipNodeCheck {
		localNode = nodes[0]
	} else {
		for _, node := range nodes {
			// convert the node to a string, then split that to find the addr

			nodeStr := fmt.Sprint(node)
			nodeAddrStrWithPort := strings.Split(nodeStr, " ")
			if nodeAddrStrWithPort == nil || len(nodeAddrStrWithPort) != 2 {
				logrus.Error("Did not find expected node format in client.GetNodes")
				continue
			}
			nodeaddrStr := strings.Split(nodeAddrStrWithPort[1], ":")[0]
			logrus.Debug("Comparing against local ip list..")
			for localIP := range localIps {
				if localIP == nodeaddrStr {
					logrus.Debug("found node with matching localip ", localIP, "==", node)
					localNode = node
				}
			}
		}
	}
	return localNode
}

func runner() {
	// Guard the whole scan cycle (not each set): the scheduler can fire again
	// while a previous runner is mid-loop, and overlapping runs would race on the
	// shared global scanpol/policy mutated per set in applyScanPolicy.
	if !running.CompareAndSwap(false, true) {
		logrus.Warn("Already running. Skipping.")
		return
	}
	defer running.Store(false)
	if config.Service.AutoDiscover {
		runnerDiscovery()
		return
	}
	logrus.Debug("Printing namespaces to monitor and their config below.")
	for _, x := range config.Monitor {
		logrus.Debugf("%+v", x)
	}
	for _, ovr := range config.Monitor {
		element := ovr.resolve(monconf{})
		hs, ok := ns_set_to_histSet[nsSetKey(element.Namespace, element.Set)]
		if !ok {
			logrus.Warnf("No collectors registered for %s:%s, skipping.", element.Namespace, element.Set)
			continue
		}
		scanOne(element, hs)
	}
}

// runnerDiscovery scans the sets published by the most recent discovery pass,
// fetching each set's live collectors from the discovery registry.
func runnerDiscovery() {
	v := effectiveSets.Load()
	if v == nil {
		logrus.Warn("Discovery has not populated any sets yet, skipping scan cycle.")
		return
	}
	sets := v.([]effectiveSet)
	for _, es := range sets {
		hs, ok := discoveryRegistry.get(es.key())
		if !ok {
			logrus.Warnf("No collectors registered for %s, skipping.", es.key())
			continue
		}
		scanOne(es.cfg, hs)
	}
}

// scanOne runs (and times) a single set's scan against the supplied collectors,
// updating the scan-time and last-updated gauges. Overlap protection lives in
// the caller: runner() holds the `running` guard for the entire scan cycle.
func scanOne(element monconf, hs *histSet) {
	startTime := float64(time.Now().Unix())
	if hs.quantiles != nil {
		hs.quantiles.reset()
	}
	err := updateStats(element.Namespace, element.Set, element, hs)
	finishTime := float64(time.Now().Unix())
	timeToUpdate := float64((finishTime - startTime))
	timeToUpdateMinutes := float64(timeToUpdate / 60)
	logrus.Info("Scan for ", element.Namespace, ":", element.Set, " took ", timeToUpdateMinutes, " minutes. Reporting as:", timeToUpdate, " seconds.")
	scanTimes.WithLabelValues(element.Namespace, element.Set).Set(timeToUpdate)

	if err != "" {
		logrus.Error("There was a problem updating the stats.", err)
	} else {
		if hs.quantiles != nil {
			hs.quantiles.finalize()
		}
		scanLastUpdated.WithLabelValues(element.Namespace, element.Set).Set(finishTime)
	}
}

// this stuff is pretty static. wanted it out of the way.
func initRecSizeVars() ([]*as.Operation, *as.WritePolicy) {
	writePolicy := as.NewWritePolicy(0, 0)
	writePolicy.Expiration = as.TTLDontUpdate //dont change the TTL of a record. should result in a no-op.
	writePolicy.MaxRetries = 10
	writePolicy.SleepBetweenRetries = 334 //334ms.
	writePolicy.TotalTimeout = 0          //let socket time it out.

	// Since the only operations are deemed 'Read Op' this will be a no-op. The writePolicy is demanded by the client driver anyway.
	operations := []*as.Operation{
		as.ExpReadOp("recordsize", as.ExpRecordSize(), as.ExpReadFlagDefault),
	}
	return operations, writePolicy
}

func measureRecordSize(client *as.Client, key *as.Key, operations []*as.Operation, policy *as.WritePolicy) (int, error) {
	// Apply the expression to a record
	record, err := client.Operate(policy, key, operations...)

	if err != nil {
		aerr, ok := err.(*as.AerospikeError)
		if ok && aerr.ResultCode == types.KEY_NOT_FOUND_ERROR {
			logrus.Debug("Key not found error. Record was probably deleted or evicted/expired between scan time and metadata read time.")
			return 0, as.ErrKeyNotFound
		} else {
			logrus.Fatal(err)
		}
	}

	recordsize, rok := record.Bins["recordsize"].(int)

	if !rok {
		logrus.Error("Could not convert 'recordsize' to int")
	}

	// if config.Service.Verbose {
	// 	logrus.Debug("Found devsize: ", devsize, " converted to KiB -> ", devsize_kb)
	// 	logrus.Debug("Found memsize: ", memsize, " converted to KiB -> ", memsize_kb)
	// }

	// return it as KiB
	return recordsize, err
}

// simple function to take a human duration input like 1m20s and return a time.Duration output
func parseDur(dur string) time.Duration {
	parsedDur, err := time.ParseDuration(dur)
	if err != nil {
		panic(err)
	}
	return parsedDur
}

// scanErrMsg formats a ScanNode error into the updateStats return string ("" on
// success). A non-empty result tells scanOne to log the failure and skip the
// set's last-updated gauge. It is deliberately non-fatal: a single set's scan
// failure (e.g. a timeout on a large whole-namespace scan) must not take the
// whole exporter down, which logrus.Fatal would do.
func scanErrMsg(namespace, set string, err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("scan failed for %s:%s: %v", namespace, set, err)
}

// ttlRange tracks the lowest and highest record TTL (raw seconds) seen during a
// single set's scan, feeding the min/max TTL gauges.
type ttlRange struct {
	min, max uint32
	seen     bool
}

// observe folds one record's TTL (seconds) into the running min/max.
func (r *ttlRange) observe(ttlSec uint32) {
	if !r.seen || ttlSec < r.min {
		r.min = ttlSec
	}
	if !r.seen || ttlSec > r.max {
		r.max = ttlSec
	}
	r.seen = true
}

// publish writes the observed range to the gauges. When no expirable record was
// seen it leaves the prior values untouched, avoiding a misleading 0.
func (r *ttlRange) publish(namespace, set string) {
	if !r.seen {
		return
	}
	minTTLGauge.WithLabelValues(namespace, set).Set(float64(r.min))
	maxTTLGauge.WithLabelValues(namespace, set).Set(float64(r.max))
}

// processRecord folds one successfully-scanned record into the histograms and
// the ttl range, returning true when the record was expirable (and thus counted
// toward the exported total). Non-expirable records are skipped.
func processRecord(rec *as.Result, element monconf, hs *histSet, ttls *ttlRange) bool {
	if rec.Record.Expiration == NON_EXPIRABLE_TTL_VALUE {
		if element.SizeHistogramEnabled || hs.quantiles != nil {
			recordsize, err := measureRecordSize(client.Load(), rec.Record.Key, measureOps, opPolicy)
			if err != nil && err != as.ErrKeyNotFound {
				logrus.Errorf("Failure fetching record size. Err: %v", err)
			}
			if recordsize != 0 {
				if element.SizeHistogramEnabled && hs.sizes != nil {
					hs.sizes.WithLabelValues("recordsize").Observe(float64(recordsize))
				}
				if hs.quantiles != nil {
					hs.quantiles.observeSize(float64(recordsize))
				}
			}
		}
		return false
	}
	ttls.observe(rec.Record.Expiration)
	modifier := hs.modifier
	if modifier < 1 {
		modifier = 1 // guard div-by-zero; expirable sets always set this
	}
	expireTime := float64(rec.Record.Expiration) / float64(modifier)
	if hs.counts != nil {
		hs.counts.WithLabelValues().Observe(expireTime)
	}
	if hs.quantiles != nil {
		hs.quantiles.observeTTL(expireTime)
	}
	observeRecordSize(rec, element, hs, expireTime)
	return true
}

// observeRecordSize reads the record's size (a metadata-only, read-only Operate)
// and feeds the kib/size histograms when enabled. The read is skipped entirely
// when neither histogram is enabled, avoiding a per-record server round-trip.
func observeRecordSize(rec *as.Result, element monconf, hs *histSet, expireTime float64) {
	if !element.KByteHistogramEnabled && !element.SizeHistogramEnabled && hs.quantiles == nil {
		return
	}
	// no-op "Operation"/"Expression" returning metadata only; should not incur IO
	// expense and does not mutate the record (opPolicy carries TTLDontUpdate).
	recordsize, err := measureRecordSize(client.Load(), rec.Record.Key, measureOps, opPolicy)
	if err != nil && err != as.ErrKeyNotFound { // key-not-found is debug-logged earlier and non-fatal.
		logrus.Errorf("Failure fetching record size. Err: %v", err)
	}
	if element.KByteHistogramEnabled && hs.bytes != nil {
		hs.bytes.addWeight(expireTime, recordsize)
	}
	if element.SizeHistogramEnabled && hs.sizes != nil && recordsize != 0 {
		hs.sizes.WithLabelValues("recordsize").Observe(float64(recordsize))
	}
	if hs.quantiles != nil && recordsize != 0 {
		hs.quantiles.observeSize(float64(recordsize))
	}
}

// applyScanPolicy configures the shared scan/read policies for one set's scan:
// timeouts, throttle, and the ScanPercent → MaxRecords sampling. Aerospike
// dropped native ScanPercent, so we approximate it with a record cap.
func applyScanPolicy(element monconf, localNode *as.Node, namespace, set string) {
	scanpol.TotalTimeout = parseDur(element.ScanTotalTimeout)
	scanpol.SocketTimeout = parseDur(element.ScanSocketTimeout)
	scanpol.RecordsPerSecond = element.RecordsPerSecond // 0 => no throttle (ahhh!!)
	switch {
	case element.ScanPercent > 0 && element.ScanPercent < 100:
		scanpol.MaxRecords = sampledMaxRecords(element, localNode, namespace, set)
	case element.ScanPercent >= 100:
		logrus.Warn("Setting max records to 0 to scan 100% of data, seems kinda silly so warning you..")
		scanpol.MaxRecords = 0
	default:
		scanpol.MaxRecords = int64(element.Recordcount)
	}
	policy.TotalTimeout = parseDur(element.PolicyTotalTimeout)
	policy.SocketTimeout = parseDur(element.PolicySocketTimeout)
	// The synthetic null set must count ONLY set-less records. A bare set=""
	// scan is namespace-wide, so filter server-side to records whose set name
	// is empty. scanpol is a shared global reused across sets, so clear the
	// filter for named sets or it would leak onto the next scan in the loop.
	if set == NullSet {
		scanpol.FilterExpression = as.ExpEq(as.ExpSetName(), as.ExpStringVal(""))
	} else {
		scanpol.FilterExpression = nil
	}
}

// sampledMaxRecords computes the MaxRecords cap approximating ScanPercent
// sampling for a set, falling back to 100 when the computed sample is < 1.
func sampledMaxRecords(element monconf, localNode *as.Node, namespace, set string) int64 {
	setCount := countSet(localNode, namespace, set)
	logrus.Debug("Got setCount of:", setCount, " for localNode=", localNode, ", namespace=", namespace, ", set=", set, ".")
	sampleRecCount := int64(float64(setCount) * element.ScanPercent / 100)
	if sampleRecCount < 1 {
		logrus.Warn("Nonsensical record count calculated:", sampleRecCount, ". Defaulting to 100 records.")
		sampleRecCount = 100
	}
	logrus.Debug("Setting max records to ", sampleRecCount, " based off sample percent ", element.ScanPercent)
	return sampleRecCount
}

// ensureNode (re)connects the client if needed and returns the local node. The
// returned string is a non-empty updateStats error message when no usable local
// node is available; an empty string means localNode is set.
func ensureNode() (*as.Node, string) {
	if c := client.Load(); c == nil || !c.IsConnected() {
		if err := aeroInit(); err != nil {
			return nil, "Failure during aeroInit()."
		}
	}
	localNode := getLocalNode()
	nodeWarmup(localNode)
	if localNode == nil {
		return nil, "Did not find self in node list"
	}
	return localNode, ""
}

func updateStats(namespace string, set string, element monconf, hs *histSet) string {
	logrus.Debug("Running:", running.Load())
	localNode, msg := ensureNode()
	if msg != "" {
		return msg
	}

	logrus.WithFields(logrus.Fields{
		"namespace": namespace,
		"set":       set,
	}).Info("Begin scan/inspection.")
	applyScanPolicy(element, localNode, namespace, set)

	recs, err := client.Load().ScanNode(scanpol, localNode, namespace, scanSet(set))
	var ttls ttlRange

	if msg := scanErrMsg(namespace, set, err); msg != "" {
		logrus.Error(msg)
		return msg
	}

	// measureRecordSize is needed by both the kib and size histograms; initialize
	// its read-only (TTLDontUpdate) policy once when either is enabled so the
	// metadata read never falls back to the client's default write policy.
	if element.KByteHistogramEnabled || element.SizeHistogramEnabled || hs.quantiles != nil {
		measureOps, opPolicy = initRecSizeVars()
	}
	counts := drainScan(recs, element, hs, &ttls)
	ttls.publish(namespace, set)
	logrus.WithFields(logrus.Fields{
		"total(records exported)": counts.exported,
		"totalInspected":          counts.inspected,
		"namespace":               namespace,
		"set":                     set,
	}).Info("Scan complete.")
	return ""
}

// scanCounts holds the per-scan tallies returned by drainScan.
type scanCounts struct {
	exported  int // records counted into the histograms
	inspected int // records pulled from the scan (incl. non-expirable)
}

// countRecord folds one successfully-scanned record into the tallies: it always
// counts toward inspected, and toward exported when the record was expirable.
func (c *scanCounts) countRecord(rec *as.Result, element monconf, hs *histSet, ttls *ttlRange) {
	c.inspected++
	if processRecord(rec, element, hs, ttls) {
		c.exported++
	}
}

// drainScan iterates one node's scan result set, feeding each record through
// processRecord, and stops early once the configured Recordcount cap is hit.
func drainScan(recs *as.Recordset, element monconf, hs *histSet, ttls *ttlRange) scanCounts {
	var c scanCounts
	for rec := range recs.Results() {
		if config.Service.Verbose && element.ReportCount > 0 && c.exported%element.ReportCount == 0 {
			logrus.Info("Processed ", c.exported, " records...")
		}
		if rec.Err != nil {
			logrus.Error("Error while inspecting scan results: ", rec.Err)
			logrus.Warn("Sleeping 140s since we hit an error to allow any pending scan to clear out.")
			time.Sleep(140 * time.Second)
		} else {
			c.countRecord(rec, element, hs, ttls)
		}
		if element.Recordcount != -1 && c.exported >= element.Recordcount {
			logrus.Debug("Retrieved ", c.exported, " records. Which is >= the limit specified of ", element.Recordcount, ". Will terminate query now.")
			recs.Close() // close the record set to stop the query
			break
		}
	}
	return c
}
