# aerospike-ttl-prom-exporter

A prometheus exporter than scans record ttl for Aerospike and exports it.

# The problem:
tl;dr - this allows us to measure storage capacity in a situation where we store data until eviction, or we want to understand the distribution of TTLs better within a system and monitor that over time.

TTL (time-to-live) on a record dictates when the record will expire, and if evicting we need to measure the lowest bucket and trends of these ttls.

The data currently exported by Aerospike histogram dumps is not accurate enough to give us the granularity we need to look at these marks accurately. The histogram exports 100 buckets, always, and depending on the min/max of that local system we can get wildly different metrics between systems. This also means that the accuracy of these metrics can be very low, and further making the problem worse - we do not have a way to line up the 'bucket' boundaries of the exported histograms, so we have to decrease accuracy further by lumping the ranges into different buckets that line up in grafana.

# Solution:
* Write a custom exporter that takes a small 1% sample on each server on a scheduled task, and exports that data to prometheus.


Example output:
```
....
aerospike_expiration_ttl_counts_hist_bucket{namespace="myOtherNS",set="",le="1.79712e+08"} 70028
aerospike_expiration_ttl_counts_hist_bucket{namespace="myOtherNS",set="",le="1.80576e+08"} 70028
aerospike_expiration_ttl_counts_hist_bucket{namespace="myOtherNS",set="",le="+Inf"} 70028
aerospike_expiration_ttl_counts_hist_sum{namespace="myOtherNS",set=""} 3.68036698307e+11
aerospike_expiration_ttl_counts_hist_count{namespace="myOtherNS",set=""} 70028
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.3824e+07"} 145142
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.4688e+07"} 186596
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.56384e+07"} 223357
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.9008e+07"} 241662
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="+Inf"} 241699
aerospike_expiration_ttl_counts_hist_sum{namespace="myNS",set="Beans"} 3.166097393414e+12
aerospike_expiration_ttl_counts_hist_count{namespace="myNS",set="Beans"} 241699
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="boo",le="1.3824e+07"} 9056
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="boo",le="1.4688e+07"} 11760
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="boo",le="1.56384e+07"} 13648
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="boo",le="1.9008e+07"} 16000
aerospike_expiration_ttl_counts_hist_bucket{namespace="myNS",set="boo",le="+Inf"} 16000
aerospike_expiration_ttl_counts_hist_sum{namespace="myNS",set="boo"} 2.1257415038e+11
aerospike_expiration_ttl_counts_hist_count{namespace="myNS",set="boo"} 16000
aerospike_ttl_build_info{version="3.0.0"} 1
aerospike_ttl_scan_last_updated{namespace="myOtherNS",set=""} 1.690219845e+09
aerospike_ttl_scan_last_updated{namespace="myNS",set="Beans"} 1.690219844e+09
aerospike_ttl_scan_last_updated{namespace="myNS",set="boo"} 1.690219848e+09
aerospike_ttl_scan_time_seconds{namespace="myOtherNS",set=""} 1
aerospike_ttl_scan_time_seconds{namespace="myNS",set="Beans"} 6
aerospike_ttl_scan_time_seconds{namespace="myNS",set="boo"} 1
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
time="2021-03-23T15:16:09-06:00" level=debug msg="Printing namespaces to monitor and their config below."
time="2021-03-23T15:16:09-06:00" level=debug msg="{Namespace:mynamespace Set:User Recordcount:-1 ScanPercent:1 ExportPercentages:true ExportRecordCount:false ExportType:days ExportTypeDivision:86400 ExportBucketMultiply:1 ReportCount:100 ScanPriority:1 ScanTotalTimeout:20m ScanSocketTimeout:20m PolicyTotalTimeout:20m PolicySocketTimeout:20m RecordsPerSecond:500}"
time="2021-03-23T15:16:09-06:00" level=debug msg="Running:true"
time="2021-03-23T15:16:09-06:00" level=debug msg="Finding local node."
time="2021-03-23T15:16:09-06:00" level=debug msg="Fetching membership list.."
time="2021-03-23T15:16:09-06:00" level=debug msg="Looping through active cluster nodes"
time="2021-03-23T15:16:09-06:00" level=debug msg="Comparing against local ip list.."
time="2021-03-23T15:16:09-06:00" level=debug msg="found node with matching localip 127.0.0.1==BB9020014AC4202 127.0.0.1:3000"
time="2021-03-23T15:16:09-06:00" level=info msg="Begin scan/inspection." namespace=mynamespace set=User
time="2021-03-23T15:16:09-06:00" level=debug msg="Setting max records to 100 based off sample percent 1"
...
```

# Notes

`staticBucketList` and `bucketWidth/numberOfBucketsToExport` are mutually exclusive. You must pick one or the other. Program will fail to start with a fatal log message if you try to specify both.

`staticBucketList` accepts an array of buckets you wish to define for the histogram.

Alternatively, you can use `bucketWidth` `numberOfBucketsToExport` and `bucketStart` to specify a linear histogram.  