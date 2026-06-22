#!/usr/bin/env bash
# release.sh — cut a release. The git tag is the single source of truth for the
# version: goreleaser injects it into main.buildVersion (see .goreleaser.yml), so
# there is no version string to hand-edit in source.
#
# Gates the release on a clean tree, gofmt, vet, and tests; refuses to reuse an
# existing tag; pushes the tag; then runs goreleaser.
#
# Usage: scripts/release.sh vX.Y.Z
set -euo pipefail

vers="${1:?usage: scripts/release.sh vX.Y.Z}"
[[ "${vers}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "version must look like vX.Y.Z, got '${vers}'"; exit 1; }

cd "$(cd "$(dirname "$0")/.." && pwd)" # repo root

[[ -z "$(git status --porcelain)" ]] || { echo "working tree dirty — commit or stash first"; exit 1; }

branch="$(git rev-parse --abbrev-ref HEAD)"
git fetch origin "${branch}" --quiet
local_sha="$(git rev-parse HEAD)"
remote_sha="$(git rev-parse "origin/${branch}")"
if [[ "${local_sha}" != "${remote_sha}" ]]; then
  echo "local ${branch} (${local_sha:0:7}) != origin/${branch} (${remote_sha:0:7}) — pull or push first"
  exit 1
fi

if git tag --points-at HEAD | grep -q '^v'; then
  echo "HEAD already tagged: $(git tag --points-at HEAD | grep '^v' | tr '\n' ' ')— bump a commit first"
  exit 1
fi
unformatted="$(gofmt -l .)"; [[ -z "${unformatted}" ]] || { echo "gofmt needed: ${unformatted}"; exit 1; }
go vet ./...
go test ./...

git rev-parse "${vers}" >/dev/null 2>&1 && { echo "tag ${vers} already exists — bump the version"; exit 1; }

read -rp "release ${vers} from $(git rev-parse --short HEAD)? [y/N] " ans
[[ "${ans}" == "y" || "${ans}" == "Y" ]] || { echo "aborted"; exit 1; }

git tag -a "${vers}" -m "${vers}"
git push origin "${vers}"
goreleaser release --clean
