# aerospike-ttl-exporter

A Prometheus exporter that scans Aerospike record TTLs and sizes, exporting them as histograms with configurable bucket boundaries.

## The problem

TTL (time-to-live) on a record dictates when it expires. If you store data until eviction, you need to measure the lowest bucket and trends of these TTLs over time.

Aerospike's built-in histogram dumps export 100 fixed-count buckets whose boundaries depend on the local min/max, so:

- Accuracy varies wildly between nodes.
- Bucket boundaries don't align across servers, forcing lossy re-bucketing in Grafana.
- Only record counts are available — no size distribution across TTL ranges.

## Solution

A custom exporter that samples records on each server on a schedule and exports TTL and size histograms to Prometheus with operator-controlled (or auto-fitted) bucket boundaries.

## Example queries

### How large are the records in the fresh TTL range?
```promql
# Scenario: default-ttl=33d
histogram_quantile(0.50, sum(rate(aerospike_ttl_kib_hist_bucket{namespace="myns"}[$__rate_interval])) by (le))
# Query result: 28.6
# Interpreted: 50% of the data has been written in the last (33-28.6) 4.4 days
```

### How many records are in the fresh TTL range?
```promql
# Scenario: default-ttl=33d
histogram_quantile(0.50, sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns"}[$__rate_interval])) by (le))
# Query result: 22.1
# Interpreted: 50% of the data has been written in the last (33-22.1) 10.9 days
```

### What percentage of records will expire in a week?
```promql
sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns",le="7",ttlUnit="days"}[$__rate_interval]))*100
/
sum(rate(aerospike_ttl_counts_hist_bucket{namespace="myns",le="+Inf"}[$__rate_interval]))
# Result: 13.1
```

### What percentage of data (by size) will expire in a week?
```promql
sum(rate(aerospike_ttl_kib_hist_bucket{namespace="myns",le="7",ttlUnit="days"}[$__rate_interval]))*100
/
sum(rate(aerospike_ttl_kib_hist_bucket{namespace="myns",le="+Inf"}[$__rate_interval]))
# Result: 1.4
```

### What's the exact p99 record size? (no histogram bucket rounding)
```promql
aerospike_ttl_size_bytes_quantiles{namespace="myns",quantile="0.99"}
# Result: 204
# Interpreted: 99% of records are ≤204 bytes — exact, not histogram-interpolated
```

### Conclusions: an abnormal distribution where the largest records are updated more often.

### How will my evict-void-time change if I evict 10% of my data earlier?

Useful when you're already evicting and need to forecast changes like:
- Records will become 10% larger
- You will lose 10% of capacity
- You will reduce your HWM by 10% (e.g. 50% to 45%)

