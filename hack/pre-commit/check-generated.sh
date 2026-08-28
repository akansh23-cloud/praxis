#!/usr/bin/env bash
# Assert that the committed generated artifacts match what the current markers
# produce. The CRDs and RBAC in config/ are the contract the API validation
# checkpoint rests on; a types change committed without them is a silent
# divergence between what the code says and what the API server enforces.
#
# Only genuinely generated paths are checked. config/samples/ and the rest of
# config/ are hand-authored and must not be flagged here.
set -euo pipefail

repo_root="$(git rev-parse --show-toplevel)"
cd "${repo_root}"

# Everything controller-gen owns. Keep in sync with the manifests/generate
# targets in the Makefile.
GENERATED_PATHS=(
	'api/**/zz_generated.*.go'
	'config/crd/bases'
	'config/rbac/role.yaml'
	'config/webhook/manifests.yaml'
)

# Only consider paths that actually exist in this repository, so a project
# without webhooks does not fail on a missing pathspec.
existing=()
for p in "${GENERATED_PATHS[@]}"; do
	if git ls-files --error-unmatch -- "${p}" >/dev/null 2>&1; then
		existing+=("${p}")
	fi
done

if [ ${#existing[@]} -eq 0 ]; then
	echo "warning: no generated artifacts are tracked; nothing to check." >&2
	exit 0
fi

if ! make generate manifests >/dev/null 2>&1; then
	echo "error: 'make generate manifests' failed; run it directly to see why." >&2
	exit 1
fi

# pre-commit stashes unstaged work, so the working tree here is the staged
# state plus whatever regeneration just wrote. A diff therefore means exactly
# one thing: the staged generated output was stale.
if ! git diff --quiet -- "${existing[@]}"; then
	echo "error: generated files are out of date. Run:" >&2
	echo >&2
	echo "    make generate manifests" >&2
	echo >&2
	echo "and stage the result. Files that changed:" >&2
	git diff --name-only -- "${existing[@]}" >&2
	exit 1
fi
