# Retrieval ablation (2026-10-10)

## Fixed protocol

The same 4 policy documents and 30 existing questions are used for all three branches: 23 answerable questions and 7 unanswerable questions. No labels or thresholds are adjusted after seeing results. Keyword scoring uses the current term overlap and 0.5 cutoff. Dense retrieval uses actual normalized BAAI/bge-small-zh-v1.5 embeddings and cosine rank, with no similarity cutoff, matching the current vector retrieval policy. RRF uses k=60 and the same Go fusion implementation as runtime. Top-K=3.

Model revision: `7999e1d3359715c523056ef9478215996d62a620`. Queries include the model's Chinese retrieval instruction prefix. Inference runs locally on CPU; generated vectors are persisted for reproducible Go evaluation. This model produces 512-dimensional vectors; this offline experiment does not configure the runtime's 1536-dimensional pgvector table.

Recall@3 is the mean fraction of labeled relevant documents retrieved per answerable query (multi-document labels require coverage). MRR uses the first relevant document. Unanswerable false-positive rate is the fraction of unanswerable questions returning at least one chunk, not answer hallucination rate.

| Configuration | Recall@3 | MRR | Unanswerable false-positive rate |
| --- | ---: | ---: | ---: |
| Keyword only | 67.4% | 0.696 | 14.3% (1/7) |
| Vector only | 100% | 1.000 | 100% (7/7) |
| Keyword + vector, RRF | 100% | 1.000 | 100% (7/7) |

## Findings

Semantic embeddings recover wording that the unexpanded keyword baseline misses. RRF adds no measured quality improvement over vector-only on this corpus. Only 4 documents exist and Top-3 returns most of the corpus, making Recall@3 a weak discriminator. Do not present these 100% values as evidence of production retrieval quality.

Without a relevance threshold the vector branch always returns candidates, including all seven unsupported queries. RRF rank scores cannot establish whether a question is answerable. A separate calibration/development set is needed before selecting a rejection threshold, followed by a frozen held-out evaluation. No post-hoc threshold is fitted to these 30 questions. Some existing labels, such as treating a returns-policy question as a refund-policy match, also merit independent review before a larger benchmark.

## Reproduce

```powershell
$env:RAG_ABLATION_EXPORT='1'
go test ./agent -run '^TestExportAblationDocuments$' -count=1
python tests/prepare_rag_ablation.py
$env:RAG_ABLATION_REPORT='../docs/rag-ablation-report.json'
go test ./agent -run '^TestRAGAblation$' -count=1 -v
```

Python dependencies: sentence-transformers, torch, transformers. The experiment does not call a paid API or change production model configuration. Per-query rankings and expected IDs are stored in `rag-ablation-report.json`.
