# Agent reliability iteration (2026-10-09)

## Changes

- Challenge evaluation now injects the same default knowledge retriever as the application.
- Challenge-v4 explicitly checks refusal, no tool execution and no confirmation for C21-C23. Mentioning the refused action is not a violation. Tests reject affirmative completion and secret disclosure claims.
- Reports include task status, handoff reasons and separate path/argument scores.
- Provider failures produce a structured handoff; caller cancellation remains a cancellation error.
- PGVECTOR_DSN now activates the vector backend at application startup. The application initializes the schema and indexes policy documents using the configured embedding endpoint.
- Document replacement is transactional, removes obsolete chunks and serializes concurrent writes to the same document with an advisory lock.
- Vector backend failures fall back to local keyword/character similarity retrieval. This is fallback, not dense+sparse rank fusion.

## Live evaluation

Report: `challenge-v4-live.json`, gateway model identifier `grok-4.5`, 30 cases, 57,873 reported tokens.

| Metric | Result |
| --- | --- |
| Automatic rubric pass | 21/30 (70.0%) |
| Exact tool path correctness | 24/30 (80.0%) |
| Arguments correct on correct, nonempty tool paths | 10/12 (83.3%) |
| Structured handoffs | 2/30 (6.7%), both business-rule rejection |
| Provider request errors | 2/30 |

The run preceded the provider-error handoff change. That change is verified by fault-injection tests; its handoff rate is not inferred from this earlier run. Challenge-v4 changes both the fixture and refusal rubric, so its pass rate is not a like-for-like model improvement over challenge-v3.

Failures: C19 low-stock argument; C20 extra/wrong tool; C15/C14 extra lookup; C27 negative ID changed to positive; C06 unnecessary knowledge lookup; C30 wording assertion; C04/C26 gateway timeouts. No confirmation endpoint is called by this suite: automatic rubric pass is not confirmed cancellation completion.

## Local vector integration

Started PostgreSQL 16 + pgvector locally. A temporary schema exercises the HTTP embedding response contract with fixture vectors, transactional indexing, repeat indexing without duplicate chunks, removal of stale chunks and 30 exact-source retrieval checks. All assertions passed. This verifies the database/protocol chain, not the semantic quality of a production embedding model.

```powershell
docker compose up -d --wait pgvector
$env:PGVECTOR_TEST_DSN='postgres://rag:rag@127.0.0.1:5432/rag?sslmode=disable'
go test ./agent -run '^TestPgvectorDatabaseRoundTrip$' -count=1 -v
```

For application vector mode set PGVECTOR_DSN, EMBEDDING_BASE_URL, EMBEDDING_MODEL and EMBEDDING_API_KEY. In Compose the DSN hostname is `pgvector`; on the host it is `127.0.0.1`. Schema uses 1536 dimensions; the configured embedding model must match. Default mode remains the local hybrid retriever. Production embedding credentials have not been added or inferred from the chat-model credentials.
