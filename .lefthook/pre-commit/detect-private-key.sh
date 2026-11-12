#!/usr/bin/env bash
# git-mirror, Copyright (c) 2026 YouSysAdmin
# SPDX-License-Identifier: MIT

# Fail if any staged file contains a PEM-style private key header.
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

PATTERN='-----BEGIN ([A-Z]+ )?PRIVATE KEY-----'

hits=$(LC_ALL=C grep -lE -e "$PATTERN" -- "$@" 2>/dev/null || true)
if [[ -n $hits ]]; then
  echo "Private key material detected in:" >&2
  # shellcheck disable=SC2086
  printf '  %s\n' $hits >&2
  exit 1
fi
