#!/usr/bin/env bash
# Fail if any staged Go file is not gofmt-clean. Reports the offenders rather
# than rewriting them, so the commit that fails is the commit you inspect.
set -euo pipefail

unformatted="$(gofmt -l "$@")"
if [ -n "${unformatted}" ]; then
	echo "These files are not gofmt-clean:" >&2
	echo "${unformatted}" >&2
	echo >&2
	echo "Fix with: gofmt -w ${unformatted//$'\n'/ }" >&2
	exit 1
fi
