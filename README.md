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
# HELP aerospike_ttl_build_info Build info
# TYPE aerospike_ttl_build_info gauge
aerospike_ttl_build_info{version="0.2.0"} 1
# HELP aerospike_ttl_percents Time in which this many records will expire. Sampled locally. Shows percentages of how many records were found in each bucket vs total records scanned.
# TYPE aerospike_ttl_percents gauge
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="140"} 1.16
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="141"} 1.902
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="142"} 1.56
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="143"} 1.812
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="144"} 1.7
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="145"} 1.624
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="146"} 1.896
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="147"} 2.14
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="148"} 1.468
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="149"} 1.894
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="150"} 2.054
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="151"} 1.86
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="152"} 1.722
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="153"} 1.758
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="154"} 1.928
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="155"} 1.99
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="156"} 1.796
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="157"} 1.898
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="158"} 1.882
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="159"} 1.918
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="160"} 2.954
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="161"} 1.93
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="162"} 2.186
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="163"} 2.112
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="164"} 2.6
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="165"} 2.544
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="166"} 3.028
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="167"} 2.942
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="168"} 4.542
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="169"} 2.988
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="170"} 2.584
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="171"} 2.744
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="172"} 3.05
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="173"} 2.206
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="174"} 2.84
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="175"} 3.866
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="176"} 3.932
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="177"} 2.54
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="178"} 2.218
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="179"} 2.154
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="180"} 0.088
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="181"} 0.052
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="182"} 0.172
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="183"} 0.248
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="184"} 0.136
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="185"} 0.09
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="186"} 0.08
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="187"} 0.112
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="188"} 0.098
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="189"} 0.142
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="190"} 0.166
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="191"} 0.178
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="192"} 0.078
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="193"} 0.09
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="194"} 0.108
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="195"} 0.124
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="196"} 0.072
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="197"} 0.204
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="198"} 0.31
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="199"} 0.118
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="200"} 0.136
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="201"} 0.156
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="202"} 0.214
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="203"} 0.228
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="204"} 0.314
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="205"} 0.21
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="206"} 0.342
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="207"} 0.31
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="208"} 0.56
aerospike_ttl_percents{exportType="days",namespace="mynamespace",set="User",ttl="209"} 2.942
aerospike_ttl_percents{exportType="totalScanned",namespace="mynamespace",set="User",ttl="total"} 50000
# HELP aerospike_ttl_scan_last_updated Epoch time that scan last finished.
# TYPE aerospike_ttl_scan_last_updated gauge
aerospike_ttl_scan_last_updated{namespace="mynamespace",set="User"} 1.573758691e+09
# HELP aerospike_ttl_scan_minutes Scan times in minutes.
# TYPE aerospike_ttl_scan_minutes gauge
aerospike_ttl_scan_minutes{namespace="mynamespace",set="User"} 0.21666666666666667

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
