#!/usr/bin/env bash
# gen-stability-test.sh — THE ULTIMATE SAFETY TEST (hermetic).
#
# The TTL exporter MUST be strictly read-only. Aerospike bumps a record's
# generation counter (gen) on every write, so if gen changes after the exporter
# scans a set, the exporter performed a write — a VERY DANGEROUS bug. This is a
# property of the scan code path, not of any one cluster, so an ephemeral
# community-edition node in docker proves it just as well as prod, with no creds
# or external host.
#
# Seeds 10 canary records, scans them with ALL histogram types enabled
# (expiry_count, expiry_bytes, size_bytes), then re-reads gen for every record. Any gen change => the
# exporter wrote (FAIL, loud). After the read-only assertion, verifies:
#   1. All 10 records' gens are unchanged.
#   2. Prometheus /metrics shows >= 10 observations for each histogram type,
#      proving the histograms were actually populated (a test that "passes"
#      without producing histograms proves nothing).
#
# NEGATIVE CONTROL: a test that can only ever PASS is worthless. After the
# read-only assertion, the harness deliberately writes one canary and confirms
# the SAME gen comparison now reports a change — proving it can actually detect
# a write. Every gen read is also guarded against an empty result (missing
# canary / broken aql query), which would otherwise compare equal and false-PASS.
#
# Usage: scripts/gen-stability-test.sh        (RUN_SECS=30 to lengthen the scan)
# Requires: docker, go, curl on PATH. aql runs inside the container.
set -euo pipefail

CANARY_COUNT=10
RUN_SECS="${RUN_SECS:-20}"
CONTAINER="ttl-exporter-test-asd"
IMAGE="aerospike/aerospike-server-enterprise:latest"
LOG="/tmp/gen-test-exporter.log"
BIN="/tmp/gen-test-exporter.bin"
METRICS_PORT=9634

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"
export AEROSPIKE_HOST="127.0.0.1"
export AQL="docker exec -i ${CONTAINER} aql"

exporter_pid=""
cleanup() {
  [[ -n "${exporter_pid}" ]] && kill "${exporter_pid}" 2>/dev/null || true
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
  rm -f "${BIN}" 2>/dev/null || true
}
trap cleanup EXIT

# read_gens outputs one "canaryNN=gen" line per record. The caller captures this
# into an associative array. Exits non-zero if any canary is missing.
read_gens() {
  local phase="$1" missing=0
  for i in $(seq -w 1 "${CANARY_COUNT}"); do
    local pk="canary${i}"
    local gen
    gen=$(${AQL} -h "${AEROSPIKE_HOST}" -c "SET OUTPUT JSON; SET RECORD_PRINT_METADATA TRUE; SELECT * FROM test.myset WHERE PK = '${pk}'" \
      | grep -o '"gen": *[0-9]*' | grep -o '[0-9]*$' || true)
    if [[ -z "${gen}" ]]; then
      echo "FAIL: could not read gen for ${pk} (${phase}). Canary missing or aql query broken." >&2
      missing=1
    else
      echo "${pk}=${gen}"
    fi
  done
  [[ "${missing}" -eq 0 ]] || return 1
}

echo "==> starting ${IMAGE} (container ${CONTAINER})..."
docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
docker run -d --name "${CONTAINER}" -p 3000-3002:3000-3002 "${IMAGE}" >/dev/null

echo "==> waiting for Aerospike to accept client ops..."
ready=""
for _ in $(seq 1 90); do
  if docker exec -i "${CONTAINER}" aql -h 127.0.0.1 -c "show namespaces" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
[[ -n "${ready}" ]] || { echo "FAIL: Aerospike did not become ready in time." >&2; exit 1; }

# Crank nsup sweep and histogram rebuild to 5s so the TTL histogram is
# available within seconds of record insertion (defaults are 120s and 3600s).
docker exec -i "${CONTAINER}" asinfo -v "set-config:context=namespace;id=test;nsup-period=5" >/dev/null
docker exec -i "${CONTAINER}" asinfo -v "set-config:context=namespace;id=test;nsup-hist-period=5" >/dev/null

echo "==> seeding ${CANARY_COUNT} canary records..."
"${SCRIPT_DIR}/insert.sh" >/dev/null

# nsup builds the TTL histogram in the background; auto-discovery needs it.
# Poll until the histogram response contains bucket-width (not "hist-unavailable").
echo "==> waiting for nsup to build TTL histogram..."
hist_ready=""
for _ in $(seq 1 60); do
  hist_raw=$(docker exec -i "${CONTAINER}" asinfo -v "histogram:namespace=test;set=myset;type=ttl" 2>/dev/null || true)
  if echo "${hist_raw}" | grep -q "bucket-width"; then
    hist_ready=1
    break
  fi
  sleep 1
done
[[ -n "${hist_ready}" ]] || { echo "FAIL: TTL histogram not available after 60s. nsup may not have run." >&2; exit 1; }
echo "   histogram ready: $(echo "${hist_raw}" | head -c 80)..."

