"""Export actual local embedding vectors for the Go retrieval ablation."""
import json
from pathlib import Path
from sentence_transformers import SentenceTransformer

root = Path(__file__).resolve().parents[1]
cases = json.loads((root / "agent/testdata/rag_eval.json").read_text(encoding="utf-8"))
documents = json.loads((root / "agent/testdata/rag_ablation_documents.json").read_text(encoding="utf-8"))
model_name = "BAAI/bge-small-zh-v1.5"
revision = "7999e1d3359715c523056ef9478215996d62a620"
model = SentenceTransformer(model_name, revision=revision, device="cpu")
prefix = "为这个句子生成表示以用于检索相关文章："
texts = [d["Title"] + " " + d["Content"] for d in documents] + [prefix + c["query"] for c in cases]
vectors = model.encode(texts, normalize_embeddings=True, show_progress_bar=True).tolist()
output = {"model": model_name, "revision": revision, "queryPrefix": prefix, "documentVectors": vectors[:len(documents)], "queryVectors": vectors[len(documents):]}
(root / "agent/testdata/rag_ablation_vectors.json").write_text(json.dumps(output), encoding="utf-8")
