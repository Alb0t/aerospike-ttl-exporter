#!/usr/bin/env bash
# Write the canary record (with a positive TTL so it is expirable), bumping its
# gen. The TTL makes the scan exercise the read-only metadata Operate path; see
# insert.aql for why. Run this to (re)seed the gen-stability test.
# AQL overrides the client command (e.g. "docker exec -i <container> aql").
set -euo pipefail
cd "$(dirname "$0")"
${AQL:-aql} -h "${AEROSPIKE_HOST:-localhost}" < insert.aql
