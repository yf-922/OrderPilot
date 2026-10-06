"""Count real replay JSON with a fixed tokenizer; not provider billing usage."""

import argparse
import importlib.metadata
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile

import tiktoken


def evaluate(envelopes, encoding):
    if len(envelopes) != 30 or len({item["id"] for item in envelopes}) != 30:
        raise ValueError("Expected exactly 30 distinct replay cases")
    rows = []
    for item in envelopes:
        counts = {
            stage: len(encoding.encode(item[stage], disallowed_special=()))
            for stage in ("raw", "compact", "window")
        }
        if counts["raw"] == 0:
            raise ValueError("Empty baseline")
        rows.append({
            "id": item["id"], "scenario": item["scenario"],
            "rawTokens": counts["raw"],
            "compactTokens": counts["compact"],
            "windowTokens": counts["window"],
            "savedTokens": counts["raw"] - counts["window"],
            "reductionPercent": 100 * (counts["raw"] - counts["window"]) / counts["raw"],
        })
    raw = statistics.mean(row["rawTokens"] for row in rows)
    compact = statistics.mean(row["compactTokens"] for row in rows)
    window = statistics.mean(row["windowTokens"] for row in rows)
    return {
        "cases": len(rows), "encoding": encoding.name,
        "tiktokenVersion": importlib.metadata.version("tiktoken"),
        "scope": "UTF-8 JSON messages+tools envelope, not Grok tokenizer or billed usage",
        "averageRawTokens": raw, "averageCompactTokens": compact,
        "averageWindowTokens": window,
        "averageFieldSavedTokens": raw - compact,
        "averageHistorySavedTokens": compact - window,
        "averageSavedTokens": raw - window,
        "meanCaseReductionPercent": statistics.mean(row["reductionPercent"] for row in rows),
        "aggregateReductionPercent": 100 * (raw - window) / raw,
        "measurements": rows,
    }


def main():
    root = Path(__file__).resolve().parents[1]
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--encoding", default="cl100k_base")
    parser.add_argument("--output", type=Path, default=root / "docs/context-token-v2-report.json")
    args = parser.parse_args()
    encoding = tiktoken.get_encoding(args.encoding)
    with tempfile.TemporaryDirectory(prefix="context-token-eval-") as temp:
        payload = Path(temp) / "envelopes.json"
        env = os.environ.copy()
        env.pop("CONTEXT_REPLAY_REPORT_PATH", None)
        env["CONTEXT_REPLAY_ENVELOPES_PATH"] = str(payload)
        subprocess.run(
            ["go", "test", "./agent", "-run", "^TestCustomerContextReplaySuite$", "-count=1"],
            cwd=root, env=env, check=True,
        )
        report = evaluate(json.loads(payload.read_text(encoding="utf-8")), encoding)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({key: value for key, value in report.items() if key != "measurements"}, indent=2))
    print(f"Report: {args.output}")


if __name__ == "__main__":
    main()
