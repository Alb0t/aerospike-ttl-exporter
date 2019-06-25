package main

import (
	"flag"
	"fmt"
	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"os"
	"strings"
)

var (
	expirationTTL = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "aerospike_expirationTTL",
			Help: "Days in which this many records will expire. Sampled locally.",
		},
		[]string{"days", "namespace", "set"},
	)
)

var (
	listenPort          = flag.String("listen", ":9146", "listen address for prometheus")
	nodeAddr            = flag.String("node", "127.0.0.1", "aerospike node")
	namespaceSets       = flag.String("namespaceSets", "", "namespace:set comma delimited. Ex: myns:myset,myns2:myset3,myns3:,myns4 - set optional, but colon is not")
	recordCount         = flag.Int("recordCount", 3000000, "How many records to stop scanning at?")
	failOnClusterChange = flag.Bool("failOnClusterChange", false, "should we abort the scan on cluster change?")
	reportCount         = flag.Int("reportCount", 100000, "How many records should be report on? Every <x> records will cause an entry in the stdout")
	frequencySecs       = flag.Int("frequencySecs", 300, "how often to run the scan to report data (seconds)?")
	recordQueueSize     = flag.Int("recordQueueSize", 50, "Number of records to place in queue before blocking.")
	verbose             = flag.Bool("verbose", false, "Print more stuff.")
)

// these are global because im lazy
var running = false                             // bool to track whether a scan is running already or not.
var localIps = make(map[string]bool)            // map to prevent duplicates, and a list of what our local ips are
var namespaceSetsMap = make(map[string]bool)    // map to prevent duplicates, list of namespace/sets to monitor
var resultMap = make(map[string]map[uint32]int) // map of namespace:set -> { ttl, count } stored globally so we can report 0 on unseen metrics if the server suddenly doesn't have any

func init() {

	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(expirationTTL)

	// parse flags here instead of main because this gets called FIRST
	flag.Parse()

	fmt.Println("Printing cmdline args/defaults:",
		"\n\t-listenPort=", *listenPort,
		"\n\t-nodeAddr=", *nodeAddr,
		"\n\t-namespaceSets=", *namespaceSets,
		"\n\t-recordCount=", *recordCount,
		"\n\t-failOnClusterChange=", *failOnClusterChange,
		"\n\t-reportCount=", *reportCount,
		"\n\t-frequencySecs=", *frequencySecs,
		"\n\t-recordQueueSize=", *recordQueueSize,
		"\n\t-verbose=", *verbose,
	)
	if *namespaceSets == "" {
		fmt.Println("Must specify a namespace to montior.")
		os.Exit(1)
	} else {
		// transform a string like "ns1:set1,ns2:set2,ns3:,ns4:set1" into a map
		namespaceSetsArr := strings.Split(*namespaceSets, ",")
		for namespaceSet := range namespaceSetsArr {
			if namespaceSetsArr[namespaceSet] == "" { // handle trailing comma
				continue
			}
			resultMap[namespaceSetsArr[namespaceSet]] = map[uint32]int{}
			// string should be ns:set
			namespaceSetsMap[namespaceSetsArr[namespaceSet]] = true
		}
	}
	if *verbose {
		fmt.Println("Calling aeroInit()")
	}

	// create client connection and setup policy
	aeroInit()

	// create a list of local ips to compare against and ensure we are checking the local node only
	// this should only need to happen once
	err := findLocalIps()
	if err != nil {
		fmt.Println("Exception in findLocalIps:", err)
	}

	if *verbose {
		fmt.Println("Starting scheduler..")
	}
	// start process to start polling for stats
	//scheduler.Every(*frequencyMins).Minutes().Run(updateStats)
	scheduler.Every(*frequencySecs).Seconds().Run(runner)
}