```promql
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

## Metrics reference

All metrics are in the `aerospike_ttl_` namespace.

### Per-set histograms

| Metric | Labels | Description |
|--------|--------|-------------|
| `counts_hist` | `namespace`, `set`, `ttlUnit` | Records per TTL bucket. |
| `kib_hist` | `namespace`, `set`, `ttlUnit`, `storage_type` | KiB per TTL bucket, size-weighted (`storage_type=recordsize`). Requires `kbyteHistogramEnabled: true`. |
| `size_bytes_hist` | `namespace`, `set`, `metadata_op` | Record size distribution in raw bytes (`metadata_op=recordsize`). Requires `sizeHistogramEnabled: true`. Uses `sizeBuckets` config. |

### Per-set quantile summaries

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `ttl_quantiles` | Summary | `namespace`, `set`, `ttlUnit`, `quantile` | Exact quantile summary of record TTLs from the most recent complete scan (in display units). |
| `size_bytes_quantiles` | Summary | `namespace`, `set`, `quantile` | Exact quantile summary of record sizes (bytes) from the most recent complete scan. |
| `quantile_last_refresh_success_ts` | Gauge | `namespace`, `set` | Unix epoch when quantile summaries were last successfully computed. Use to detect stale data. |

Quantile summaries are computed from the full scan dataset — not streaming estimates — so they are exact for the sampled records. They are **double-buffered**: partial or failed scans never export; the prior successful run's values persist until the next run completes. By default p20/p50/p90/p99 are emitted; configure with `quantileTargets` (see below). Set `quantileTargets: []` to disable.

### Per-set gauges

| Metric | Labels | Description |
|--------|--------|-------------|
| `scan_time_seconds` | `namespace`, `set` | Wall-clock seconds the most recent scan took. |
| `scan_last_updated` | `namespace`, `set` | Unix epoch when the most recent scan finished. |
| `min_ttl_seconds` | `namespace`, `set` | Lowest record TTL (seconds) observed in the most recent scan. Non-expirable records excluded. |
| `max_ttl_seconds` | `namespace`, `set` | Highest record TTL (seconds) observed in the most recent scan. Non-expirable records excluded. |

### Per-namespace gauges

| Metric | Labels | Description |
|--------|--------|-------------|
| `default_ttl_seconds` | `namespace` | Namespace `default-ttl` as reported by Aerospike (`0` = never expire). Discovery mode only. |

### Global

| Metric | Labels | Description |
|--------|--------|-------------|
| `build_info` | `version` | Always `1`. The `version` label carries the release tag (or `dev`). |

### Example output

```
aerospike_ttl_build_info{version="dev"} 1
aerospike_ttl_default_ttl_seconds{namespace="test"} 0
aerospike_ttl_min_ttl_seconds{namespace="test",set="myset"} 86374
aerospike_ttl_max_ttl_seconds{namespace="test",set="myset"} 86374
aerospike_ttl_counts_hist_bucket{namespace="test",set="myset",ttlUnit="hours",le="23.76"} 0
aerospike_ttl_counts_hist_bucket{namespace="test",set="myset",ttlUnit="hours",le="24.384"} 200
aerospike_ttl_counts_hist_bucket{namespace="test",set="myset",ttlUnit="hours",le="+Inf"} 200
aerospike_ttl_counts_hist_sum{namespace="test",set="myset",ttlUnit="hours"} 4799.083333333327
aerospike_ttl_counts_hist_count{namespace="test",set="myset",ttlUnit="hours"} 200
aerospike_ttl_kib_hist_bucket{namespace="test",set="myset",storage_type="recordsize",ttlUnit="hours",le="23.76"} 0
aerospike_ttl_kib_hist_bucket{namespace="test",set="myset",storage_type="recordsize",ttlUnit="hours",le="24.384"} 16
aerospike_ttl_kib_hist_bucket{namespace="test",set="myset",storage_type="recordsize",ttlUnit="hours",le="+Inf"} 16
aerospike_ttl_kib_hist_sum{namespace="test",set="myset",storage_type="recordsize",ttlUnit="hours"} 382.4269531249998
aerospike_ttl_kib_hist_count{namespace="test",set="myset",storage_type="recordsize",ttlUnit="hours"} 16
aerospike_ttl_size_bytes_hist_bucket{metadata_op="recordsize",namespace="test",set="myset",le="34.56227054460177"} 0
aerospike_ttl_size_bytes_hist_bucket{metadata_op="recordsize",namespace="test",set="myset",le="203.19049958682461"} 200
aerospike_ttl_size_bytes_hist_bucket{metadata_op="recordsize",namespace="test",set="myset",le="+Inf"} 200
aerospike_ttl_size_bytes_hist_sum{metadata_op="recordsize",namespace="test",set="myset"} 16320
aerospike_ttl_size_bytes_hist_count{metadata_op="recordsize",namespace="test",set="myset"} 200
aerospike_ttl_ttl_quantiles{namespace="test",set="myset",ttlUnit="hours",quantile="0.2"} 23.99
aerospike_ttl_ttl_quantiles{namespace="test",set="myset",ttlUnit="hours",quantile="0.5"} 23.997
aerospike_ttl_ttl_quantiles{namespace="test",set="myset",ttlUnit="hours",quantile="0.9"} 23.997
aerospike_ttl_ttl_quantiles{namespace="test",set="myset",ttlUnit="hours",quantile="0.99"} 23.997
aerospike_ttl_ttl_quantiles_sum{namespace="test",set="myset",ttlUnit="hours"} 4799.08
aerospike_ttl_ttl_quantiles_count{namespace="test",set="myset",ttlUnit="hours"} 200
aerospike_ttl_size_bytes_quantiles{namespace="test",set="myset",quantile="0.2"} 81
aerospike_ttl_size_bytes_quantiles{namespace="test",set="myset",quantile="0.5"} 81
aerospike_ttl_size_bytes_quantiles{namespace="test",set="myset",quantile="0.9"} 82
aerospike_ttl_size_bytes_quantiles{namespace="test",set="myset",quantile="0.99"} 82
aerospike_ttl_size_bytes_quantiles_sum{namespace="test",set="myset"} 16320
aerospike_ttl_size_bytes_quantiles_count{namespace="test",set="myset"} 200
aerospike_ttl_quantile_last_refresh_success_ts{namespace="test",set="myset"} 1.782252482e+09
aerospike_ttl_scan_last_updated{namespace="test",set="myset"} 1.782252482e+09
aerospike_ttl_scan_time_seconds{namespace="test",set="myset"} 0
```

## Usage

```
Usage of ./aerospike-ttl-exporter:
  -configFile string
    Path to the config file for the exporter. (Default: "/etc/ttl-aerospike-exporter.yaml")
