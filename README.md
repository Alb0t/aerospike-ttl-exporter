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
# HELP aerospike_expirationTTL_counts Days in which this many records will expire. Sampled locally. Shows counts of how many records were found in each bucket.
# TYPE aerospike_expirationTTL_counts gauge
aerospike_expirationTTL_counts{days="159",namespace="mynamespace",set=""} 449
aerospike_expirationTTL_counts{days="160",namespace="mynamespace",set=""} 2260
aerospike_expirationTTL_counts{days="161",namespace="mynamespace",set=""} 5364
aerospike_expirationTTL_counts{days="162",namespace="mynamespace",set=""} 4423
aerospike_expirationTTL_counts{days="163",namespace="mynamespace",set=""} 4021
aerospike_expirationTTL_counts{days="164",namespace="mynamespace",set=""} 5384
aerospike_expirationTTL_counts{days="165",namespace="mynamespace",set=""} 4667
aerospike_expirationTTL_counts{days="166",namespace="mynamespace",set=""} 6266
aerospike_expirationTTL_counts{days="167",namespace="mynamespace",set=""} 4459
aerospike_expirationTTL_counts{days="168",namespace="mynamespace",set=""} 5030
aerospike_expirationTTL_counts{days="169",namespace="mynamespace",set=""} 3486
aerospike_expirationTTL_counts{days="170",namespace="mynamespace",set=""} 6923
aerospike_expirationTTL_counts{days="171",namespace="mynamespace",set=""} 1105
aerospike_expirationTTL_counts{days="172",namespace="mynamespace",set=""} 277
aerospike_expirationTTL_counts{days="173",namespace="mynamespace",set=""} 311
aerospike_expirationTTL_counts{days="174",namespace="mynamespace",set=""} 281
aerospike_expirationTTL_counts{days="175",namespace="mynamespace",set=""} 471
aerospike_expirationTTL_counts{days="176",namespace="mynamespace",set=""} 330
aerospike_expirationTTL_counts{days="177",namespace="mynamespace",set=""} 288
aerospike_expirationTTL_counts{days="178",namespace="mynamespace",set=""} 351
aerospike_expirationTTL_counts{days="179",namespace="mynamespace",set=""} 264
aerospike_expirationTTL_counts{days="180",namespace="mynamespace",set=""} 223
aerospike_expirationTTL_counts{days="181",namespace="mynamespace",set=""} 356
aerospike_expirationTTL_counts{days="182",namespace="mynamespace",set=""} 342
aerospike_expirationTTL_counts{days="183",namespace="mynamespace",set=""} 384
aerospike_expirationTTL_counts{days="184",namespace="mynamespace",set=""} 531
aerospike_expirationTTL_counts{days="185",namespace="mynamespace",set=""} 351
aerospike_expirationTTL_counts{days="186",namespace="mynamespace",set=""} 296
aerospike_expirationTTL_counts{days="187",namespace="mynamespace",set=""} 418
aerospike_expirationTTL_counts{days="188",namespace="mynamespace",set=""} 346
aerospike_expirationTTL_counts{days="189",namespace="mynamespace",set=""} 427
aerospike_expirationTTL_counts{days="190",namespace="mynamespace",set=""} 300
aerospike_expirationTTL_counts{days="191",namespace="mynamespace",set=""} 501
aerospike_expirationTTL_counts{days="192",namespace="mynamespace",set=""} 430
aerospike_expirationTTL_counts{days="193",namespace="mynamespace",set=""} 387
aerospike_expirationTTL_counts{days="194",namespace="mynamespace",set=""} 391
aerospike_expirationTTL_counts{days="195",namespace="mynamespace",set=""} 485
aerospike_expirationTTL_counts{days="196",namespace="mynamespace",set=""} 547
aerospike_expirationTTL_counts{days="197",namespace="mynamespace",set=""} 635
aerospike_expirationTTL_counts{days="198",namespace="mynamespace",set=""} 677
aerospike_expirationTTL_counts{days="199",namespace="mynamespace",set=""} 964
aerospike_expirationTTL_counts{days="200",namespace="mynamespace",set=""} 3173
aerospike_expirationTTL_counts{days="201",namespace="mynamespace",set=""} 2211
aerospike_expirationTTL_counts{days="minBucket",namespace="mynamespace",set=""} 159
aerospike_expirationTTL_counts{days="total",namespace="mynamespace",set=""} 70785
# HELP aerospike_expirationTTL_perc Days in which this many records will expire. Sampled locally. Shows percentages of how many records were found in each bucket vs total records scanned.
# TYPE aerospike_expirationTTL_perc gauge
aerospike_expirationTTL_perc{days="159",namespace="mynamespace",set=""} 0.6343151797697252
aerospike_expirationTTL_perc{days="160",namespace="mynamespace",set=""} 3.1927668291304654
aerospike_expirationTTL_perc{days="161",namespace="mynamespace",set=""} 7.57787666878576
aerospike_expirationTTL_perc{days="162",namespace="mynamespace",set=""} 6.248498975771703
aerospike_expirationTTL_perc{days="163",namespace="mynamespace",set=""} 5.680582044218408
aerospike_expirationTTL_perc{days="164",namespace="mynamespace",set=""} 7.606131242494879
aerospike_expirationTTL_perc{days="165",namespace="mynamespace",set=""} 6.593204775022957
aerospike_expirationTTL_perc{days="166",namespace="mynamespace",set=""} 8.852157943067034
aerospike_expirationTTL_perc{days="167",namespace="mynamespace",set=""} 6.299357208448118
aerospike_expirationTTL_perc{days="168",namespace="mynamespace",set=""} 7.10602528784347
aerospike_expirationTTL_perc{days="169",namespace="mynamespace",set=""} 4.92477219749947
aerospike_expirationTTL_perc{days="170",namespace="mynamespace",set=""} 9.780320689411598
aerospike_expirationTTL_perc{days="171",namespace="mynamespace",set=""} 1.5610651974288339
aerospike_expirationTTL_perc{days="172",namespace="mynamespace",set=""} 0.3913258458713004
aerospike_expirationTTL_perc{days="173",namespace="mynamespace",set=""} 0.439358621176803
aerospike_expirationTTL_perc{days="174",namespace="mynamespace",set=""} 0.39697676061312426
aerospike_expirationTTL_perc{days="175",namespace="mynamespace",set=""} 0.6653952108497563
aerospike_expirationTTL_perc{days="176",namespace="mynamespace",set=""} 0.4662004662004662
aerospike_expirationTTL_perc{days="177",namespace="mynamespace",set=""} 0.406865861411316
aerospike_expirationTTL_perc{days="178",namespace="mynamespace",set=""} 0.49586776859504134
aerospike_expirationTTL_perc{days="179",namespace="mynamespace",set=""} 0.372960372960373
aerospike_expirationTTL_perc{days="180",namespace="mynamespace",set=""} 0.3150384968566787
aerospike_expirationTTL_perc{days="181",namespace="mynamespace",set=""} 0.5029314120223212
aerospike_expirationTTL_perc{days="182",namespace="mynamespace",set=""} 0.4831532104259377
aerospike_expirationTTL_perc{days="183",namespace="mynamespace",set=""} 0.5424878152150879
aerospike_expirationTTL_perc{days="184",namespace="mynamespace",set=""} 0.7501589319771138
aerospike_expirationTTL_perc{days="185",namespace="mynamespace",set=""} 0.49586776859504134
aerospike_expirationTTL_perc{days="186",namespace="mynamespace",set=""} 0.41816769089496364
aerospike_expirationTTL_perc{days="187",namespace="mynamespace",set=""} 0.5905205905205905
aerospike_expirationTTL_perc{days="188",namespace="mynamespace",set=""} 0.4888041251677615
aerospike_expirationTTL_perc{days="189",namespace="mynamespace",set=""} 0.6032351486896942
aerospike_expirationTTL_perc{days="190",namespace="mynamespace",set=""} 0.42381860563678747
aerospike_expirationTTL_perc{days="191",namespace="mynamespace",set=""} 0.707777071413435
aerospike_expirationTTL_perc{days="192",namespace="mynamespace",set=""} 0.607473334746062
aerospike_expirationTTL_perc{days="193",namespace="mynamespace",set=""} 0.5467260012714558
aerospike_expirationTTL_perc{days="194",namespace="mynamespace",set=""} 0.5523769160132797
aerospike_expirationTTL_perc{days="195",namespace="mynamespace",set=""} 0.6851734124461397
aerospike_expirationTTL_perc{days="196",namespace="mynamespace",set=""} 0.7727625909444091
aerospike_expirationTTL_perc{days="197",namespace="mynamespace",set=""} 0.8970827152645334
aerospike_expirationTTL_perc{days="198",namespace="mynamespace",set=""} 0.9564173200536837
aerospike_expirationTTL_perc{days="199",namespace="mynamespace",set=""} 1.3618704527795438
aerospike_expirationTTL_perc{days="200",namespace="mynamespace",set=""} 4.482588118951755
aerospike_expirationTTL_perc{days="201",namespace="mynamespace",set=""} 3.1235431235431235
aerospike_expirationTTL_perc{days="minBucket",namespace="mynamespace",set=""} 159
aerospike_expirationTTL_perc{days="total",namespace="mynamespace",set=""} 70785
```

# To use:
Grab a release from https://github.com/Alb0t/aerospike-ttl-exporter/releases .
Extract and run the binary, or create a systemd service file with options.

# Usage/params:
```
Usage of ./aerospike-ttl-exporter:
  -exportPercentages
    	Export percentage distribution per bucket out of total. (default true)
  -exportRecordCount
    	Export record count per bucket.
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
    	How many records to stop scanning at? Will stop at recordCount or scanPercent, whichever is less. Pass '-recordCount=-1' to only use scanPercent. (default 3000000)
  -recordQueueSize int
    	Number of records to place in queue before blocking. (default 50)
  -reportCount int
    	How many records should be report on? Every <x> records will cause an entry in the stdout (default 100000)
  -scanPercent int
    	What percentage of data to scan? Will stop at recordCount or scanPercent, whichever is less. (default 1)
  -verbose
    	Print more stuff.
      
```
