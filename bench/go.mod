module github.com/akansh23-cloud/praxis/bench

go 1.26.0

// The benchmark depends on Praxis (for the API types it files Incidents
// against) without Praxis ever depending on the benchmark. The replace
// pins the dependency to this working tree, so bench always builds against
// the exact CRD types committed beside it.
replace github.com/akansh23-cloud/praxis => ../

require github.com/spf13/cobra v1.10.2

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/spf13/pflag v1.0.9 // indirect
)
