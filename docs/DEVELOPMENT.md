# Development

Everything Praxis needs runs on a laptop. There is no cloud account, no
managed cluster and no LLM API key required to build, test or install what
exists today — Phase 0 ships API types and scaffolding, and none of it calls a
model.

---

## 1. Prerequisites

Four tools. Nothing else is required for `make lint test` or `make install run`.

| Tool | Minimum | Why |
|---|---|---|
| **Go** | 1.26.0 (per `go.mod`) | builds the manager; also fetches every pinned dev tool into `./bin` |
| **Docker** | 20.10+ | kind runs the cluster as containers |
| **kind** | 0.20+ | the local Kubernetes cluster |
| **kubectl** | 1.28+ | applying CRDs, inspecting resources |

Optional:

| Tool | For |
|---|---|
| **pre-commit** (Python 3.9+) | the local git hooks — `make pre-commit-install` |
| **skaffold** | the in-cluster dev loop — `make dev-skaffold` |
| **kubebuilder** 4.15.0 | only if you are scaffolding a *new* API or controller |

Everything else — `controller-gen`, `kustomize`, `setup-envtest`, the envtest
control-plane binaries, and `golangci-lint` — is downloaded into `./bin` at
pinned versions by the Makefile the first time you need it. You do not install
them yourself, and `./bin` is gitignored.

### Versions pinned by the repository

These are read from the Makefile and `go.mod`; you do not choose them.

| Component | Version | Pinned in |
|---|---|---|
| `controller-gen` | v0.21.0 | `Makefile` (`CONTROLLER_TOOLS_VERSION`) |
| `kustomize` | v5.8.1 | `Makefile` (`KUSTOMIZE_VERSION`) |
| `golangci-lint` | v2.12.2 | `Makefile` (`GOLANGCI_LINT_VERSION`) and `.custom-gcl.yml` |
| `chainsaw` | v0.2.15 | `Makefile` (`CHAINSAW_VERSION`) |
| `setup-envtest` | v0.24.1 | derived from `sigs.k8s.io/controller-runtime` in `go.mod` |
| envtest control plane | Kubernetes 1.36 | derived from `k8s.io/api` in `go.mod` |

`golangci-lint` is not used as-shipped: `make lint` builds a **custom** binary
that includes the `logcheck` module plugin declared in `.custom-gcl.yml`, so
the first `make lint` on a clean checkout compiles a linter and takes a few
minutes. Subsequent runs are seconds.

---

## 2. The environment this was actually verified on

Stated exactly, because "works on my machine" is only useful if you know which
machine. Every command in §3 and §4 was run end-to-end on this configuration:

| | |
|---|---|
| OS | Ubuntu on WSL2, kernel `6.6.87.2-microsoft-standard-WSL2`, x86_64 |
| Go | `go1.26.7 linux/amd64` |
| Docker | `28.4.0` |
| kind | `v0.33.0` |
| kind node image | `kindest/node:v1.37.0` |
| kubectl | `v1.34.1` (bundled kustomize `v5.7.1`) |
| kubebuilder | `v4.15.0` |
| git | `2.43.0` |

Two honest caveats about that list:

- **Only this configuration has been exercised.** Praxis has not been built on
  macOS, on arm64, on native Linux, or against a non-kind cluster. Nothing in
  the code is known to be platform-specific, but "not known to break" is not
  the same as "tested".
- **`pre-commit` could not be exercised here.** The verification host has no
  `pip`, `venv` or `pipx`, so `.pre-commit-config.yaml` has been validated
  structurally and each of its local hook scripts has been run directly — but
  `pre-commit run --all-files` itself has not been executed. See §7.

CI (`.github/workflows/`) runs on `ubuntu-latest` with the Go version read from
`go.mod`, which is a second, independent configuration.

---

## 3. Fresh clone to green build

Exact commands, in order, from nothing.

```bash
git clone https://github.com/akansh23-cloud/praxis.git
cd praxis

# 1. Static analysis. First run compiles the custom golangci-lint (minutes).
make lint

# 2. Unit tests against a real API server + etcd (envtest). First run
#    downloads the control-plane binaries into ./bin.
make test

# 3. Compile the manager.
make build
```

`make test` implies `manifests generate fmt vet`, so a types change is
regenerated and vetted before any test runs. If `make test` reports a diff in
`config/crd/bases/`, that is the signal that generated artifacts were committed
stale — regenerate and commit them.

Expected first-run cost on the verified host: a few minutes for the linter
build, under a minute for the envtest download, seconds thereafter.

---

## 4. Fresh clone to a running cluster

