#!/usr/bin/env bash
# git-mirror, Copyright (c) 2026 YouSysAdmin
# SPDX-License-Identifier: MIT

# Ensure each staged text file ends with exactly one newline.
set -euo pipefail

[[ $# -eq 0 ]] && exit 0

for f in "$@"; do
  [[ -f $f && ! -L $f ]] || continue
  # Skip empty files.
  [[ -s $f ]] || continue
  # Append a newline if the last byte isn't one.
  if [[ "$(tail -c 1 -- "$f" | LC_ALL=C od -An -tu1 | tr -d ' \n')" != "10" ]]; then
    printf '\n' >>"$f"
  fi
done
