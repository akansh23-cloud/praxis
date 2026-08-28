#!/usr/bin/env bash
# Praxis Phase 1 demo — the lifecycle walk and the structural rejections.
#
# What it shows, in order:
#   1. Incident → evidence hash → RemediationPlan → AwaitingApproval,
#      with every condition visible;
#   2. the hash-bound annotation approval round-trip;
#   3. structural rejection: editing an admitted plan's spec is refused
#      by the API server (CEL transition rule);
#   4. structural rejection: an out-of-vocabulary verb (DeleteNamespace)
#      is refused at admission and never persisted.
#
# Prerequisites: a cluster (make kind-up), CRDs (make install), and a
# running manager (make dev in another terminal). Run from the repo root.
# The script resets its own objects first, so it runs clean repeatedly.
#
# Recording: `make demo-record` wraps this script with asciinema.
# DEMO_PAUSE (seconds, default 1) sets the narration pause; 0 for tests.
set -euo pipefail

PAUSE="${DEMO_PAUSE:-1}"
INCIDENT=checkout-oomkill
PLAN=checkout-oomkill-7f3a2c

say() { printf '\n\033[1m# %s\033[0m\n' "$*"; sleep "$PAUSE"; }

run() {
  printf '\033[36m$ %s\033[0m\n' "$*"
  "$@"
  sleep "$PAUSE"
}

# refused runs a command the API server MUST reject; the server's message —
# written to be read — stays on the terminal.
refused() {
  printf '\033[36m$ %s\033[0m\n' "$*"
  if "$@"; then
    printf '\033[31mUNEXPECTED: the API server accepted this. That is a Phase 1 bug.\033[0m\n'
    exit 1
  fi
  printf '\033[32m^ refused, as designed\033[0m\n'
  sleep "$PAUSE"
}

conditions() {
  run kubectl get remediationplan "$PLAN" -o \
    jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\n"}{end}'
}

[ -f config/samples/praxis_v1alpha1_remediationplan.yaml ] || {
  echo "Run this from the repository root." >&2
  exit 1
}
kubectl get crd remediationplans.praxis.dev >/dev/null 2>&1 || {
  echo "The praxis CRDs are not installed — run: make install" >&2
  exit 1
}

# The hash the sample plan cites, read from the sample itself so the demo
# and the fixture can never drift apart.
HASH=$(awk '/evidenceBundleHash:/ {print $2}' config/samples/praxis_v1alpha1_remediationplan.yaml)

echo "(resetting any previous demo state)"
kubectl delete remediationplan "$PLAN" --ignore-not-found --wait >/dev/null
kubectl delete incident "$INCIDENT" --ignore-not-found --wait >/dev/null

say "PRAXIS PHASE 1 — the lifecycle, and what it refuses to do"

say "An incident fired. It declares WHERE remediation may act (spec.scope):"
run kubectl apply -f config/samples/praxis_v1alpha1_incident.yaml

say "Evidence gets collected and fingerprinted (hand-set until Phase 3 — status subresource only):"
run kubectl patch incident "$INCIDENT" --subresource=status --type=merge \
  -p "{\"status\":{\"evidenceBundleHash\":\"$HASH\"}}"

say "A remediation plan citing exactly that evidence:"
run kubectl apply -f config/samples/praxis_v1alpha1_remediationplan.yaml

say "The controller walks it Pending → Validating → AwaitingApproval:"
if ! kubectl wait --for=jsonpath='{.status.phase}'=AwaitingApproval \
  "remediationplan/$PLAN" --timeout=60s; then
  echo "The plan never reached AwaitingApproval — is the manager running? (make dev)" >&2
  exit 1
fi
run kubectl get remediationplan "$PLAN"
conditions

say "Approval is bound to a hash of the exact plan + evidence (LLD §8):"
boundTo=$(kubectl get remediationplan "$PLAN" -o jsonpath='{.status.approval.boundTo}')
printf '  boundTo = %s\n' "$boundTo"

say "A human approves by naming that hash — anything else is void:"
run kubectl annotate --overwrite remediationplan "$PLAN" \
  "praxis.dev/approve=$boundTo" "praxis.dev/approved-by=${USER:-demo}"
run kubectl wait --for=condition=Approved "remediationplan/$PLAN" --timeout=60s
run kubectl get remediationplan "$PLAN"

say "STRUCTURAL REJECTION 1 — an approved plan cannot be edited into a different plan:"
refused kubectl patch remediationplan "$PLAN" --type=merge \
  -p '{"spec":{"evidenceBundleHash":"sha256:abababababababababababababababababababababababababababababababab"}}'

say "STRUCTURAL REJECTION 2 — a verb outside the closed vocabulary cannot exist:"
refused kubectl apply -f docs/demo/delete-namespace-plan.yaml

say "...and nothing was persisted:"
refused kubectl get remediationplan demo-forbidden-plan

say "Phase 1, end to end: validated lifecycle, hash-bound approval, and an API surface that refuses what it should."
