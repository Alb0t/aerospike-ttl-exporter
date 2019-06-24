# aerospike-ttl-prom-exporter

# The problem:
tl;dr - this allows us to measure storage capacity.

TTL (time-to-live) on a record dictates when the record will expire, and if evicting we need to measure the lowest bucket and trends of these ttls.

The data currently exported by Aerospike histogram dumps is not accurate enough to give us the granularity we need to look at these marks accurately. The histogram exports 100 buckets, always, and depending on the min/max of that local system we can get wildly different metrics between systems. This also means that the accuracy of these metrics can be very low, and further making the problem worse - we do not have a way to line up the 'bucket' boundaries of the exported histograms, so we have to decrease accuracy further by lumping the ranges into different buckets that line up in grafana.

# Solution:
* Write a custom exporter that takes a small 1% sample on each server on a scheduled task, and exports that data to prometheus.


Example output:
```
expirationTTL{days="168"} 20196
expirationTTL{days="169"} 79970
expirationTTL{days="170"} 52968
expirationTTL{days="171"} 59885
expirationTTL{days="172"} 71291
expirationTTL{days="173"} 64535
expirationTTL{days="174"} 82774
expirationTTL{days="175"} 59456
expirationTTL{days="176"} 67994
expirationTTL{days="177"} 46504
expirationTTL{days="178"} 80302
expirationTTL{days="179"} 73785
expirationTTL{days="180"} 4368
expirationTTL{days="181"} 4060
expirationTTL{days="182"} 4723
expirationTTL{days="183"} 6333
expirationTTL{days="184"} 3829
expirationTTL{days="185"} 4380
expirationTTL{days="186"} 4117
expirationTTL{days="187"} 3678
expirationTTL{days="188"} 3763
expirationTTL{days="189"} 5126
expirationTTL{days="190"} 3966
expirationTTL{days="191"} 5195
expirationTTL{days="192"} 6649
expirationTTL{days="193"} 4549
expirationTTL{days="194"} 5018
expirationTTL{days="195"} 4380
expirationTTL{days="196"} 5542
expirationTTL{days="197"} 5389
expirationTTL{days="198"} 4533
expirationTTL{days="199"} 7036
expirationTTL{days="200"} 5160
expirationTTL{days="201"} 5204
expirationTTL{days="202"} 5672
expirationTTL{days="203"} 6582
expirationTTL{days="204"} 6638
expirationTTL{days="205"} 6806
expirationTTL{days="206"} 8324
expirationTTL{days="207"} 8815
expirationTTL{days="208"} 12652
expirationTTL{days="209"} 76894
expirationTTL{days="210"} 1
```
