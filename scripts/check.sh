#!/usr/bin/env bash
# Read the canary record's metadata (gen/ttl) as JSON.
# AQL overrides the client command (e.g. "docker exec -i <container> aql") so the
# same wrapper works against host aql or an aql inside a container.
set -euo pipefail
cd "$(dirname "$0")"
${AQL:-aql} -h "${AEROSPIKE_HOST:-localhost}" < check.aql | grep -A999999 '\['
