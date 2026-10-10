# BM25 / vector / RRF ablation v2

## Corpus and protocol

32 authored **simulated** commerce policies: cancellation, refunds, returns, delivery and address changes. Conditions distinguish unpaid, pending, picking, shipped, partial orders and payment channels. Documents are not a real merchant's terms and do not implement payment/returns endpoints. Each document contains policy ID, condition, action, exception and source. Paragraphs form complete rule units; short documents remain intact. Current corpus has 32 documents and 32 chunks. Runtime indexes the expanded corpus when pgvector is configured; without pgvector it uses the expanded BM25 corpus.

BM25 parameters: k1=1.2, b=0.75. Tokenization uses Chinese unigrams/bigrams and full ASCII identifiers such as CAN-101. This is a small-corpus scan over at most 10,000 chunks, not a search-engine deployment. Vector model: BAAI/bge-small-zh-v1.5, revision 7999e1d3359715c523056ef9478215996d62a620, normalized embeddings, Chinese retrieval query prefix. Model inference is local CPU. Offline vectors are 512-dimensional, not the runtime pgvector schema's 1536 dimensions.

All branches use identical documents, chunks and labels. Candidate count=9, output Top-3; RRF k=60. Calibration: 40 questions (32 standard, 8 unknown). Frozen test: 75 questions (32 paraphrases, 32 policy identifiers, 3 multi-condition, 8 unknown). No cases are removed after measuring results. JSON files retain per-query rankings. No user traffic or end-to-end answer quality is measured.

Recall@3 averages relevant-document coverage per answerable question, including multi-document labels. MRR measures first relevant rank. Unknown false positives mean returned chunks, not hallucinated answers.

## Frozen test, without rejection threshold

| Branch | Recall@3 (67 answerable) | MRR | Unknown false-positive rate |
| --- | ---: | ---: | ---: |
| BM25 | 79.9% | 0.746 | 8/8 |
| Vector | 85.1% | 0.813 | 8/8 |
| RRF | 88.1% | 0.811 | 8/8 |

RRF gains 3.0 percentage points of Recall@3 over vectors, but its MRR is slightly lower. It improves coverage rather than establishing superiority on every metric.

## Calibration-selected query rejection

For each branch, use only calibration questions to maximize `(Recall@3 + unknown specificity)/2`. Candidate thresholds are observed calibration top scores; ties prefer the lower threshold. Reject an entire query when its top score is below that branch's threshold. Thresholds are not adjusted on the test set.

| Branch | Selected threshold | Test Recall@3 | Test MRR | Test unknown FP |
| --- | ---: | ---: | ---: | ---: |
| BM25 | 11.6461 | 64.9% | 0.639 | 0/8 |
| Vector | 0.536229 | 68.7% | 0.692 | 0/8 |
| RRF | 0.0325225 | 61.9% | 0.617 | 0/8 |

The standard-worded calibration set transfers poorly to colloquial questions. RRF score agreement is not semantic confidence. Thresholds remain **offline experiments, not runtime defaults**. A larger, linguistically diverse independent calibration set is needed before deployment; do not fit on these frozen test results. 0/8 is a small sample observation, not a guaranteed rejection rate.

## Reproduction

```powershell
python tests/prepare_rag_ablation.py --v2
$env:RAG_V2_REPORT='../docs/retrieval-v2-report.json'
go test ./agent -run '^TestRetrievalV2Ablation$' -count=1 -v
```

Vectors embed the corpus/cases; Go checks they match the frozen source before evaluation. Existing v1 reports remain historical. This experiment does not measure latency or claim lower model cost.
