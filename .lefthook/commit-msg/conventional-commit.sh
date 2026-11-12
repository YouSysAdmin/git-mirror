#!/usr/bin/env bash
# git-mirror, Copyright (c) 2026 YouSysAdmin
# SPDX-License-Identifier: MIT

# Enforce Conventional Commits on the subject line. Lefthook
# passes the path to the commit-message file as $1.
set -euo pipefail

msg_file="${1:?commit message file path required}"

# First non-empty, non-comment line is the subject.
subject="$(grep -vE '^[[:space:]]*#' -- "$msg_file" | grep -vE '^[[:space:]]*$' | head -n1 || true)"

# Allow git-generated merge/revert/fixup/squash subjects.
case "$subject" in
"Merge "* | "Revert "* | "fixup! "* | "squash! "*) exit 0 ;;
esac

pattern='^(feat|fix|chore|docs|refactor|test|perf|build|ci|style|revert)(\([a-z0-9_.-]+\)|\[[a-z0-9_.-]+\])?!?: .+'

if [[ ! $subject =~ $pattern ]]; then
  echo "error: commit subject must follow Conventional Commits:" >&2
  echo "    <type>[scope][!]: <short summary>" >&2
  echo "  types: feat fix chore docs refactor test perf build ci style revert" >&2
  echo "  got:   ${subject:-<empty>}" >&2
  exit 1
fi
