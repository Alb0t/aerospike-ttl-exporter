package main

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	as "github.com/aerospike/aerospike-client-go/v5"
	asl "github.com/aerospike/aerospike-client-go/v5/logger"

	logrus "github.com/sirupsen/logrus"
)

var client *as.Client
var scanpol = as.NewScanPolicy()
var policy = as.NewPolicy()
var infoPolicy = as.NewInfoPolicy()
var cp = as.NewClientPolicy()
var err error
var buf bytes.Buffer
var backoff = 1.0

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
	logger := log.New(&buf, "AerospikeLogger: ", log.LstdFlags|log.Lshortfile)
	logger.SetOutput(os.Stdout)
	asl.Logger.SetLogger(logger)

	if config.Service.Verbose {
		asl.Logger.SetLevel(asl.DEBUG)
	} else {
		asl.Logger.SetLevel(asl.ERR)
	}

	if client != nil && client.IsConnected() {
		logrus.Warn("Client was connected but aeroinit called. Reopening connection")
		client.Close()

	}
	// TODO: make these configurable.
	// cp.ConnectionQueueSize = 20
	// cp.MinConnectionsPerNode = 10
	cp.IdleTimeout = 55 * time.Second
	//function to define policies and connect to aerospike.
	logrus.Info("Connecting to ", config.Service.AerospikeAddr, "...")
	if config.Service.Username != "" {
		cp.User = config.Service.Username
		if config.Service.Password != "" {
			cp.Password = config.Service.Password
		}
		client, err = as.NewClientWithPolicy(cp, config.Service.AerospikeAddr, config.Service.AerospikePort)
	} else {
		client, err = as.NewClient(config.Service.AerospikeAddr, config.Service.AerospikePort)
	}

	if err != nil || !client.IsConnected() {
		logrus.Fatal("Exception while establishing connection:", err)
		return err
	}
	logrus.Info("Connected:", client.IsConnected())
	//time.Sleep(15 * time.Second)
	scanpol.IncludeBinData = false
	return nil
}

