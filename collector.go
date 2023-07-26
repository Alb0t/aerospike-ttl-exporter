package main

import (
	"flag"
	"io/ioutil"
	"os"

	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

var buildVersion = "3.1.0"
var configFile = flag.String("configFile", "/etc/ttl-aerospike-exporter.yaml", "The yaml config file for the exporter")
var ns_set_to_histograms = make(map[string]map[string]*prometheus.HistogramVec)

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
	Namespace               string          `yaml:"namespace"`
	Set                     string          `yaml:"set"`
	Recordcount             int             `yaml:"recordCount,omitempty"`
	ScanPercent             float64         `yaml:"scanPercent,omitempty"`
	NumberOfBucketsToExport int             `yaml:"numberOfBucketsToExport,omitempty"`
	BucketWidth             int             `yaml:"bucketWidth,omitempty"`
	BucketStart             int             `yaml:"bucketStart,omitempty"`
	StaticBucketList        []float64       `yaml:"staticBucketList,omitempty"`
	ReportCount             int             `yaml:"reportCount,omitempty"`
	ScanTotalTimeout        string          `yaml:"scanTotalTimeout"`
	ScanSocketTimeout       string          `yaml:"scanSocketTimeout"`
	PolicyTotalTimeout      string          `yaml:"policyTotalTimeout"`
	PolicySocketTimeout     string          `yaml:"policySocketTimeout"`
	RecordsPerSecond        int             `yaml:"recordsPerSecond"`
	ByteHistogram           map[string]bool `yaml:"byteHistogram,omitempty"`
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
		var buckets []float64
		number_of_buckets := histogramConf.NumberOfBucketsToExport
		bucket_width := float64(histogramConf.BucketWidth)
		bucket_start := float64(histogramConf.BucketStart)

		// buckets definitions
		if len(histogramConf.StaticBucketList) > 0 {
			if number_of_buckets != 0 || bucket_width != 0 { // cant check that bucket_start is not 0 because thats a reasonable start value.
				log.Fatalf("Static list of buckets chosen for %s.%s but bucket count or bucket width defined.", namespace, set)
			}
			// should be using static buckets if we are still here.
			buckets = histogramConf.StaticBucketList
		} else {
			buckets = prometheus.LinearBuckets(bucket_start, bucket_width, number_of_buckets)
		}

		histograms := make(map[string]*prometheus.HistogramVec)

		if histogramConf.ByteHistogram["deviceSize"] || histogramConf.ByteHistogram["memorySize"] {
			expirationTTLBytesHist := prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace:   "aerospike_ttl",
					Name:        "bytes_hist",
					Help:        "Histogram of how many bytes fall into each ttl bucket. Memory will be the in-memory data size and does not include PI or SI.",
					Buckets:     buckets,
					ConstLabels: prometheus.Labels{"namespace": namespace, "set": set},
				}, []string{"storage_type"},
			)
			prometheus.MustRegister(expirationTTLBytesHist)
			histograms["bytes"] = expirationTTLBytesHist
		}

		if true {
			expirationTTLCountsHist := prometheus.NewHistogramVec(
				prometheus.HistogramOpts{
					Namespace:   "aerospike_ttl",
					Name:        "counts_hist",
					Help:        "Histogram of how many records fall into each ttl bucket.",
					Buckets:     buckets,
					ConstLabels: prometheus.Labels{"namespace": namespace, "set": set},
				}, []string{},
			)
			prometheus.MustRegister(expirationTTLCountsHist)
			histograms["counts"] = expirationTTLCountsHist
		}

		// Add the HistogramVec to the inner map
		ns_set_to_histograms[namespace+":"+set] = histograms

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
