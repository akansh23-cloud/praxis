#!/usr/bin/env bash
# Chainsaw e2e orchestration: kind cluster → CRDs → manager → chainsaw.
# Invoked by `make test-e2e-chainsaw`, which supplies KIND/KUBECTL/CHAINSAW.
#
# The manager runs ON THE HOST, as a plain process against the cluster's
# kubeconfig, not as an in-cluster Deployment. That is the simpler of the two
# options and it is sufficient: everything Phase 1 asserts — CRD admission
# behaviour and the controller's status writes — is identical wherever the
# process runs. The in-cluster path (image build, kind image load, deploy,
# RBAC) costs minutes per CI run and exercises nothing Phase 1 claims; it
# becomes load-bearing in Phase 5, when ServiceAccounts and NetworkPolicies
# are themselves the test subject, and the scaffold Ginkgo suite
# (make test-e2e) already covers "the Deployment comes up" on demand.
set -euo pipefail

KIND="${KIND:-kind}"
KUBECTL="${KUBECTL:-kubectl}"
CHAINSAW="${CHAINSAW:-bin/chainsaw}"

# The cluster is deliberately not the dev cluster ("praxis") nor the scaffold
# e2e cluster ("praxis-test-e2e"): this suite must never tear down either.
CLUSTER="${E2E_CLUSTER:-praxis-chainsaw}"
HEALTH_PORT="${E2E_HEALTH_PORT:-18081}"

for tool in "$KIND" "$KUBECTL" "$CHAINSAW" go; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "Required tool '$tool' is not available." >&2
    exit 1
  }
done

WORK="$(mktemp -d)"
export KUBECONFIG="$WORK/kubeconfig"
CREATED_CLUSTER=0
MANAGER_PID=""

cleanup() {
  status=$?
  if [[ -n "$MANAGER_PID" ]] && kill -0 "$MANAGER_PID" 2>/dev/null; then
    kill "$MANAGER_PID" 2>/dev/null || true
    wait "$MANAGER_PID" 2>/dev/null || true
  fi
  if [[ $status -ne 0 && -s "$WORK/manager.log" ]]; then
    echo "--- manager log (last 60 lines) ---" >&2
    tail -n 60 "$WORK/manager.log" >&2
  fi
  if [[ "$CREATED_CLUSTER" == 1 && "${E2E_KEEP_CLUSTER:-0}" != 1 ]]; then
    "$KIND" delete cluster --name "$CLUSTER" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

if "$KIND" get clusters 2>/dev/null | grep -qx "$CLUSTER"; then
  echo ">> Reusing existing kind cluster '$CLUSTER' (it will NOT be deleted afterwards)."
  "$KIND" export kubeconfig --name "$CLUSTER" --kubeconfig "$KUBECONFIG"
else
  echo ">> Creating kind cluster '$CLUSTER'..."
  "$KIND" create cluster --name "$CLUSTER" --kubeconfig "$KUBECONFIG" --wait 120s
  CREATED_CLUSTER=1
fi

echo ">> Installing CRDs..."
make install
"$KUBECTL" wait --for=condition=Established \
  crd/incidents.praxis.dev crd/remediationplans.praxis.dev --timeout=60s

echo ">> Building and starting the manager on the host..."
go build -o "$WORK/manager" ./cmd
"$WORK/manager" \
  --metrics-bind-address=0 \
  --health-probe-bind-address=":$HEALTH_PORT" \
  >"$WORK/manager.log" 2>&1 &
MANAGER_PID=$!

for _ in $(seq 1 60); do
  if curl -fs "http://localhost:$HEALTH_PORT/readyz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$MANAGER_PID" 2>/dev/null; then
    echo "Manager process exited before becoming ready." >&2
    exit 1
  fi
  sleep 1
done
curl -fs "http://localhost:$HEALTH_PORT/readyz" >/dev/null || {
  echo "Manager never became ready on port $HEALTH_PORT." >&2
  exit 1
}

echo ">> Running the Chainsaw suite..."
"$CHAINSAW" test \
  --test-dir test/e2e/chainsaw \
  --assert-timeout 60s \
  --error-timeout 60s \
  --exec-timeout 30s \
  --cleanup-timeout 60s

echo ">> Chainsaw suite passed."
