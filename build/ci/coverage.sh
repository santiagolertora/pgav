#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${root}"

config="${root}/build/ci/coverage.yml"
threshold="$(awk '/^threshold:/{print $2; exit}' "${config}")"
packages="$(awk '/^  - /{print $2}' "${config}")"

if [[ -z "${threshold}" ]]; then
  echo "coverage: missing threshold in ${config}" >&2
  exit 1
fi

cover_file="${root}/cover.out"
# shellcheck disable=SC2086
go test -race -count=1 -covermode=atomic -coverprofile="${cover_file}" ${packages}

total="$(go tool cover -func="${cover_file}" | awk '/^total:/{gsub(/%/, "", $3); print $3}')"
awk -v total="${total}" -v threshold="${threshold}" 'BEGIN {
  if (total + 0 < threshold + 0) {
    printf "coverage: %.1f%% is below threshold %s%%\n", total, threshold > "/dev/stderr"
    exit 1
  }
  printf "coverage: %.1f%% (threshold %s%%)\n", total, threshold
}'
