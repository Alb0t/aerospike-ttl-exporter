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
aerospike_expirationTTL{days="unexpirable",namespace="mynamespace",set="testset"} 1
etc.....
```