echo "==> reading gen for all ${CANARY_COUNT} canaries (before scan)..."
declare -A gens_before
while IFS='=' read -r pk gen; do
  gens_before["${pk}"]="${gen}"
done < <(read_gens "before scan")
echo "   gens before: ${gens_before[*]}"

echo "==> building exporter..."
go build -o "${BIN}" .
"${BIN}" -configFile "${SCRIPT_DIR}/docker-test.yaml" >"${LOG}" 2>&1 &
exporter_pid=$!
echo "==> exporter pid ${exporter_pid} scanning for ${RUN_SECS}s (log: ${LOG})..."
sleep 3

if ! kill -0 "${exporter_pid}" 2>/dev/null; then
  echo "FAIL: exporter exited before scanning. Last log lines:" >&2
  tail -n 20 "${LOG}" >&2
  exit 1
fi

sleep "$(( RUN_SECS > 3 ? RUN_SECS - 3 : 1 ))"

# --- Histogram verification (while exporter is still alive) ---
echo "==> verifying histogram output at :${METRICS_PORT}/metrics..."
metrics=$(curl -sf "http://localhost:${METRICS_PORT}/metrics") || {
  echo "FAIL: could not scrape /metrics from exporter." >&2
  tail -n 20 "${LOG}" >&2
  exit 1
}

check_histogram() {
  local name="$1" min_samples="$2"
  local count
  count=$(echo "${metrics}" | grep "^${name}_count" | awk '{s+=$2} END {printf "%.0f", s+0}')
  if [[ -z "${count}" || "${count}" -lt "${min_samples}" ]]; then
    echo "FAIL: ${name} has ${count:-0} observations, need >= ${min_samples}. Exporter did not produce this histogram; gen-stability result would be meaningless." >&2
    echo "--- relevant /metrics lines ---" >&2
    echo "${metrics}" | grep "^${name}" >&2 || echo "(none)" >&2
    exit 1
  fi
  echo "   ${name}: ${count} observations (>= ${min_samples} required)"
}

check_histogram "aerospike_ttl_expiry_count_hist" "${CANARY_COUNT}"
# expiry_bytes_hist _count is total bytes (size-weighted), NOT record count, so
# with 10 canaries it will trivially clear this threshold.
check_histogram "aerospike_ttl_expiry_bytes_hist" "${CANARY_COUNT}"
check_histogram "aerospike_ttl_size_bytes_hist" "${CANARY_COUNT}"

echo "==> sample /metrics lines (illustrative output):"
echo "${metrics}" | grep -E "^aerospike_ttl_"

# --- Stop exporter, check gens ---
kill "${exporter_pid}" 2>/dev/null || true
wait "${exporter_pid}" 2>/dev/null || true
exporter_pid=""

echo "==> reading gen for all ${CANARY_COUNT} canaries (after scan)..."
declare -A gens_after
while IFS='=' read -r pk gen; do
  gens_after["${pk}"]="${gen}"
done < <(read_gens "after scan")
echo "   gens after: ${gens_after[*]}"

failed=0
for pk in "${!gens_before[@]}"; do
  before="${gens_before[${pk}]}"
  after="${gens_after[${pk}]}"
  if [[ "${before}" != "${after}" ]]; then
    echo "FAIL: ${pk} gen changed ${before} -> ${after}. Exporter performed a WRITE. VERY DANGEROUS." >&2
    failed=1
  fi
done
[[ "${failed}" -eq 0 ]] || exit 1
echo "==> PASS (read-only): all ${CANARY_COUNT} gens unchanged."

# --- Negative control: prove the harness can detect a write ---
echo "==> negative control: writing canary01 deliberately to confirm a write is detectable..."
${AQL} -h "${AEROSPIKE_HOST}" -c "SET RECORD_TTL 86400; INSERT INTO test.myset (PK,mybin) VALUES ('canary01','MODIFIED')" >/dev/null
control_gen=$(${AQL} -h "${AEROSPIKE_HOST}" -c "SET OUTPUT JSON; SET RECORD_PRINT_METADATA TRUE; SELECT * FROM test.myset WHERE PK = 'canary01'" \
  | grep -o '"gen": *[0-9]*' | grep -o '[0-9]*$' || true)
if [[ -z "${control_gen}" ]]; then
  echo "FAIL: could not read canary01 gen after deliberate write." >&2
  exit 1
fi
echo "==> gen after write: ${control_gen}"
if [[ "${control_gen}" == "${gens_after[canary01]}" ]]; then
  echo "FAIL: deliberate write did not move gen (${gens_after[canary01]} -> ${control_gen}). The harness CANNOT detect writes; the read-only result is meaningless." >&2
  exit 1
fi

echo "PASS: exporter is read-only (all ${CANARY_COUNT} gens unchanged) AND produced all histogram types AND the harness proved it can detect a write (canary01 gen ${gens_after[canary01]} -> ${control_gen})."