```

1. Grab a release from https://github.com/Alb0t/aerospike-ttl-exporter/releases
2. Create a config file. Start from a ready-to-edit example:
   - [`examples/manual.yaml`](examples/manual.yaml) — `autoDiscover: false`; you list sets and bucket boundaries explicitly.
   - [`examples/autodiscover.yaml`](examples/autodiscover.yaml) — `autoDiscover: true`; buckets fit automatically, no `monitor:` entries.
   - [`examples/autodiscover-with-override.yaml`](examples/autodiscover-with-override.yaml) — auto-discovery plus per-set `monitor:` overrides (exponential scale on one set, pinned static buckets on another).

   `conf.yaml` is the fully-commented reference for every field.
3. Run the binary: `./aerospike-ttl-exporter -configFile /path/to/conf.yaml`

The config file is YAML. There are **no default values** — any omitted or misspelled key gets Go's zero value for that type (e.g. `0` for `int`, `""` for `string`). Don't omit fields or misspell them.

## Operating modes

### Legacy mode (`autoDiscover: false`)

List every namespace/set and its TTL bucket boundaries explicitly under `monitor:`. Only those entries are scanned.

### Auto-discovery mode (`autoDiscover: true`)

The exporter asks Aerospike which namespaces and sets exist, reads each set's TTL distribution, and builds histogram bucket configuration automatically.

**How it works:**

1. Enumerates namespaces (`namespaces` info command) and the sets in each (`sets/<ns>`).
2. For each namespace, checks for set-less records (the "null set") by comparing namespace total objects to the sum of per-set objects. If set-less records exist, a synthetic null-set entry is added (scanned with a server-side filter to count only set-less records).
3. Reads each namespace's `default-ttl` and each set's TTL histogram (`histogram:namespace=<ns>;set=<set>;type=ttl`).
4. Fits histogram buckets to the observed min/max populated TTL. `discoveryBucketCount` sets the number of bins; `n+1` edges span `[minTTL, maxTTL]` inclusive so the densest top TTLs land in a real bucket, not `+Inf`. Spacing is **linear** by default; set `ttlBuckets.scale: exponential` (auto mode only) for geometric spacing when TTLs are heavily skewed.
5. `discoveryRangePaddingPct` extends each fitted top edge by that percentage. Aerospike's TTL histogram rescales dynamically, so between discovery passes the live max TTL can drift above the observed max and spill into `+Inf`; padding leaves headroom. Pair with a short `discoveryIntervalSecs` to re-fit before drift grows large.
6. Picks the display unit from the magnitude of the observed max TTL: `>2d → days`, `>2h → hours`, else `seconds`, then **quantizes** the fitted range to whole display units. Sub-unit drift between passes leaves the edges byte-identical, so the collector signature is unchanged and no resetting rebuild happens.

**Scheduling:** Discovery runs on its own interval (`discoveryIntervalSecs`, defaults to `frequencySecs`) independent of the scan cadence. It runs once synchronously at startup so metrics are populated before the first scan, then periodically. Only unregisters/re-registers collectors when the computed bucket signature actually changes — no churn when the distribution is stable.

**Resilience:** Each info command retries with exponential backoff (500ms → 4s) for up to 30 seconds. If any call fails after the retry window, the entire discovery pass is skipped and the previous registry is kept — a transient blip won't prune healthy sets.

**Pruning:** Sets that no longer exist on the Aerospike node are dropped from the registry, and their gauge series (`min_ttl_seconds`, `max_ttl_seconds`, `scan_time_seconds`, `scan_last_updated`) are deleted. The per-namespace `default_ttl_seconds` gauge is deleted once no sets survive in that namespace.

**Overrides:** `monitor:` entries whose `namespace` + `set` match a discovered set override the discovered/default config **field-by-field**. Only fields explicitly set in the entry replace the default; everything else stays discovered. Bucket config blocks (`ttlBuckets`, `sizeBuckets`) replace wholesale when overridden (no field-by-field merge within a block).

**Never-expire sets:** A set with `default-ttl=0` and an empty TTL histogram is treated as non-expirable — its TTL histograms (`counts_hist`, `kib_hist`) are skipped, but `size_bytes_hist` is still registered if enabled.

When `autoDiscover` is `false` the exporter behaves exactly as before and only scans the explicit `monitor:` entries.

## Bucket configuration

TTL histograms (`counts_hist`, `kib_hist`) and the size histogram (`size_bytes_hist`) share one unified bucket schema. Each set (or `discoveryDefaults`, or a `monitor:` override) carries up to two blocks:

- `ttlBuckets` — boundaries for the TTL histograms. Values may carry an `s`/`h`/`d` suffix; the suffix sets the `ttlUnit` label and the seconds divisor.
- `sizeBuckets` — boundaries for the record-size histogram, in raw bytes.

Both use the same `mode`-driven shape:

| Mode | Fields | Meaning |
|------|--------|---------|
| `static` | `static: [...]` | Explicit list of bucket boundaries. |
| `linear` | `start`, `width`, `count` | `prometheus.LinearBuckets(start, width, count)` |
| `exponential` | `min`, `max`, `count` | `prometheus.ExponentialBucketsRange(min, max, count)` |
| `auto` | `scale` (opt) | **ttlBuckets only.** Discovery fits buckets to the live TTL histogram (range quantized to whole display units for cross-pass stability). `scale: linear` (default) or `exponential`. Fatal if used on `sizeBuckets`. |

### Examples

```yaml
ttlBuckets:
  mode: static
  static: [1d, 3d, 5d, 7d, 14d]

