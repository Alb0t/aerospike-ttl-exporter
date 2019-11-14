package main

import (
	"flag"
	as "github.com/aerospike/aerospike-client-go"
	"github.com/carlescere/scheduler"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
	"io/ioutil"
	"os"
)

var buildVersion = "0.2.0"
var expirationTTLCounts *prometheus.GaugeVec
var expirationTTLPercents *prometheus.GaugeVec

var configFile = flag.String("configFile", "/etc/ttl-aerospike-exporter.yaml", "The yaml config file for the exporter")

var buildInfo = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "aerospike_ttl_build_info",
		Help: "Build info",
	},
	[]string{"version"},
)

var scanTimes = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "aerospike_ttl_scan_minutes",
		Help: "Scan times in minutes.",
	},
	[]string{"namespace", "set"},
)

var scanLastUpdated = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "aerospike_ttl_scan_last_updated",
		Help: "Epoch time that scan last finished.",
	},
	[]string{"namespace", "set"},
)

// these are global because im lazy
var running = false                             // bool to track whether a scan is running already or not.
var localIps = make(map[string]bool)            // map to prevent duplicates, and a list of what our local ips are
var resultMap = make(map[string]map[uint32]int) // map of namespace:set -> { ttl, count } stored globally so we can report 0 on unseen metrics if the server suddenly doesn't have any
var config conf

type conf struct {
	Service serviceConf
	Monitor []monconf
}

type serviceConf struct {
	ListenPort          string `yaml:"listenPort"`
	NodeAddr            string `yaml:"nodeAddr"`
	SkipNodeCheck       bool   `yaml:"skipNodeCheck"`
	FailOnClusterChange bool   `yaml:"FailOnClusterChange"`
	FrequencySecs       int    `yaml:"frequencySecs"`
	Verbose             bool   `yaml:"verbose"`
}

type monconf struct {
	Namespace            string      `yaml:"namespace"`
	Set                  string      `yaml:"set"`
	Recordcount          int         `yaml:"recordCount,omitempty"`
	ScanPercent          int         `yaml:"scanPercent,omitempty"`
	ExportPercentages    bool        `yaml:"exportPercentages,omitempty"`
	ExportRecordCount    bool        `yaml:"exportRecordCount,omitempty"`
	ExportType           string      `yaml:"exportType,omitempty"`
	ExportTypeDivision   uint32      `yaml:"exportTypeDivision,omitempty"`
	ExportBucketMultiply uint32      `yaml:"exportBucketMultiply,omitempty"`
	MinPercent           float64     `yaml:"minPercent,omitempty"`
	MinCount             int         `yaml:"minCount,omitempty"`
	ReportCount          int         `yaml:"reportCount,omitempty"`
	ScanPriority         as.Priority `yaml:"scanPriority"`
	ScanTotalTimeout     string      `yaml:"scanTotalTimeout"`
	ScanSocketTimeout    string      `yaml:"scanSocketTimeout"`
	PolicyTotalTimeout   string      `yaml:"policyTotalTimeout"`
	PolicySocketTimeout  string      `yaml:"policySocketTimeout"`
}

func (c *conf) getConf() *conf {
	flag.Parse()
	yamlFile, err := ioutil.ReadFile(*configFile)
	if err != nil {
		log.Fatal("Failed to read configfile: ", *configFile)
	}
	err = yaml.Unmarshal(yamlFile, c)
	if err != nil {
		log.Fatal("Failed to unmarshal configfile, bad format? File:", *configFile)
	}
	return c
}

func init() {
	config.getConf()
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
	})
	log.SetOutput(os.Stdout)

	if config.Service.Verbose {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	expirationTTLPercents = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "aerospike_ttl_percents",
			Help: "Time in which this many records will expire. Sampled locally. Shows percentages of how many records were found in each bucket vs total records scanned.",
		},
		[]string{"exportType", "ttl", "namespace", "set"},
	)

	expirationTTLCounts = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "aerospike_ttl_counts",
			Help: "Time in which this many records will expire. Sampled locally. Shows counts of how many records were found in each bucket.",
		},
		[]string{"exportType", "ttl", "namespace", "set"},
	)
	prometheus.MustRegister(buildInfo)
	prometheus.MustRegister(scanTimes)
	prometheus.MustRegister(scanLastUpdated)
	buildInfo.WithLabelValues(buildVersion).Set(1)
	prometheus.MustRegister(expirationTTLPercents)
	prometheus.MustRegister(expirationTTLCounts)

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
