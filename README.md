# aerospike-ttl-prom-exporter

A prometheus exporter than scans record ttl for Aerospike and exports it.

# The problem:
tl;dr - this allows us to measure storage capacity in a situation where we store data until eviction, or we want to understand the distribution of TTLs better within a system and monitor that over time.

TTL (time-to-live) on a record dictates when the record will expire, and if evicting we need to measure the lowest bucket and trends of these ttls.

The data currently exported by Aerospike histogram dumps is not accurate enough to give us the granularity we need to look at these marks accurately. The histogram exports 100 buckets, always, and depending on the min/max of that local system we can get wildly different metrics between systems. This also means that the accuracy of these metrics can be very low, and further making the problem worse - we do not have a way to line up the 'bucket' boundaries of the exported histograms, so we have to decrease accuracy further by lumping the ranges into different buckets that line up in grafana. The built-in feature also only gives us record counts, and we are unable to see a distribution of size across various ttl buckets.

# Solution:
Write a custom exporter that takes a sample on each server on a scheduled task, and exports that data to prometheus.

This allows us to ask questions like: 
## How large are the users in the fresh TTL range?
```
# Scenario: default-ttl=33d
histogram_quantile(0.50, sum(rate(aerospike_ttl_kib_hist_bucket{namespace="myns"}[$__rate_interval])) by (le))
# Query result: 28.6
# Interpreted: 50% of the data has been written in the last (33-28.6) 4.4 days
```

## How many records are in the fresh TTL range?
```
# Scenario: default-ttl=33d
histogram_quantile(0.50, sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns"}[$__rate_interval])) by (le))
# Query result: 22.1
# Interpreted: 50% of the data has been written in the last (33-22.1) 10.9 days
```

## What percentage of records will expire in a week?
```
# We divide the number of records (counts) that will expire <=7 days, but the "+Inf' bucket which includes everything.
sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns",le="7",ttlUnit="days"}[$__rate_interval]))*100
/
sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns",le="+Inf"}[$__rate_interval]))

# Result: 13.1
```

## What percentage of data will expire in a week?
```
# We divide the number of records (counts) that will expire <=7 days, but the "+Inf' bucket which includes everything.
sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns",le="7",ttlUnit="days"}[$__rate_interval]))*100
/
sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns",le="+Inf"}[$__rate_interval]))

# Result: 1.4
```

## Conclusions about above queries: We have an abornmal distribution where our largest records are updated more often!

## How will my evict-void-time change if I evict 10% of my data earlier?
This is useful if you are already evicting and you need to understand how changes will affect your eviction time like:
* records will become 10% larger
* we will lose 10% of our capacity
* we will reduce my hwm by 10% of its current value (ex.. 50 to 45%)

How do we forecast those changes?
```
histogram_quantile(0.10, 
    sum(
        rate(
            aerospike_ttl_kib_hist_bucket{namespace="myns"}
            [$__rate_interval]
        )
    ) by (le)
)

# Result: 26.2
```

Example output:
```
....
aerospike_ttl_build_info{version="3.1.0"} 1
aerospike_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.3824e+07"} 858
aerospike_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.4688e+07"} 901
aerospike_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.56384e+07"} 971
aerospike_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="1.9008e+07"} 1004
aerospike_ttl_counts_hist_bucket{namespace="myNS",set="Beans",le="+Inf"} 1004
aerospike_ttl_counts_hist_sum{namespace="myNS",set="Beans"} 9.037094916e+09
aerospike_ttl_counts_hist_count{namespace="myNS",set="Beans"} 1004
aerospike_ttl_kib_hist_bucket{namespace="myNS",set="Beans",storage_type="device",le="1.3824e+07"} 1938
aerospike_ttl_kib_hist_bucket{namespace="myNS",set="Beans",storage_type="device",le="1.4688e+07"} 2046
aerospike_ttl_kib_hist_bucket{namespace="myNS",set="Beans",storage_type="device",le="1.56384e+07"} 2196
aerospike_ttl_kib_hist_bucket{namespace="myNS",set="Beans",storage_type="device",le="1.9008e+07"} 4041
aerospike_ttl_kib_hist_bucket{namespace="myNS",set="Beans",storage_type="device",le="+Inf"} 4041
aerospike_ttl_kib_hist_sum{namespace="myNS",set="Beans",storage_type="device"} 5.244241494e+10
aerospike_ttl_kib_hist_count{namespace="myNS",set="Beans",storage_type="device"} 4041
aerospike_ttl_scan_last_updated{namespace="myNS",set="Beans"} 1.690408779e+09
aerospike_ttl_scan_time_seconds{namespace="myNS",set="Beans"} 103
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
