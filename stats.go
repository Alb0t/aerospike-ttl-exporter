package main

import (
	"fmt"
	as "github.com/aerospike/aerospike-client-go"
	"net"
	"strings"
)

var client *as.Client
var scanpol *as.ScanPolicy
var err error

func verbLog(str string) {
	if *verbose {
		fmt.Println("[VERBOSE]:", str)
	}
}

func findLocalIps() error {
	verbLog("Fetching local interfaces")
	ifaces, ierr := net.Interfaces()
	if ierr != nil {
		fmt.Println("Error while retrieving net.Interfaces:", ierr)
	}
	for _, i := range ifaces {
		verbLog("Fetching addr for iface")
		addrs, errAd := i.Addrs()
		if errAd != nil {
			fmt.Println("Error while retrieving interface addresss:", errAd)
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
	if localIPOverride != nil {
		localIps[*localIPOverride] = true
	}
	return nil
}

func aeroInit() error {
	verbLog(fmt.Sprint("Connecting to ", *nodeAddr, "..."))
	client, err = as.NewClient(*nodeAddr, 3000)

	if err != nil || !client.IsConnected() {
		fmt.Println("Exception while establishing connection", err)
		return err
	}
	verbLog(fmt.Sprint("Connected:", client.IsConnected()))
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
	verbLog("Finding local node.")
	var localNode *as.Node
	verbLog("Fetching membership list..")
	nodes := client.GetNodes()
	verbLog("Looping through active cluster nodes")
	for _, node := range nodes {
		// convert the node to a string, then split that to find the addr

		nodeStr := fmt.Sprint(node)
		nodeAddrStrWithPort := strings.Split(nodeStr, " ")
		if nodeAddrStrWithPort == nil || len(nodeAddrStrWithPort) != 2 {
			fmt.Println("Did not find expected node format in client.GetNodes")
			continue
		}
		nodeaddrStr := strings.Split(nodeAddrStrWithPort[1], ":")[0]
		verbLog("Comparing against local ip list..")
		for localIP := range localIps {
			if localIP == nodeaddrStr {
				verbLog(fmt.Sprint("found node with matching localip ", localIP, "==", node))
				localNode = node
			}
		}
	}
	return localNode
}
func runner() {
	res := updateStats()
	if res != "" {
		fmt.Println("There was a problem updating the stats.", res)
	}
}

func updateStats() string {
	verbLog(fmt.Sprint("Running:", running))
	if client == nil || client.IsConnected() == false {
		err := aeroInit()
		if err != nil {
			running = false
			return "Failure during aeroInit()."
		}
	}
	if running {
		fmt.Println("Already running. Skipping.")
		return "Already running."
	}
	localNode := getLocalNode()
	if localNode == nil {
		return "Did not find self in node list"
	}
	verbLog("Beginning scan job..")
	running = true
	recs, _ := client.ScanNode(scanpol, localNode, *namespace, *set)
	total := 0

	for rec := range recs.Results() {
		if *verbose {
			if total%*reportCount == 0 {
				fmt.Println("Processed ", total, " records...")
			}
		}
		if rec.Err == nil {
			total++
			expireTimeInDays := rec.Record.Expiration / 86400.0
			results[expireTimeInDays]++
		} else {
			fmt.Println("Error while inspecting record.", rec.Err)
		}
	}
	var minBucket uint32 = 0
	for key := range results {
		var skey string
		if key == 49710 {
			skey = "unexpirable"
		} else {
			skey = fmt.Sprint(key)
		}
		if minBucket == 0 || (key < minBucket && results[key] > 0) {
			minBucket = key
		}
		expirationTTL.WithLabelValues(skey, *namespace).Set(float64(results[key]))
		results[key] = 0 //zero back out the result in case this key goes away, report 0.
	}
	expirationTTL.WithLabelValues("total", *namespace).Set(float64(total))

	// if no records were scanned, then do not report a minBucket.
	if total > 0 {
		expirationTTL.WithLabelValues("minBucket", *namespace).Set(float64(minBucket))
	} else {
		expirationTTL.DeleteLabelValues("minBucket", *namespace)
	}
	fmt.Println("End Scan, inspected", total, "records.")
	running = false
	return ""
}