ttlBuckets:
  mode: linear
  start: 180d
  width: 10d
  count: 10

sizeBuckets:
  mode: exponential
  min: 1
  max: 8389000
  count: 10
```

### Validation

Config is validated on load. Fatal startup errors for:

- `discoveryBucketCount <= 0` when `autoDiscover: true`
- `mode: auto` on `sizeBuckets`
- `mode: static` with empty list
- `mode: linear` missing `start`, `width`, or `count <= 0`
- `mode: exponential` with `min <= 0`, `max <= min`, or `count <= 0`
- `sizeHistogramEnabled: true` without a `sizeBuckets` mode
- `autoDiscover: false` with any `monitor:` entry missing an explicit `ttlBuckets` mode (or using `auto`/empty)
- `quantileTargets` with values outside `(0, 1)`

## Configuration reference

See `conf.yaml` for a fully commented example, or the ready-to-edit configs in [`examples/`](examples/).

### `service:` block

| Key | Type | Description |
|-----|------|-------------|
| `listenPort` | string | Address to bind the `/metrics` HTTP endpoint (e.g. `:9634`). |
| `aerospikeAddr` | string | Aerospike host to connect to. |
| `aerospikePort` | int | Aerospike port. |
| `skipNodeCheck` | bool | Skip local-node verification. **Dangerous in production** — disables the guard ensuring scans only hit the co-located node. |
| `failOnClusterChange` | bool | (Reserved.) |
| `frequencySecs` | int | Seconds between scan cycles. Scans do not overlap; if a cycle is still running when the next fires, it is skipped. |
| `verbose` | bool | Enable debug-level logging. |
| `username` | string | Aerospike username (omit if auth not enabled). |
| `password` | string | Aerospike password (only considered if `username` is set). |
| `autoDiscover` | bool | Enable auto-discovery mode. |
| `discoveryIntervalSecs` | int | Seconds between discovery/re-fit passes. Defaults to `frequencySecs` if unset or `<= 0`. |
| `discoveryBucketCount` | int | Number of linear TTL bins per discovered set. |
| `discoveryRangePaddingPct` | int | Percentage to extend each fitted range's top edge for headroom. `0` = none. |
| `discoveryDefaults` | monconf | Base scan/perf/feature settings applied to every discovered set (same shape as a `monitor:` entry, minus `namespace`/`set`). |

### `monitor:` entries

Each entry targets one namespace/set. In auto-discovery mode they act as per-set overrides (field-by-field). In legacy mode they are the only sets scanned.

| Key | Type | Description |
|-----|------|-------------|
| `namespace` | string | Aerospike namespace. |
| `set` | string | Set name. Use `null` for namespace-level scan (all records including null set). |
| `recordCount` | int | Stop scanning after this many exported records. `-1` = no cap. |
| `scanPercent` | float | Percentage of data to scan (approximated via `MaxRecords`). `0` or `-1` = disabled. Works alongside `recordCount`; scan stops at whichever limit is reached first. |
| `reportCount` | int | Log progress every N exported records (when `verbose: true`). |
| `scanTotalTimeout` | string | Go duration (e.g. `20m`, `1h30s`). Total scan timeout. |
| `scanSocketTimeout` | string | Per-socket timeout. |
| `policyTotalTimeout` | string | Policy total timeout (for metadata reads). |
| `policySocketTimeout` | string | Policy socket timeout. |
| `recordsPerSecond` | int | Server-side RPS throttle. `0` = unlimited. |
| `kbyteHistogramEnabled` | bool | Enable `kib_hist` (size-weighted TTL histogram). Bucket counts represent total KiB per TTL bucket, O(1) per record. |
| `sizeHistogramEnabled` | bool | Enable `size_bytes_hist` (record-size distribution). |
| `ttlBuckets` | bucketConfig | TTL histogram bucket config (see Bucket configuration). |
| `sizeBuckets` | bucketConfig | Size histogram bucket config (see Bucket configuration). |
| `quantileTargets` | []float64 | Quantile percentiles to compute for TTL and size summaries. Values in `(0, 1)`. Default: `[0.20, 0.50, 0.90, 0.99]`. Set to `[]` to disable quantile summaries entirely. |

## Debug logging

When `verbose: true`, the exporter logs each set's resolved config and scan lifecycle. In auto-discovery mode it also logs the fitted buckets per set:

```
level=info msg="Discovery: myns:myset — 11 buckets [50.4..67.5] days (default-ttl=604800s)"
level=info msg="Discovery: reconciled 33 set(s) across 2 namespace(s)"
level=info msg="Begin scan/inspection." namespace=myns set=myset
level=info msg="Scan complete." namespace=myns set=myset total(records exported)=42 totalInspected=42
```

## Read-only guarantee

The exporter only **scans** Aerospike — it never writes. Record size is read with a metadata-only `Operate` that carries `TTLDontUpdate`, so even the touched record's TTL/generation is left alone.

`scripts/gen-stability-test.sh` is the end-to-end proof. It is **hermetic**: spins up an ephemeral enterprise-edition Aerospike in Docker (single-node, no license needed), seeds 10 canary records with a positive TTL, runs the real exporter binary with all histogram types enabled (`counts_hist`, `kib_hist`, `size_bytes_hist`), then:

1. Scrapes `/metrics` and asserts every histogram has `>= 10` observations — the exporter must actually produce histograms for the gen check to be meaningful.
2. Re-reads `gen` for all 10 canaries. Any change = exporter wrote (FAIL).
3. Runs a negative control: deliberately writes a canary and confirms the gen comparison detects it, proving the harness can't false-pass.

## Testing

```bash
just test        # unit tests: bucket resolvers, gauges, discovery, config decode
just gen-check   # e2e read-only proof (hermetic; needs docker + go on PATH)
```

`just test` runs `go test ./...`. `just gen-check` runs the read-only safety test — it spins up a throwaway Aerospike in Docker and tears it down. Both need `docker` + `go` on PATH.

### Other `just` recipes

```bash
just build                    # cross-compile linux/amd64 binary
just deploy <hostname>        # build, scp, and run on a remote host
just run-remote <hostname>    # run locally against a remote Aerospike node
```

Set `AS_USER`/`AS_PASS` env vars to override Aerospike credentials for `deploy` and `run-remote`.

## Graceful shutdown

The exporter handles `SIGINT`/`SIGTERM`: stops scheduler jobs, drains the HTTP server (5s timeout), and exits cleanly.
