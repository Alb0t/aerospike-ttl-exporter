package main

import (
	"flag"
	"io/ioutil"
	"os"
	"strconv"
	"strings"

	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

var buildVersion = "4.0.7"
var configFile = flag.String("configFile", "/etc/ttl-aerospike-exporter.yaml", "The yaml config file for the exporter")
var ns_set_to_histograms = make(map[string]map[string]*prometheus.HistogramVec)
var ns_set_to_ttl_unit = make(map[string]map[string]int)

var buildInfo = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: "aerospike_ttl",
		Name:      "build_info",
		Help:      "Build info",
	},
	[]string{"version"},
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

// these are global because im lazy
var running = false                  // bool to track whether a scan is running already or not.
var localIps = make(map[string]bool) // map to prevent duplicates, and a list of what our local ips are
var config conf

type conf struct {
	Service serviceConf
	Monitor []monconf
}

type serviceConf struct {
	ListenPort          string `yaml:"listenPort"`
	SkipNodeCheck       bool   `yaml:"skipNodeCheck"`
	FailOnClusterChange bool   `yaml:"FailOnClusterChange"`
	FrequencySecs       int    `yaml:"frequencySecs"`
	Verbose             bool   `yaml:"verbose"`
	Username            string `yaml:"username"`
	Password            string `yaml:"password"`
	AerospikeAddr       string `yaml:"aerospikeAddr"`
	AerospikePort       int    `yaml:"aerospikePort"`
}

type monconf struct {
	Namespace                string          `yaml:"namespace"`
	Set                      string          `yaml:"set"`
	Recordcount              int             `yaml:"recordCount,omitempty"`
	ScanPercent              float64         `yaml:"scanPercent,omitempty"`
	NumberOfBucketsToExport  int             `yaml:"numberOfBucketsToExport,omitempty"`
	BucketWidth              string          `yaml:"bucketWidth,omitempty"`
	BucketStart              string          `yaml:"bucketStart,omitempty"`
	StaticBucketList         []string        `yaml:"staticBucketList,omitempty"`
	ReportCount              int             `yaml:"reportCount,omitempty"`
	ScanTotalTimeout         string          `yaml:"scanTotalTimeout"`
	ScanSocketTimeout        string          `yaml:"scanSocketTimeout"`
	PolicyTotalTimeout       string          `yaml:"policyTotalTimeout"`
	PolicySocketTimeout      string          `yaml:"policySocketTimeout"`
	RecordsPerSecond         int             `yaml:"recordsPerSecond"`
	KByteHistogram           map[string]bool `yaml:"kbyteHistogram,omitempty"`
	KByteHistogramResolution float64         `yaml:"kbyteHistogramResolution,omitempty"`
}

func (c *conf) setConf() {
	flag.Parse()
	yamlFile, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatal("Failed to read configfile: ", *configFile)
	}
	err = yaml.Unmarshal(yamlFile, c) // This actually writes it back to *conf
	if err != nil {
		log.Fatal("Failed to unmarshal configfile, bad format? File:", *configFile)
	}
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

func init() {
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

	// We need to define a histogram for each monconf (ns/set/buckets)
	for histogramConfIndex := range config.Monitor {
		histogramConf := config.Monitor[histogramConfIndex]
		namespace := histogramConf.Namespace
		set := histogramConf.Set
		number_of_buckets := histogramConf.NumberOfBucketsToExport

		var buckets []float64
		var unit_modifier int
		var ttl_unit string
		// buckets definitions
		if len(histogramConf.StaticBucketList) > 0 {
			if number_of_buckets != 0 || histogramConf.BucketWidth != "" { // cant check that bucket_start is not 0 because thats a reasonable start value.
				log.Fatalf("Static list of buckets chosen for %s.%s but bucket count or bucket width defined.", namespace, set)
			}

			// drop "d", "s", "h"
			buckets, ttl_unit, unit_modifier = parseTimeValues(histogramConf.StaticBucketList)
		} else {
			var start_and_width []float64
			start_and_width, ttl_unit, unit_modifier = parseTimeValues([]string{histogramConf.BucketStart, histogramConf.BucketWidth})
			bucket_start := start_and_width[0]
			bucket_width := start_and_width[1]
			buckets = prometheus.LinearBuckets(bucket_start, bucket_width, number_of_buckets)
		}

		histograms := make(map[string]*prometheus.HistogramVec)
		ttl_units := make(map[string]int)

		if histogramConf.KByteHistogram["deviceSize"] || histogramConf.KByteHistogram["memorySize"] {
			expirationTTLBytesHist := prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace:   "aerospike_ttl",
					Name:        "kib_hist",
					Help:        "Histogram of how many bytes fall into each ttl bucket. Memory will be the in-memory data size and does not include PI or SI.",
					Buckets:     buckets,
					ConstLabels: prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttl_unit},
				}, []string{"storage_type"},
			)
			prometheus.MustRegister(expirationTTLBytesHist)
			histograms["bytes"] = expirationTTLBytesHist
			ttl_units["modifier"] = unit_modifier
		}

		if true {
			expirationTTLCountsHist := prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace:   "aerospike_ttl",
					Name:        "counts_hist",
					Help:        "Histogram of how many records fall into each ttl bucket.",
					Buckets:     buckets,
					ConstLabels: prometheus.Labels{"namespace": namespace, "set": set, "ttlUnit": ttl_unit},
				}, []string{},
			)
			prometheus.MustRegister(expirationTTLCountsHist)
			histograms["counts"] = expirationTTLCountsHist
			ttl_units["modifier"] = unit_modifier
		}

		// Add the HistogramVec to the inner map
		ns_set_to_histograms[namespace+":"+set] = histograms
		ns_set_to_ttl_unit[namespace+":"+set] = ttl_units

		//now we can call something like ns_set_to_histograms[mynamespace_myset].Observe in the future.
	}
	prometheus.MustRegister(scanTimes)
	prometheus.MustRegister(scanLastUpdated)
	prometheus.MustRegister(buildInfo)
	buildInfo.WithLabelValues(buildVersion).Set(1)

	// create a list of local ips to compare against and ensure we are checking the local node only
	// this should only need to happen once
	err := findLocalIps()
	if err != nil {
		log.Error("Exception in findLocalIps:", err)
	}

	// create client connection and setup policy
	aeroInit()

	if config.Service.Verbose {
		log.Info("Starting scheduler..")
	}
	// start process to start polling for stats
	scheduler.Every(config.Service.FrequencySecs).Seconds().Run(runner)
}
