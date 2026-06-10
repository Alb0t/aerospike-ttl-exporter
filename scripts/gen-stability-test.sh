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
# Spins up Aerospike in docker, seeds a canary record, scans it with the
# exporter for a bounded window, then re-reads gen. Unchanged => read-only
# (PASS); changed => the exporter wrote (FAIL, loud). Tears the container down.
#
# NEGATIVE CONTROL: a test that can only ever PASS is worthless. After the
# read-only assertion, the harness deliberately writes the canary and confirms
# the SAME gen comparison now reports a change — proving it can actually detect
# a write. Every gen read is also guarded against an empty result (missing
# canary / broken check.sh), which would otherwise compare equal and false-PASS.
#
# Usage: scripts/gen-stability-test.sh        (RUN_SECS=30 to lengthen the scan)
# Requires: docker, go on PATH. aql runs inside the container (no host aql needed).
set -euo pipefail

RUN_SECS="${RUN_SECS:-20}"
CONTAINER="ttl-exporter-test-asd"
IMAGE="aerospike/aerospike-server:latest"
LOG="/tmp/gen-test-exporter.log"
BIN="/tmp/gen-test-exporter.bin"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
cd "${REPO_ROOT}"
# Route aql through the container's own client: no host aql, and no astools.conf
# creds to trip community edition's no-auth handshake. localhost = the asd.
export AEROSPIKE_HOST="127.0.0.1"
export AQL="docker exec -i ${CONTAINER} aql"

exporter_pid=""
cleanup() {
  [[ -n "${exporter_pid}" ]] && kill "${exporter_pid}" 2>/dev/null || true
  docker rm -f "${CONTAINER}" >/dev/null 2>&1 || true
  rm -f "${BIN}" 2>/dev/null || true
}
trap cleanup EXIT

read_gen() { "${SCRIPT_DIR}/check.sh" | grep -o '"gen": *[0-9]*' | grep -o '[0-9]*$'; }

# require_gen fails loud when a gen read came back empty. A missing or unreadable
# canary would otherwise make before==after=="" compare equal and FALSE-PASS the
# whole test. $1 = the read value, $2 = phase label for the message.
require_gen() {
  if [[ -z "$1" ]]; then
    echo "FAIL: could not read canary gen (${2}). Canary missing or check.sh broken; gen-stability result would be meaningless." >&2
    exit 1
  fi
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

"${SCRIPT_DIR}/insert.sh" >/dev/null # seed canary
before="$(read_gen)"
require_gen "${before}" "before scan"
echo "==> gen before scan: ${before}"

# Build + run the binary directly (not `go run .`): killing `go run` orphans the
# child that holds the listen port, breaking the next run. The binary is the pid
# we kill.
echo "==> building exporter..."
go build -o "${BIN}" .
"${BIN}" -configFile "${SCRIPT_DIR}/docker-test.yaml" >"${LOG}" 2>&1 &
exporter_pid=$!
echo "==> exporter pid ${exporter_pid} scanning for ${RUN_SECS}s (log: ${LOG})..."
sleep 3
# Liveness guard: a dead exporter never scans, so an unchanged gen would be a
# false pass. Fail loud instead.
if ! kill -0 "${exporter_pid}" 2>/dev/null; then
  echo "FAIL: exporter exited before scanning. Last log lines:" >&2
  tail -n 20 "${LOG}" >&2
  exit 1
fi
sleep "$(( RUN_SECS > 3 ? RUN_SECS - 3 : 1 ))"
kill "${exporter_pid}" 2>/dev/null || true
wait "${exporter_pid}" 2>/dev/null || true
exporter_pid=""

after="$(read_gen)"
require_gen "${after}" "after scan"
echo "==> gen after scan:  ${after}"

if [[ "${before}" != "${after}" ]]; then
  echo "FAIL: gen changed ${before} -> ${after}. Exporter performed a WRITE. VERY DANGEROUS." >&2
  exit 1
fi
echo "==> PASS (read-only): gen unchanged (${after})."

# NEGATIVE CONTROL: prove the assertion above can actually fire. Deliberately
# write the canary (insert bumps gen) with the exporter already dead, then run
# the SAME read+compare. If gen does NOT move here, the harness cannot detect a
# write, so the read-only PASS above is meaningless — fail loud.
echo "==> negative control: writing canary deliberately to confirm a write is detectable..."
"${SCRIPT_DIR}/insert.sh" >/dev/null
control="$(read_gen)"
require_gen "${control}" "after deliberate write"
echo "==> gen after write: ${control}"
if [[ "${control}" == "${after}" ]]; then
  echo "FAIL: deliberate write did not move gen (${after} -> ${control}). The harness CANNOT detect writes; the read-only result is meaningless." >&2
  exit 1
fi
echo "PASS: exporter is read-only (gen ${after}) AND the harness proved it can detect a write (gen ${after} -> ${control})."
