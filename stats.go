package main

import (
	"fmt"
	"net"
	"strconv"

	"strings"
	"time"

	as "github.com/aerospike/aerospike-client-go"
	log "github.com/sirupsen/logrus"
)

var client *as.Client
var scanpol *as.ScanPolicy
var policy = as.NewPolicy()
var err error

func findLocalIps() error {
	// this function is used to find the local node that the code is running on.
	// by default, this is client.getnodes[0] - but if the node stops/starts, we don't want it
	// to automatically fail over to a DIFFERENT node. That would be bad.
	// this should only be called once.
	// mostly copy pasta from stack overflow
	log.Info("Fetching local interfaces")
	ifaces, ierr := net.Interfaces()
	if ierr != nil {
		log.Error("Error while retrieving net.Interfaces:", ierr)
	}
	for _, i := range ifaces {
		log.Debug("Fetching addr for iface")
		addrs, errAd := i.Addrs()
		if errAd != nil {
			log.Error("Error while retrieving interface addresss:", errAd)
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
	log.Debug("Printing localIp map:", localIps)
	return nil
}

func aeroInit() error {
	//function to define policies and connect to aerospike.
	log.Info("Connecting to ", config.Service.NodeAddr, "...")
	client, err = as.NewClient(config.Service.NodeAddr, 3000)

	if err != nil || !client.IsConnected() {
		log.Error("Exception while establishing connection:", err)
		return err
	}
	log.Info("Connected:", client.IsConnected())
	scanpol = as.NewScanPolicy()
	scanpol.ConcurrentNodes = false
	scanpol.IncludeBinData = false
	scanpol.FailOnClusterChange = config.Service.FailOnClusterChange
	return nil
}

func countSetObjects(n *as.Node, ns, set string) int64 {
	const statKey = "objects"
	// get the list of cluster nodes
	infop := as.NewInfoPolicy()
	objCount := 0

	cmd := fmt.Sprintf("sets/%s/%s", ns, set)
	info, err := n.RequestInfo(infop, cmd)
	if err != nil {
		return -1
	}
	vals := strings.Split(info[cmd], ":")
	for _, val := range vals {
		if i := strings.Index(val, statKey); i > -1 {
			cnt, err := strconv.Atoi(val[i+len(statKey)+1:])
			if err != nil {
				return -1
			}
			objCount += cnt
			break
		}
	}

	return int64(objCount)
}

func getLocalNode() *as.Node {
	log.Debug("Finding local node.")
	var localNode *as.Node
	log.Debug("Fetching membership list..")
	nodes := client.GetNodes()
	log.Debug("Looping through active cluster nodes")
	if config.Service.SkipNodeCheck {
		return nodes[0]
	}
	for _, node := range nodes {
		// convert the node to a string, then split that to find the addr

		nodeStr := fmt.Sprint(node)
		nodeAddrStrWithPort := strings.Split(nodeStr, " ")
		if nodeAddrStrWithPort == nil || len(nodeAddrStrWithPort) != 2 {
			log.Error("Did not find expected node format in client.GetNodes")
			continue
		}
		nodeaddrStr := strings.Split(nodeAddrStrWithPort[1], ":")[0]
		log.Debug("Comparing against local ip list..")
		for localIP := range localIps {
			if localIP == nodeaddrStr {
				log.Debug("found node with matching localip ", localIP, "==", node)
				localNode = node
			}
		}
	}
	return localNode
}

func runner() {
	log.Debug("Printing namespaces to monitor and their config below.")
	for _, x := range config.Monitor {
		log.Debugf("%+v", x)
	}
	for _, element := range config.Monitor {
		// if for some reason the scheduler calls us concurrently, just skip the new runs until the existing one is done
		// probably just paranoia.
		if running {
			log.Warn("Already running. Skipping.")
		}
		running = true
		// while I am splitting namespace and set for aerospike calls and metric display,
		// the metrics are stored in a map so preserving the original "ns" var
		startTime := float64(time.Now().Unix())
		err := updateStats(element.Namespace, element.Set, element.Namespace+":"+element.Set, element)
		finishTime := float64(time.Now().Unix())
		timeToUpdate := float64((finishTime - startTime) / 60)
		log.Info("Scan for ", element.Namespace, ":", element.Set, " took ", timeToUpdate, " minutes.")
		scanTimes.WithLabelValues(element.Namespace, element.Set).Set(timeToUpdate)
		scanLastUpdated.WithLabelValues(element.Namespace, element.Set).Set(finishTime)

		if err != "" {
			log.Error("There was a problem updating the stats.", err)
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
	log.Debug("Running:", running)
	if client == nil || client.IsConnected() == false {
		err := aeroInit()
		if err != nil {
			return "Failure during aeroInit()."
		}
	}
	localNode := getLocalNode()
	if localNode == nil {
		return "Did not find self in node list"
	}

	log.WithFields(log.Fields{
		"namespace": namespace,
		"set":       set,
	}).Info("Begin scan/inspection.")
	//scanpol.ScanPercent = element.ScanPercent // deprecated
	scanpol.Priority = element.ScanPriority
	scanpol.TotalTimeout = parseDur(element.ScanTotalTimeout)
	scanpol.SocketTimeout = parseDur(element.ScanSocketTimeout)
	scanpol.RecordsPerSecond = element.RecordsPerSecond // this will default to 0 if its not passed. that means no throttle (ahhh!!)
	// Aerospike deprecated ScanPercent because they're evil
	// so we'll do it ourselves.
	// TODO: maybe add predexp digest mod match.
	if element.ScanPercent > 0 && element.Recordcount == -1 {
		sampleRecCount := float64(countSetObjects(localNode, namespace, set)) * element.ScanPercent / 100
		scanpol.MaxRecords = int64(sampleRecCount)
		log.Debug("Setting max records to ", sampleRecCount, " based off sample percent ", element.ScanPercent)
	} else if element.ScanPercent > 100 {
		log.Warn("Setting max records to 0 to scan 100% of data, seems kinda silly so warnings you..")
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
				log.Info("Processed ", total, " records...")
			}
		}
		if rec.Err == nil {
			totalInspected++
			if rec.Record.Expiration == 4294967295 {
				//log.Debug("Found non-expirable record, not adding to total or exporting.")
				// too noisy
			} else {
				total++
				expireTime := (rec.Record.Expiration / element.ExportTypeDivision) * element.ExportBucketMultiply
				resultMap[namespaceSet][expireTime]++
			}
		} else {
			log.Error("Error while inspecting scan results: ", rec.Err)
			log.Warn("Sleeping 140s since we hit an error to allow any pending scan to clear out.")
			time.Sleep(140 * time.Second)
		}
		if element.Recordcount != -1 && total >= element.Recordcount {
			log.Debug("Retrieved ", total, " records. Which is >= the limit specified of ", element.Recordcount, ". Will terminate query now.")
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
	log.WithFields(log.Fields{
		"total(records exported)": total,
		"totalInspected":          totalInspected,
		"namespace":               namespace,
		"set":                     set,
	}).Info("Scan complete.")
	return ""
}
