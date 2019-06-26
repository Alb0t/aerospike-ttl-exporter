# aerospike-ttl-prom-exporter

A prometheus exporter than scans record ttl for Aerospike and exports it.

# The problem:
tl;dr - this allows us to measure storage capacity.

TTL (time-to-live) on a record dictates when the record will expire, and if evicting we need to measure the lowest bucket and trends of these ttls.

The data currently exported by Aerospike histogram dumps is not accurate enough to give us the granularity we need to look at these marks accurately. The histogram exports 100 buckets, always, and depending on the min/max of that local system we can get wildly different metrics between systems. This also means that the accuracy of these metrics can be very low, and further making the problem worse - we do not have a way to line up the 'bucket' boundaries of the exported histograms, so we have to decrease accuracy further by lumping the ranges into different buckets that line up in grafana.

# Solution:
* Write a custom exporter that takes a small 1% sample on each server on a scheduled task, and exports that data to prometheus.


Example output:
```
aerospike_expirationTTL{days="8",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="82",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="83",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="85",namespace="mynamespace",set="testset"} 24
aerospike_expirationTTL{days="86",namespace="mynamespace",set="testset"} 6
aerospike_expirationTTL{days="89",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="9",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="90",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="92",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="93",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="96",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="97",namespace="mynamespace",set="testset"} 6
aerospike_expirationTTL{days="98",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="99",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="minBucket",namespace="mynamespace",set="testset"} 1
aerospike_expirationTTL{days="total",namespace="mynamespace",set="testset"} 738
etc.....
```

# To use:
Grab a release from https://github.com/Alb0t/aerospike-ttl-exporter/releases .
Extract and run the binary, or create a systemd service file with options.

# Usage/params:
```
./aerospike-ttl-exporter -h
Usage of ./aerospike-ttl-exporter:
  -failOnClusterChange
    	should we abort the scan on cluster change?
  -frequencySecs int
    	how often to run the scan to report data (seconds)? (default 300)
  -listen string
    	listen address for prometheus (default ":9146")
  -namespaceSets string
    	namespace:set comma delimited. Ex: myns:myset,myns2:myset3,myns3:,myns4 - set optional, but colon is not
  -node string
    	aerospike node (default "127.0.0.1")
  -recordCount int
    	How many records to stop scanning at? Will stop at recordCount or scanPercent, whichever is less. (default 3000000)
  -recordQueueSize int
    	Number of records to place in queue before blocking. (default 50)
  -reportCount int
    	How many records should be report on? Every <x> records will cause an entry in the stdout (default 100000)
  -scanPercent int
    	What percentage of data to scan? Will stop at recordCount or scanPercent, whichever is less. (default 1)
  -verbose
    	Print more stuff.
      ```
