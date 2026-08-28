# `deploy/grafana/` — dashboard JSON

Grafana dashboards over the metrics listed in `docs/02-LLD.md` §15:
`praxis_plans_total`, `praxis_policy_rejections_total`, `praxis_dryrun_total`,
`praxis_verifications_total`, `praxis_rollbacks_total`, `praxis_breaker_open`,
`praxis_llm_tokens_total`, `praxis_llm_cost_usd_total`, `praxis_runbook_hits_total`
and the `praxis_stage_duration_seconds` histogram.

Policy rejections and rollbacks are headline panels, not error panels — "the
gate caught it" is the system working.

Empty in Phase 0. Populated once the metrics exist.
