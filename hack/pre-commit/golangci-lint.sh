#!/usr/bin/env bash
# Run the repository's pinned golangci-lint. Prefers the binary that `make
# lint` builds into ./bin (it carries the logcheck plugin from .custom-gcl.yml);
# falls back to one on PATH, and skips with a warning if neither is present so
# a fresh clone is not blocked from committing before `make lint` has ever run.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
local_bin="${repo_root}/bin/golangci-lint"

if [ -x "${local_bin}" ]; then
	exec "${local_bin}" run
elif command -v golangci-lint >/dev/null 2>&1; then
	echo "note: using golangci-lint from PATH; run 'make lint' for the pinned build." >&2
	exec golangci-lint run
else
	echo "warning: golangci-lint not found; skipping. Run 'make lint' to install it." >&2
	exit 0
fi
