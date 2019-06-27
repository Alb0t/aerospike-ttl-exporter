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
# HELP aerospike_ttl_counts Days in which this many records will expire. Sampled locally. Shows counts of how many records were found in each bucket.
# TYPE aerospike_ttl_counts gauge
aerospike_ttl_counts{days="159",namespace="mynamespace",set=""} 481
aerospike_ttl_counts{days="160",namespace="mynamespace",set=""} 2293
aerospike_ttl_counts{days="161",namespace="mynamespace",set=""} 5424
aerospike_ttl_counts{days="162",namespace="mynamespace",set=""} 4384
aerospike_ttl_counts{days="163",namespace="mynamespace",set=""} 4030
aerospike_ttl_counts{days="164",namespace="mynamespace",set=""} 5390
aerospike_ttl_counts{days="165",namespace="mynamespace",set=""} 4681
aerospike_ttl_counts{days="166",namespace="mynamespace",set=""} 6249
aerospike_ttl_counts{days="167",namespace="mynamespace",set=""} 4436
aerospike_ttl_counts{days="168",namespace="mynamespace",set=""} 5034
aerospike_ttl_counts{days="169",namespace="mynamespace",set=""} 3470
aerospike_ttl_counts{days="170",namespace="mynamespace",set=""} 6954
aerospike_ttl_counts{days="171",namespace="mynamespace",set=""} 1030
aerospike_ttl_counts{days="172",namespace="mynamespace",set=""} 264
aerospike_ttl_counts{days="173",namespace="mynamespace",set=""} 308
aerospike_ttl_counts{days="174",namespace="mynamespace",set=""} 284
aerospike_ttl_counts{days="175",namespace="mynamespace",set=""} 483
aerospike_ttl_counts{days="176",namespace="mynamespace",set=""} 321
aerospike_ttl_counts{days="177",namespace="mynamespace",set=""} 287
aerospike_ttl_counts{days="178",namespace="mynamespace",set=""} 368
aerospike_ttl_counts{days="179",namespace="mynamespace",set=""} 249
aerospike_ttl_counts{days="180",namespace="mynamespace",set=""} 215
aerospike_ttl_counts{days="181",namespace="mynamespace",set=""} 369
aerospike_ttl_counts{days="182",namespace="mynamespace",set=""} 340
aerospike_ttl_counts{days="183",namespace="mynamespace",set=""} 384
aerospike_ttl_counts{days="184",namespace="mynamespace",set=""} 537
aerospike_ttl_counts{days="185",namespace="mynamespace",set=""} 341
aerospike_ttl_counts{days="186",namespace="mynamespace",set=""} 301
aerospike_ttl_counts{days="187",namespace="mynamespace",set=""} 418
aerospike_ttl_counts{days="188",namespace="mynamespace",set=""} 348
aerospike_ttl_counts{days="189",namespace="mynamespace",set=""} 427
aerospike_ttl_counts{days="190",namespace="mynamespace",set=""} 296
aerospike_ttl_counts{days="191",namespace="mynamespace",set=""} 498
aerospike_ttl_counts{days="192",namespace="mynamespace",set=""} 443
aerospike_ttl_counts{days="193",namespace="mynamespace",set=""} 379
aerospike_ttl_counts{days="194",namespace="mynamespace",set=""} 390
aerospike_ttl_counts{days="195",namespace="mynamespace",set=""} 485
aerospike_ttl_counts{days="196",namespace="mynamespace",set=""} 559
aerospike_ttl_counts{days="197",namespace="mynamespace",set=""} 631
aerospike_ttl_counts{days="198",namespace="mynamespace",set=""} 683
aerospike_ttl_counts{days="199",namespace="mynamespace",set=""} 967
aerospike_ttl_counts{days="200",namespace="mynamespace",set=""} 3320
aerospike_ttl_counts{days="201",namespace="mynamespace",set=""} 2034
aerospike_ttl_counts{days="minBucket",namespace="mynamespace",set=""} 159
aerospike_ttl_counts{days="total",namespace="mynamespace",set=""} 70785
# HELP aerospike_ttl_percents Days in which this many records will expire. Sampled locally. Shows percentages of how many records were found in each bucket vs total records scanned.
# TYPE aerospike_ttl_percents gauge
aerospike_ttl_percents{days="159",namespace="mynamespace",set=""} 0.6795224977043158
aerospike_ttl_percents{days="160",namespace="mynamespace",set=""} 3.239386875750512
aerospike_ttl_percents{days="161",namespace="mynamespace",set=""} 7.6626403899131175
aerospike_ttl_percents{days="162",namespace="mynamespace",set=""} 6.193402557038921
aerospike_ttl_percents{days="163",namespace="mynamespace",set=""} 5.693296602387512
aerospike_ttl_percents{days="164",namespace="mynamespace",set=""} 7.614607614607615
aerospike_ttl_percents{days="165",namespace="mynamespace",set=""} 6.612982976619341
aerospike_ttl_percents{days="166",namespace="mynamespace",set=""} 8.828141555414282
aerospike_ttl_percents{days="167",namespace="mynamespace",set=""} 6.266864448682631
aerospike_ttl_percents{days="168",namespace="mynamespace",set=""} 7.1116762025852935
aerospike_ttl_percents{days="169",namespace="mynamespace",set=""} 4.902168538532175
aerospike_ttl_percents{days="170",namespace="mynamespace",set=""} 9.824115278660733
aerospike_ttl_percents{days="171",namespace="mynamespace",set=""} 1.455110546019637
aerospike_ttl_percents{days="172",namespace="mynamespace",set=""} 0.372960372960373
aerospike_ttl_percents{days="173",namespace="mynamespace",set=""} 0.43512043512043513
aerospike_ttl_percents{days="174",namespace="mynamespace",set=""} 0.40121494666949215
aerospike_ttl_percents{days="175",namespace="mynamespace",set=""} 0.6823479550752278
aerospike_ttl_percents{days="176",namespace="mynamespace",set=""} 0.4534859080313626
aerospike_ttl_percents{days="177",namespace="mynamespace",set=""} 0.40545313272586
aerospike_ttl_percents{days="178",namespace="mynamespace",set=""} 0.5198841562477926
aerospike_ttl_percents{days="179",namespace="mynamespace",set=""} 0.3517694426785336
aerospike_ttl_percents{days="180",namespace="mynamespace",set=""} 0.303736667373031
aerospike_ttl_percents{days="181",namespace="mynamespace",set=""} 0.5212968849332485
aerospike_ttl_percents{days="182",namespace="mynamespace",set=""} 0.4803277530550258
aerospike_ttl_percents{days="183",namespace="mynamespace",set=""} 0.5424878152150879
aerospike_ttl_percents{days="184",namespace="mynamespace",set=""} 0.7586353040898496
aerospike_ttl_percents{days="185",namespace="mynamespace",set=""} 0.48174048174048173
aerospike_ttl_percents{days="186",namespace="mynamespace",set=""} 0.4252313343222434
aerospike_ttl_percents{days="187",namespace="mynamespace",set=""} 0.5905205905205905
aerospike_ttl_percents{days="188",namespace="mynamespace",set=""} 0.49162958253867345
aerospike_ttl_percents{days="189",namespace="mynamespace",set=""} 0.6032351486896942
aerospike_ttl_percents{days="190",namespace="mynamespace",set=""} 0.41816769089496364
aerospike_ttl_percents{days="191",namespace="mynamespace",set=""} 0.7035388853570672
aerospike_ttl_percents{days="192",namespace="mynamespace",set=""} 0.6258388076569895
aerospike_ttl_percents{days="193",namespace="mynamespace",set=""} 0.5354241717878081
aerospike_ttl_percents{days="194",namespace="mynamespace",set=""} 0.5509641873278237
aerospike_ttl_percents{days="195",namespace="mynamespace",set=""} 0.6851734124461397
aerospike_ttl_percents{days="196",namespace="mynamespace",set=""} 0.7897153351698806
aerospike_ttl_percents{days="197",namespace="mynamespace",set=""} 0.8914318005227096
aerospike_ttl_percents{days="198",namespace="mynamespace",set=""} 0.9648936921664194
aerospike_ttl_percents{days="199",namespace="mynamespace",set=""} 1.3661086388359116
aerospike_ttl_percents{days="200",namespace="mynamespace",set=""} 4.690259235713781
aerospike_ttl_percents{days="201",namespace="mynamespace",set=""} 2.873490146217419
aerospike_ttl_percents{days="minBucket",namespace="mynamespace",set=""} 159
aerospike_ttl_percents{days="total",namespace="mynamespace",set=""} 70785
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
    	namespace:set comma delimited. Ex: 'myns:myset,myns2:myset3,myns3:,myns4:' - set optional, but colon is not
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
