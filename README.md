# aerospike-ttl-prom-exporter

A prometheus exporter than scans record ttl for Aerospike and exports it.

# The problem:
tl;dr - this allows us to measure storage capacity in a situation where we store at-eviction.

TTL (time-to-live) on a record dictates when the record will expire, and if evicting we need to measure the lowest bucket and trends of these ttls.

The data currently exported by Aerospike histogram dumps is not accurate enough to give us the granularity we need to look at these marks accurately. The histogram exports 100 buckets, always, and depending on the min/max of that local system we can get wildly different metrics between systems. This also means that the accuracy of these metrics can be very low, and further making the problem worse - we do not have a way to line up the 'bucket' boundaries of the exported histograms, so we have to decrease accuracy further by lumping the ranges into different buckets that line up in grafana.

# Solution:
* Write a custom exporter that takes a small 1% sample on each server on a scheduled task, and exports that data to prometheus.


Example output:
```

```

# To use:
1) Grab a release from https://github.com/Alb0t/aerospike-ttl-exporter/releases
2) Extract and create a config file
3) Run the binary pointing to the config file, or create a systemd service file.


# Usage/params:
```
Usage of ./aerospike-ttl-exporter:
  -configFile string
    Path to the config file for the exporter. (Default: "/etc/ttl-aerospike-exporter.yaml")
```
## configFile
The config file should be yaml and following a very strict pattern/layout. You can download the conf.yaml file in the repo and change it to suit your needs.
There are _NO DEFAULT VALUES_ because of the way golang works with reading yaml. Any key/value omitted, or misspelled, will result in that value being set to the type's default. Ex. if scanPercent is omitted it will be set to 0. If set is omitted it will be set to "". Data types can be found in collector.go in the declared structs, and you can find the defaults in golang if you want. Basically, don't mispell anything or leave anything out.

The program will print all its realized config values before each scan to stdout if running in debug mode.
ex.

```
...
DEBU[2019-11-14T11:51:42-07:00] Checking to see if 22 should be our minBucket.
DEBU[2019-11-14T11:51:42-07:00] minbucket not set:false                      
INFO[2019-11-14T11:51:42-07:00] Scan complete.                                namespace=myns set= total(records exported)=50000 totalInspected=50000
INFO[2019-11-14T11:51:42-07:00] Scan for myns: took 0.2 minutes.          
DEBU[2019-11-14T11:51:42-07:00] Printing namespaces to monitor and their config below.
DEBU[2019-11-14T11:51:42-07:00] {Namespace:somens Set:User Recordcount:50000 ScanPercent:1 ExportPercentages:true ExportRecordCount:false ExportType:days ExportTypeDivision:86400 ExportBucketMultiply:1 MinPercent:1e-05 MinCount:50 ReportCount:300000 ScanPriority:1 ScanTotalTimeout:20m ScanSocketTimeout:20m PolicyTotalTimeout:20m PolicySocketTimeout:20m}
DEBU[2019-11-14T11:51:42-07:00] Printing namespaces to monitor and their config below.
DEBU[2019-11-14T11:51:42-07:00] {Namespace:myns Set: Recordcount:50000 ScanPercent:1 ExportPercentages:true ExportRecordCount:false ExportType:days ExportTypeDivision:86400 ExportBucketMultiply:1 MinPercent:1e-05 MinCount:50 ReportCount:300000 ScanPriority:1 ScanTotalTimeout:20m ScanSocketTimeout:20m PolicyTotalTimeout:20m PolicySocketTimeout:20m}
...
```

# Notes
minPercent/minCount was added to prevent exporting minBucket with a very low value if only a single, or few small percentage, of records are present with that TTL. It uses both parameters together by default (if count>minCount AND pct>minPercet) but you can override this behavior by setting count=0 or percent=100 respectively.


These options can be used to configure smaller/larger buckets:
```
  exportType string
    	What label should we give the bucket
  exportTypeDivision int
    	What should we divide by the seconds to get the bucket size?
  exportBucketMultiply int
      Multiply the bucket value by this before exporting
 ```

 For example, if you wanted 15 minute buckets, you could pass these as:
 ```
   exportType: minutes
   exportTypeDivision: 900
   exportBucketMultiply: 15
 ```
 This would export things in 15 minute buckets, and report them as 'minutes'
