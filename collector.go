package main

import (
	"flag"
	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"os"
	"strings"
)

var buildVersion = "0.1.24"

var (
	listenPort           = flag.String("listenPort", ":9634", "listen address for prometheus")
	skipNodeCheck        = flag.Bool("skipNodeCheck", false, "Used only for testing local code development. Do not use in production.")
	nodeAddr             = flag.String("nodeAddr", "127.0.0.1", "aerospike node")
	namespaceSets        = flag.String("namespaceSets", "", "namespace:set comma delimited. Ex: 'myns:myset,myns2:myset3,myns3:,myns4:'- set optional, but colon is not")
	failOnClusterChange  = flag.Bool("failOnClusterChange", false, "should we abort the scan on cluster change?")
	reportCount          = flag.Int("reportCount", 300000, "How many records should be report on? Every <x> records will cause an entry in the stdout")
	frequencySecs        = flag.Int("frequencySecs", 300, "how often to run the scan to report data (seconds)?")
	recordQueueSize      = flag.Int("recordQueueSize", 50, "Number of records to place in queue before blocking.")
	verbose              = flag.Bool("verbose", false, "Print more stuff.")
	recordCount          = flag.Int("recordCount", 3000000, "How many records to stop scanning at? Will stop at recordCount or scanPercent, whichever is less. Pass '-recordCount=-1' to only use scanPercent.")
	scanPercent          = flag.Int("scanPercent", 1, "What percentage of data to scan? Will stop at recordCount or scanPercent, whichever is less.")
	exportPercentages    = flag.Bool("exportPercentages", true, "Export percentage distribution per bucket out of total.")
	exportRecordCount    = flag.Bool("exportRecordCount", false, "Export record count per bucket.")
	exportType           = flag.String("exportType", "days", "What label should we give the bucket")
	exportTypeDivision   = flag.Int("exportTypeDivision", 86400, "What should we divide by the seconds to get the bucket size?")
	exportBucketMultiply = flag.Int("exportBucketMultiply", 1, "Multiply the bucket value by this before exporting")
	minPercent           = flag.Float64("minPercent", 0.00001, "Minimum percentage which bucket must be considered for minBucket. Set to 100 to only use minCount.")
	minCount             = flag.Int("minCount", 50, "Minimum count which bucket must have to be considered for minBucket. Set to 0 to only use minPercent.")
)

var expirationTTLCounts *prometheus.GaugeVec
var expirationTTLPercents *prometheus.GaugeVec

var buildInfo = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "aerospike_ttl_build_info",
		Help: "Build info",
	},
	[]string{"version"},
)

// these are global because im lazy
var running = false                             // bool to track whether a scan is running already or not.
var localIps = make(map[string]bool)            // map to prevent duplicates, and a list of what our local ips are
var namespaceSetsMap = make(map[string]bool)    // map to prevent duplicates, list of namespace/sets to monitor
var resultMap = make(map[string]map[uint32]int) // map of namespace:set -> { ttl, count } stored globally so we can report 0 on unseen metrics if the server suddenly doesn't have any

func init() {
	flag.Parse()
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetOutput(os.Stdout)

	if *verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	expirationTTLPercents = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "aerospike_ttl_percents",
			Help: "Time in which this many records will expire. Sampled locally. Shows percentages of how many records were found in each bucket vs total records scanned.",
		},
		[]string{*exportType, "namespace", "set"},
	)

	expirationTTLCounts = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "aerospike_ttl_counts",
			Help: "Time in which this many records will expire. Sampled locally. Shows counts of how many records were found in each bucket.",
		},
		[]string{*exportType, "namespace", "set"},
	)
	prometheus.MustRegister(buildInfo)
	buildInfo.WithLabelValues(buildVersion).Set(1)

	if *exportPercentages {
		prometheus.MustRegister(expirationTTLPercents)
	}
	if *exportRecordCount {
		prometheus.MustRegister(expirationTTLCounts)
	}
	if *exportPercentages == false && *exportRecordCount == false {
		log.Fatal("Must export at least 1 metric, choose from percentage or record count - or both. Try -h for help.")
	}

	log.WithFields(log.Fields{
		"-listenPort":           *listenPort,
		"-nodeAddr":             *nodeAddr,
		"-namespaceSets":        *namespaceSets,
		"-recordCount":          *recordCount,
		"-failOnClusterChange":  *failOnClusterChange,
		"-reportCount":          *reportCount,
		"-frequencySecs":        *frequencySecs,
		"-recordQueueSize":      *recordQueueSize,
		"-verbose":              *verbose,
		"-scanPercent":          *scanPercent,
		"-exportPercentages":    *exportPercentages,
		"-exportRecordCount":    *exportRecordCount,
		"-exportType":           *exportType,
		"-exportTypeDivision":   *exportTypeDivision,
		"-exportBucketMultiply": *exportBucketMultiply,
		"-skipNodeCheck:":       *skipNodeCheck,
		"-minPercent":           *minPercent,
		"-minCount":             *minCount,
	}).Info("Showing passable parameters and their current values.")

	if *namespaceSets == "" {
		log.Fatal("Must specify a namespace to montior with '-namespaceSets'. Try -h for help")
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

	// create a list of local ips to compare against and ensure we are checking the local node only
	// this should only need to happen once
	err := findLocalIps()
	if err != nil {
		log.Error("Exception in findLocalIps:", err)
	}

	// create client connection and setup policy
	aeroInit()

	if *verbose {
		log.Info("Starting scheduler..")
	}
	// start process to start polling for stats
	//scheduler.Every(*frequencyMins).Minutes().Run(updateStats)
	scheduler.Every(*frequencySecs).Seconds().Run(runner)
}
