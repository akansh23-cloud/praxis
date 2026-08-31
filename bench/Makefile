# bench/ is its own Go module (praxisbench) — docs/02-LLD.md §2 and §17.
# This Makefile mirrors the root Makefile's conventions: pinned tools are
# installed into the repo-level ./bin by `go install` and symlinked per
# version, so bench and root share one toolchain directory.

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against the bench module.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against the bench module.
	go vet ./...

.PHONY: test
test: fmt vet ## Run bench unit tests (scenario loader/validation; no cluster needed).
	go test ./... -coverprofile cover.out

.PHONY: build
build: fmt vet ## Build the praxisbench binary into bench/bin/.
	mkdir -p bin
	go build -o bin/praxisbench ./cli/praxisbench

.PHONY: lint
lint: golangci-lint ## Run golangci-lint against the bench module.
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint and apply fixes.
	"$(GOLANGCI_LINT)" run --fix

.PHONY: run-smoke
run-smoke: build helm ## Build, then run the smoke scenario end-to-end.
	./bin/praxisbench run --scenario smoke

##@ Dependencies

## Tools land in the repo-level bin/, shared with the root Makefile.
LOCALBIN = $(abspath ../bin)
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

GOLANGCI_LINT = $(LOCALBIN)/golangci-lint
HELM = $(LOCALBIN)/helm

## helm is pinned here because only the benchmark needs it; every chart
## version it installs is pinned in cli/deploystack/versions.go.
HELM_VERSION = v3.21.4

.PHONY: golangci-lint
golangci-lint: ## Ensure the pinned custom golangci-lint exists (built by the root Makefile).
	@$(MAKE) -C .. golangci-lint

.PHONY: helm
helm: $(HELM) ## Download helm locally if necessary.
$(HELM): $(LOCALBIN)
	$(call go-install-tool,$(HELM),helm.sh/helm/v3/cmd/helm,$(HELM_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# (copied verbatim from the root Makefile so the two stay behaviourally identical)
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef
