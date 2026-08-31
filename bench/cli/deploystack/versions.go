/*
Copyright 2026 The Praxis Authors.
Licensed under the Apache License, Version 2.0.
*/

// Package deploystack installs the pinned, idempotent cluster dependencies a
// benchmark run needs: a minimal single-replica Prometheus and Loki, and
// Chaos Mesh (playbook Session 2.1 task 3). Everything external is pinned in
// this file — charts here, helm itself in bench/Makefile, the kind node
// image below — so a benchmark result always names the exact world it ran in.
package deploystack

// KindNodeImage pins the Kubernetes node image for the praxis-bench kind
// cluster to the version the repository is verified against
// (docs/DEVELOPMENT.md §2).
const KindNodeImage = "kindest/node:v1.37.0"

// chaosMesh names the Chaos Mesh release, chart and namespace alike.
const chaosMesh = "chaos-mesh"

// Chart is one pinned helm release the stack installs.
type Chart struct {
	// Release is the helm release name.
	Release string
	// Name is the chart name within Repo.
	Name string
	// Repo is the chart repository URL; passed via --repo so no local repo
	// state is created or mutated.
	Repo string
	// Version is the exact chart version.
	Version string
	// Namespace the release installs into (created if missing).
	Namespace string
	// ValuesFile is the values file under bench/deploy/.
	ValuesFile string
}

// Charts is the stack, in install order. Versions were resolved against the
// live chart repositories on 2026-08-31; bump deliberately, never implicitly.
var Charts = []Chart{
	{
		Release:    "prometheus",
		Name:       "prometheus",
		Repo:       "https://prometheus-community.github.io/helm-charts",
		Version:    "29.27.0",
		Namespace:  "monitoring",
		ValuesFile: "prometheus-values.yaml",
	},
	{
		Release:    "loki",
		Name:       "loki-stack",
		Repo:       "https://grafana.github.io/helm-charts",
		Version:    "2.10.3",
		Namespace:  "monitoring",
		ValuesFile: "loki-stack-values.yaml",
	},
	{
		Release:    chaosMesh,
		Name:       chaosMesh,
		Repo:       "https://charts.chaos-mesh.org",
		Version:    "2.8.4",
		Namespace:  chaosMesh,
		ValuesFile: "chaos-mesh-values.yaml",
	},
}
