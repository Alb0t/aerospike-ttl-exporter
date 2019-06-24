package main

import (
	"flag"
	"fmt"
	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	"os"
)

var (
	expirationTTL = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "aerospike_expirationTTL",
			Help: "Days in which this many records will expire. Sampled locally.",
		},
		[]string{"days", "namespace"},
	)
)

var (
	listenPort          = flag.String("listen", ":9146", "listen address for prometheus")
	nodeAddr            = flag.String("node", "127.0.0.1", "aerospike node")
	namespace           = flag.String("namespace", "", "namespace to scan records from.")
	set                 = flag.String("set", "", "set to scan.")
	scanPercent         = flag.Int("scanPercent", 1, "how much data to scan?")
	failOnClusterChange = flag.Bool("failOnClusterChange", false, "should we abort the scan on cluster change?")
	reportCount         = flag.Int("reportcount", 100000, "How many records should be report on? Every <x> records will cause an entry in the stdout")
	frequencySecs       = flag.Int("frequencySecs", 300, "how often to run the scan to report data (seconds)?")
	recordQueueSize     = flag.Int("recordQueueSize", 50, "Number of records to place in queue before blocking.")
	verbose             = flag.Bool("verbose", false, "Print more stuff.")
	localIPOverride     = flag.String("localIPOverride", "", "FOR TESTING ONLYYY!!!!!!!")
)

var running = false
var localIps = make(map[string]bool)
var results = make(map[uint32]int)

func init() {

	// Metrics have to be registered to be exposed:
	prometheus.MustRegister(expirationTTL)

	// parse flags here instead of main because this gets called FIRST
	flag.Parse()
	if *verbose {
		fmt.Println("Verbose!!")
		fmt.Println("Printing cmdline args/defaults:",
			"\n\t-listenPort=", *listenPort,
			"\n\t-nodeAddr=", *nodeAddr,
			"\n\t-namespace=", *namespace,
			"\n\t-set=", *set,
			"\n\t-scanPercent=", *scanPercent,
			"\n\t-failOnClusterChange=", *failOnClusterChange,
			"\n\t-reportCount=", *reportCount,
			"\n\t-frequencySecs=", *frequencySecs,
			"\n\t-recordQueueSize=", *recordQueueSize,
			"\n\t-verbose=", *verbose,
			"\n\t-localIPOverride", *localIPOverride,
		)
	}
	if *namespace == "" {
		fmt.Println("Must specify a namespace to montior.")
		os.Exit(1)
	}
	if *verbose {
		fmt.Println("Calling aeroInit()")
	}
	// create client connection and setup policy
	aeroInit()

	// create a list of local ips to compare against and ensure we are checking the local node only
	// this should only need to happen once
	findLocalIps()

	if *verbose {
		fmt.Println("Starting scheduler..")
	}
	// start process to start polling for stats
	//scheduler.Every(*frequencyMins).Minutes().Run(updateStats)
	scheduler.Every(*frequencySecs).Seconds().Run(runner)
}
