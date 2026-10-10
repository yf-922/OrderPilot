# Keyword + vector RRF retrieval

When PGVECTOR_DSN and embedding configuration are supplied, the application now runs keyword retrieval and embedding/pgvector retrieval concurrently over the same persisted knowledge chunks. Each branch returns up to 3 times the requested output count. RRF sums `1 / (60 + rank)` using one-based ranks, deduplicates by chunk ID and returns Top-K. Ties are resolved by ID. Raw lexical and cosine scores are not added.

Keyword retrieval scans at most 10,000 policy chunks and uses the existing Chinese-character/ASCII term overlap score (minimum 0.5). This is not BM25. Vector retrieval uses cosine ranking. RRF scores are rank evidence, not semantic confidence; no-answer threshold calibration remains to be evaluated with a real embedding model. One failed branch degrades to the other; both failures invoke the existing local fallback. Caller cancellation is preserved.

## Validation

- Full Go suite passed.
- PostgreSQL/pgvector integration: 30 retrieval assertions passed through HTTP embedding protocol, persisted keyword retrieval, vector retrieval and RRF.
- 30 controlled ranking fixtures: the shared relevant candidate ranks second in each branch and first after fusion; duplicate candidates do not receive extra votes. Single-branch failure, both-branch failure and cancellation are checked.
- Integration embedding vectors are fixtures. These figures verify ranking and engineering behavior, not natural-language Recall@K or task completion improvements. Existing 12-case local retrieval metrics do not measure this new RRF path.

Run with:

```powershell
docker compose up -d --wait pgvector
$env:PGVECTOR_TEST_DSN='postgres://rag:rag@127.0.0.1:5432/rag?sslmode=disable'
go test ./agent -run 'TestPgvectorDatabaseRoundTrip|TestRRFRankingAndDegradation' -count=1 -v
```

Resume wording: Implemented keyword and Embedding vector recall with RRF rank fusion, chunk deduplication and single-branch degradation; verified the pgvector retrieval chain through database integration tests.