func countSet(n *as.Node, ns string, set string) int64 {
	if set != "" {
		cmd := fmt.Sprintf("sets/%s/%s", ns, set)
		objCount := getCount(n, "objects", cmd, true)
		return objCount
	} else {
		// this means we want to get the nullset which sucks.
		// we have to return the difference of master_objects-(all set objects) given a namespace.
		//
		// since null set doesn't work with sets/s/s we will have to find what is in the nullset by adding up _all_ the sets in the ns and subtracting from total master_objects.

		// get list of all sets and their objects
		cmd := fmt.Sprintf("sets/%s", ns)
		objCount := getCount(n, "objects", cmd, false)
		// objCount should contain the sum of all our sets now.

		// now we get master objects.
		cmd = fmt.Sprintf("namespace/%s", ns)
		masterObjects := getCount(n, "master_objects", cmd, true)
		nullSetCount := masterObjects - objCount
		logrus.Debug("Found masterObjects=", masterObjects, " and total set counts=", objCount, " so our null-set must be:", nullSetCount)
		return nullSetCount
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
	return count
}

func nodeWarmup(n *as.Node) {
	logrus.Debug("Warming up node..")
	warmCount, err := n.WarmUp(1)
	if err != nil {
		logrus.Fatal("Error during node warmup", err)
	}
	logrus.Debug("Warmed up connections: ", warmCount)
}

func getLocalNode() *as.Node {
	logrus.Debug("Finding local node.")
	var localNode *as.Node
	logrus.Debug("Fetching membership list..")
	nodes := client.GetNodes()
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
	logrus.Debug("Printing namespaces to monitor and their config below.")
	for _, x := range config.Monitor {
		logrus.Debugf("%+v", x)
	}
	for _, element := range config.Monitor {
		// if for some reason the scheduler calls us concurrently, just skip the new runs until the existing one is done
		// probably just paranoia.
		if running {
			logrus.Warn("Already running. Skipping.")
		}
		running = true
		// while I am splitting namespace and set for aerospike calls and metric display,
		// the metrics are stored in a map so preserving the original "ns" var
		startTime := float64(time.Now().Unix())
		err := updateStats(element.Namespace, element.Set, element.Namespace+":"+element.Set, element)
		finishTime := float64(time.Now().Unix())
		timeToUpdate := float64((finishTime - startTime) / 60)
		logrus.Info("Scan for ", element.Namespace, ":", element.Set, " took ", timeToUpdate, " minutes.")
		scanTimes.WithLabelValues(element.Namespace, element.Set).Set(timeToUpdate)

		if err != "" {
			logrus.Error("There was a problem updating the stats.", err)
		} else {
			// Only update the "aerospike_ttl_scan_last_updated" metric if the update was successful.
			scanLastUpdated.WithLabelValues(element.Namespace, element.Set).Set(finishTime)
		}
		running = false
	}
}

// simple function to take a human duration input like 1m20s and return a time.Duration output
func parseDur(dur string) time.Duration {
	parsedDur, err := time.ParseDuration(dur)
	if err != nil {
		panic(err)
	}
	return parsedDur
}

func updateStats(namespace string, set string, namespaceSet string, element monconf) string {
	logrus.Debug("Running:", running)
	if client == nil || !client.IsConnected() {
		err := aeroInit()
		if err != nil {
			return "Failure during aeroInit()."
		}
	}
	localNode := getLocalNode()
	nodeWarmup(localNode)
	if localNode == nil {
		return "Did not find self in node list"
	}

	logrus.WithFields(logrus.Fields{
		"namespace": namespace,
		"set":       set,
	}).Info("Begin scan/inspection.")
	scanpol.TotalTimeout = parseDur(element.ScanTotalTimeout)
	scanpol.SocketTimeout = parseDur(element.ScanSocketTimeout)
	scanpol.RecordsPerSecond = element.RecordsPerSecond // this will default to 0 if its not passed. that means no throttle (ahhh!!)
	// Aerospike deprecated ScanPercent because they're evil
	// so we'll do it ourselves.
	// TODO: maybe add predexp digest mod match.
	if element.ScanPercent > 0 && element.ScanPercent < 100 && element.Recordcount == -1 {
		setCount := countSet(localNode, namespace, set)
		logrus.Debug("Got setCount of:", setCount, " for localNode=", localNode, ", namespace=", namespace, ", set=", set, ".")
		sampleRecCount := int64(float64(countSet(localNode, namespace, set)) * element.ScanPercent / 100)
		if sampleRecCount < 1 {
			logrus.Error("Nonsensical record count calculated:", sampleRecCount, ". Probably a bug.. lets not do this.")
			return "Refusing to scan since we calculated a nonsense sample record count."
		}
		scanpol.MaxRecords = int64(sampleRecCount)
		logrus.Debug("Setting max records to ", sampleRecCount, " based off sample percent ", element.ScanPercent)
	} else if element.ScanPercent >= 100 {
		logrus.Warn("Setting max records to 0 to scan 100% of data, seems kinda silly so warning you..")
		scanpol.MaxRecords = 0
	} else {
		scanpol.MaxRecords = int64(element.Recordcount)
	}
	policy.TotalTimeout = parseDur(element.PolicyTotalTimeout)
	policy.SocketTimeout = parseDur(element.PolicySocketTimeout)

	recs, _ := client.ScanNode(scanpol, localNode, namespace, set)
	total := 0
	totalInspected := 0
	resultMap[namespaceSet] = make(map[uint32]int)
	for rec := range recs.Results() {
		if config.Service.Verbose {
			if total%element.ReportCount == 0 { // this is after the scan is done. may not be valuable other than for debugging.
				logrus.Info("Processed ", total, " records...")
			}
		}
		if rec.Err == nil {
			totalInspected++
			if rec.Record.Expiration == 4294967295 {
				//logrus.Debug("Found non-expirable record, not adding to total or exporting.")
				// too noisy
			} else {
				total++
				expireTime := (rec.Record.Expiration / element.ExportTypeDivision) * element.ExportBucketMultiply
				resultMap[namespaceSet][expireTime]++
			}
		} else {
			logrus.Error("Error while inspecting scan results: ", rec.Err)
			logrus.Warn("Sleeping 140s since we hit an error to allow any pending scan to clear out.")
			time.Sleep(140 * time.Second)
		}
		if element.Recordcount != -1 && total >= element.Recordcount {
			logrus.Debug("Retrieved ", total, " records. Which is >= the limit specified of ", element.Recordcount, ". Will terminate query now.")
			recs.Close() // close the record set to stop the query
			break
		}
	}

	// There might be a better way to do this, but i'm adding a reset here to clear out any buckets that aren't valuable anymore.
	expirationTTLPercents.Reset()
	expirationTTLCounts.Reset()
	for key := range resultMap[namespaceSet] {
		skey := fmt.Sprint(key) // this will be used as a label like {ttl="100"} so needs to be string.
		if element.ExportPercentages {
			percentInThisBucket := float64(resultMap[namespaceSet][key]) * float64(100) / float64(total)
			expirationTTLPercents.WithLabelValues(element.ExportType, skey, namespace, set).Set(float64(percentInThisBucket))
		}
		if element.ExportRecordCount {
			expirationTTLCounts.WithLabelValues(element.ExportType, skey, namespace, set).Set(float64(resultMap[namespaceSet][key]))
		}
	}
	if element.ExportPercentages {
		expirationTTLPercents.WithLabelValues("totalScanned", "total", namespace, set).Set(float64(total))
	}
	if element.ExportRecordCount {
		expirationTTLCounts.WithLabelValues("totalScanned", "total", namespace, set).Set(float64(total))
	}
	logrus.WithFields(logrus.Fields{
		"total(records exported)": total,
		"totalInspected":          totalInspected,
		"namespace":               namespace,
		"set":                     set,
	}).Info("Scan complete.")
	return ""
}