```bash
# 1. Create the local cluster (idempotent — safe to re-run).
make kind-up

# 2. Install the CRDs.
make install

# 3. Confirm both kinds are served.
kubectl api-resources --api-group=praxis.dev
```

Expected output:

```
NAME                SHORTNAMES   APIVERSION             NAMESPACED   KIND
incidents           inc          praxis.dev/v1alpha1    true         Incident
remediationplans    rplan        praxis.dev/v1alpha1    true         RemediationPlan
```

Try the samples — and try to break them, which is more instructive:

```bash
kubectl apply -k config/samples/

# The closed action vocabulary, enforced by the API server, not by Praxis code:
kubectl explain remediationplan.spec.actions.type
```

Tear down when you are finished:

```bash
make kind-down     # idempotent; says so and exits 0 if there is no cluster
```

`kind-up` and `kind-down` operate on a cluster named **`praxis`**. That is
deliberately *not* the cluster the e2e suite uses (`praxis-test-e2e`), so
running `make test-e2e` can never delete your dev cluster. Override with
`make kind-up KIND_DEV_CLUSTER=other` if you need a second one.

---

## 5. The dev loop

```bash
make dev
```

`dev` is `kind-up` → `install` → `run`: it ensures the cluster, installs the
CRDs, and runs the manager **on your host** against your kubeconfig. No image
build, no deploy, no push — a controller only needs API access, so this is the
shortest path from an edit to observing its effect. `Ctrl-C` stops the manager;
the cluster and CRDs stay.

For the in-cluster loop — when you need the manager running with its real
ServiceAccount, RBAC and NetworkPolicies rather than your own credentials:

```bash
make dev-skaffold          # requires skaffold on PATH
```

Skaffold was chosen over Tilt because this repository is already
kustomize-native: `skaffold.yaml` points its kustomize deployer at
`config/default` verbatim, whereas a Tiltfile would restate the same deployment
in Starlark and become a second thing to keep in sync. **`skaffold.yaml` has
not been exercised** — there is no controller logic to hot-reload yet, so the
in-cluster loop has no work to do in Phase 0. Treat it as a starting point, not
a verified path.

---

## 6. Test layers

| Command | What it runs | Needs |
|---|---|---|
| `make test` | unit + envtest (real API server + etcd, no kubelet) | Go only |
| `make test-e2e-chainsaw` | the Phase 1 Chainsaw suite (walk, approval, rejections) against its own kind cluster `praxis-chainsaw`, manager on the host | Docker, kind |
| `make test-e2e` | scaffold Ginkgo suite: in-cluster deployment posture (image, kustomize overlay, metrics) on `praxis-test-e2e` | Docker, kind |
| `make lint` | golangci-lint incl. `logcheck` | Go |
| `make lint-config` | validates `.golangci.yml` itself | Go |

envtest gives a **real API server**, which is why the CRD validation checkpoint
means something: CEL rules, enum bounds and the spec-immutability transition
rule are enforced by the same admission code path that a production cluster
runs. There is no kubelet, so nothing schedules — envtest proves *admission*,
not *execution*.

`make test-e2e` creates and destroys `praxis-test-e2e` itself. Do not point it
at a cluster you care about.

---

## 7. Pre-commit hooks

Optional but recommended; they are a fast local echo of CI.

```bash
python3 -m pip install --user pre-commit   # or: pipx install pre-commit
make pre-commit-install                    # writes .git/hooks/pre-commit
make pre-commit-run                        # run everything once, now
```

What they check: whitespace and line-ending hygiene, merge-conflict markers,
oversized files, YAML/JSON parseability, private keys, `gofmt`, `go vet`,
`golangci-lint`, and — the one that matters most — that `api/` and `config/`
are **regenerated**. Committing a types change without running
`make generate manifests` leaves the CRDs the API server enforces out of step
with the markers the code declares, and nothing else catches it.

The `golangci-lint` hook prefers `./bin/golangci-lint` (the pinned custom
build) and skips with a warning if the linter has never been built, so a fresh
clone is not blocked from committing.

As noted in §2, `pre-commit` itself could not be installed on the verification
host. The config parses, its structure validates against the pre-commit schema,
every file in the tree passes the checks the remote hooks would apply, and each
local hook script was executed directly in both its passing and failing paths.
The framework's own end-to-end run remains unverified.

---

## 8. Repository layout

The tree follows `docs/02-LLD.md` §2. Most `internal/` packages are `doc.go`
placeholders that state a single responsibility and name the phase that fills
them in — deliberately, so the shape of the system is legible before the code
exists.

