package main

import (
	"fmt"
	as "github.com/aerospike/aerospike-client-go"
	log "github.com/sirupsen/logrus"
	"net"
	"strings"
)

var client *as.Client
var scanpol *as.ScanPolicy
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
	log.Info("Connecting to ", *nodeAddr, "...")
	client, err = as.NewClient(*nodeAddr, 3000)

	if err != nil || !client.IsConnected() {
		log.Error("Exception while establishing connection:", err)
		return err
	}
	log.Info("Connected:", client.IsConnected())
	scanpol = as.NewScanPolicy()
	scanpol.ConcurrentNodes = false
	scanpol.Priority = as.LOW
	scanpol.ScanPercent = *scanPercent
	scanpol.IncludeBinData = false
	scanpol.FailOnClusterChange = *failOnClusterChange
	scanpol.RecordQueueSize = *recordQueueSize
	return nil
}

func getLocalNode() *as.Node {
	log.Debug("Finding local node.")
	var localNode *as.Node
	log.Debug("Fetching membership list..")
	nodes := client.GetNodes()
	log.Debug("Looping through active cluster nodes")
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
	log.Debug("Namespace Sets Map:", namespaceSetsMap)
	for ns := range namespaceSetsMap {
		// if for some reason the scheduler calls us concurrently, just skip the new runs until the existing one is done
		// probably just paranoia.
		if running {
			log.Warn("Already running. Skipping.")
		}
		running = true
		namespaceSet := strings.Split(ns, ":")
		var namespace, set string
		if len(namespaceSet) == 1 {
			namespace = namespaceSet[0]
			set = ""
		} else if len(namespaceSet) == 2 {
			namespace = namespaceSet[0]
			set = namespaceSet[1]
		} else {
			log.Fatal("Couldn't parse format of ", ns)
		}
		// while I am splitting namespace and set for aerospike calls and metric display,
		// the metrics are stored in a map so preserving the original "ns" var
		err := updateStats(namespace, set, ns)
		if err != "" {
			log.Error("There was a problem updating the stats.", err)
		}
		running = false
	}
}

func updateStats(namespace string, set string, namespaceSet string) string {
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
	recs, _ := client.ScanNode(scanpol, localNode, namespace, set)
	total := 0
	totalInspected := 0

	for rec := range recs.Results() {
		if *verbose {
			if total%*reportCount == 0 {
				log.Info("Processed ", total, " records...")
			}
		}
		if rec.Err == nil {
			totalInspected++
			if rec.Record.Expiration == 4294967295 {
				log.Debug("Found non-expirable record, not adding to total or exporting.")
			} else {
				total++
				expireTimeInDays := rec.Record.Expiration / 86400
				resultMap[namespaceSet][expireTimeInDays]++
			}
		} else {
			log.Error("Error while inspecting scan results: ", rec.Err)
		}
		if *recordCount != -1 && total >= *recordCount {
			log.Debug("Retrieved ", total, " records. Which is >= the limit specified of ", *recordCount, ". Will terminate query now.")
			recs.Close() // close the record set to stop the query
			break
		}
	}

	var minBucket uint32
	for key := range resultMap[namespaceSet] {
		var skey string
		if key == 49710 {
			skey = "unexpirable"
		} else {
			skey = fmt.Sprint(key)
		}
		if minBucket == 0 || (key < minBucket && resultMap[namespaceSet][key] > 0) {
			minBucket = key
		}
		expirationTTL.WithLabelValues(skey, namespace, set).Set(float64(resultMap[namespaceSet][key]))
		resultMap[namespaceSet][key] = 0 //zero back out the result in case this key goes away, report 0.
	}
	expirationTTL.WithLabelValues("total", namespace, set).Set(float64(total))

	// if no records were scanned, then do not report a minBucket.
	if total > 0 {
		expirationTTL.WithLabelValues("minBucket", namespace, set).Set(float64(minBucket))
	} else {
		expirationTTL.DeleteLabelValues("minBucket", namespace, set)
	}
	log.WithFields(log.Fields{
		"total(records exported)": total,
		"totalInspected":          totalInspected,
		"namespace":               namespace,
		"set":                     set,
	}).Info("Scan complete.")
	return ""
}
