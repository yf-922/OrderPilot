# Live paired context evaluation (2026-10-10)

30 challenge-v4 cases, each continued from the same six simulated product-query turns; 60 live tasks using gateway identifier grok-4.5. Each arm runs once. Arm order alternates per case. Both preserve the same structured state and use the expanded BM25 policy corpus. Compression-on compacts observations and prunes complete turns; off preserves raw observations and all history. No confirmation endpoint is invoked. This evaluates seeded conversation continuations, not 30 natural user sessions or fresh single-turn challenge runs.

| Metric | Compression off | Compression on |
| --- | ---: | ---: |
| Rubric pass | 23/30 (76.7%) | 22/30 (73.3%) |
| Exact tool path | 29/30 (96.7%) | 27/30 (90.0%) |
| Structured handoff | 3/30 (10.0%) | 3/30 (10.0%) |
| Answered status | 26/30 | 26/30 |
| Awaiting confirmation | 1/30 | 1/30 |
| Input tokens, provider usage | 86,032 | 69,242 |
| Mean task input tokens | 2,867.7 | 2,308.1 |
| Total tokens | 94,531 | 78,878 |

Aggregate input reduction is 19.5%; average of paired reduction percentages is 17.9%. These differ from the older tiktoken replay's 14.4%: that test estimates one terminal envelope, while this test sums provider-reported input usage over all inference calls in each task. Actual paths and inference counts differ between arms, so total input savings include path variation. Gateway usage is not independently verified billing.

On loses rubric passes C12 (unexpected knowledge lookup) and C14 (no order-status tool), gains C05 (clarification instead of knowledge lookup). Common failures include generic product keyword arguments and negative/zero order IDs changed to 1. C19 adds a tool in the on arm. These are retained, not removed or rescored. No unconfirmed cancellation was observed. Handoffs are business-rule refusals; a handoff case can also pass the rubric, so pass and handoff rates are not additive partitions. Answered does not prove goal completion. No confirmed cancellation completion rate is measured.

Exact paired McNemar p=1.0 with only three discordant pairs; this does not establish equivalence or no degradation. Current evidence supports input reduction but not a claim that quality is unchanged. No prompt is changed in response to this test.

Reproduction requires importing local .env variables without printing credentials, then explicitly setting AGENT_CONTEXT_PAIRED_LIVE=1 and AGENT_CONTEXT_PAIRED_REPORT=../docs/context-paired-live-report.json. Run `go test ./agent -run '^TestContextPairedLiveEvaluation$' -count=1 -timeout=25m -v`, followed by `python tests/summarize_context_paired.py`. A 250,000 reported-token stop budget is checked after each completed pair; reports are saved incrementally. This is a paid opt-in test, skipped by normal CI.