```
api/v1alpha1/     CRD types — Incident, RemediationPlan. The security boundary.
cmd/main.go       manager entrypoint (kubebuilder scaffold; what make run runs)
cmd/analyzer/     analyzer binary — LLM egress, no write RBAC        [Phase 3]
cmd/executor/     executor binary — write RBAC, no egress          [Phase 2-5]
internal/         one package per pipeline stage; see each doc.go
config/           kustomize: crd/, rbac/, manager/, network-policy/
policies/         Kyverno/CEL policy bundle                          [Phase 4]
bench/            praxisbench — becomes its own Go module            [Phase 5]
deploy/grafana/   dashboard JSON
docs/             plan, HLD, LLD, adr/
hack/             boilerplate header, pre-commit helper scripts
test/e2e/         end-to-end suite
```

Two deliberate deviations from LLD §2, recorded here rather than silently:

1. **`cmd/main.go` still exists.** LLD §2 lists only `cmd/analyzer` and
   `cmd/executor`. The kubebuilder-scaffolded manager at `cmd/main.go` is what
   `make run`, `make build`, the Dockerfile and the e2e suite currently use;
   removing it in Phase 0 would break the working build to satisfy a layout
   the split binaries do not yet fill. `cmd/analyzer` and `cmd/executor` exist
   as placeholder entrypoints that exit non-zero with a message naming the
   phase that implements them. The split happens when there is logic to split.
2. **`config/network-policy/`, not `config/network-policies/`.** The singular
   name is where kubebuilder scaffolds it and what `config/default/kustomization.yaml`
   references. Renaming for the plural in the LLD would buy nothing.

An **import boundary** is lint-enforced (Session 3.3): only `internal/llm`,
`internal/agents` and the benchmark's agent driver (`bench/cli/agentrun`,
`runner`, `praxisbench`) may import `internal/llm` or `internal/agents`.
Everything else — the manager in `cmd/`, `internal/controller`,
`internal/evidence`, `internal/validate`, and every future package under
`internal/{executor,verify,rollback,risk,policy,simulate,approve,audit}` — is
denied. That is what keeps "the process that acts has no model" a checkable
property. Two guards enforce it: the `llm-boundary` depguard rule in
`.golangci.yml` (so `make lint` fails on a violation, in both modules) and
`internal/llm/boundary_test.go`, which walks every Go file of both modules.

The planner's JSON Schemas are **derived from the CRD**, never hand-written:
`make schema-derive` regenerates `internal/planschema/*.json` from
`config/crd/bases/praxis.dev_remediationplans.yaml`, and `make lint` runs
`make schema-check` so a CRD change without a re-derivation fails lint (and
`make test`, through `TestDerivedSchemasAreInSyncWithTheCRD`).

The LLM agent's provider credential is **process configuration only**:
`praxisbench run --agent llm` (provider `anthropic`) reads `ANTHROPIC_API_KEY`
from the environment; there is no flag for it, it is never logged, and every
provider error is scrubbed before it can carry it.

---

## 9. Making an API change

The CRD types are the security boundary, so this path has one rule that is not
optional.

```bash
# 1. Edit api/v1alpha1/*_types.go
# 2. Regenerate. Never hand-edit config/crd/bases/ or config/rbac/role.yaml.
make generate manifests
# 3. Reinstall and confirm the API server accepts the new schema.
make install
# 4. Prove the validation still bites.
make test
```

Do not weaken a validation marker to make something pass. The enum on
`ActionType`, the CEL discriminated-union rules on `Action`, and the
`self == oldSelf` transition rule on `RemediationPlanSpec` are load-bearing —
they are the reason a malformed or mutated plan cannot exist as an object.
If a rule genuinely must change, that is an ADR in the same pull request, not
a quiet edit. See `docs/adr/`.

---

## 10. Troubleshooting

**`make lint` takes minutes on first run.** Expected: it is compiling a custom
golangci-lint with the logcheck plugin. Cached afterwards in `./bin`.

**`make test` fails to find envtest binaries.** Run `make setup-envtest`
directly to see the resolution error. The Kubernetes version is derived from
`k8s.io/api` in `go.mod`, so a dependency bump can request a control plane that
has not been published for your platform.

**`make install` reports "No CRDs to install".** `kustomize build config/crd`
produced nothing — usually a broken `config/crd/kustomization.yaml`. Run that
command directly to see the error.

**`kubectl api-resources --api-group=praxis.dev` is empty after `make install`.**
Check that kubectl is pointed at the kind cluster:
`kubectl config current-context` should read `kind-praxis`.

**A CR is rejected and you expected it to be accepted.** Read the message —
CEL rule violations report the `message` string from the marker verbatim, and
those messages were written to be read. That rejection is the system working.
