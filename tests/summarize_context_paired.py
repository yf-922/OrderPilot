"""Summarize paired live reports without conflating rubric pass and completion."""
import json
import math
from pathlib import Path
from collections import Counter

root = Path(__file__).resolve().parents[1]
report = json.loads((root / "docs/context-paired-live-report.json").read_text(encoding="utf-8"))
pairs = report["pairs"]
summary = {"completedPairs": len(pairs), "plannedPairs": report["plannedPairs"], "arms": {}}
for arm in ("off", "on"):
    rows = [p[arm] for p in pairs]
    summary["arms"][arm] = {
        "rubricPassed": sum(r["passed"] for r in rows),
        "toolPathCorrect": sum(r["toolPathCorrect"] for r in rows),
        "states": dict(Counter(r.get("taskStatus", "request_error") for r in rows)),
        "handoffReasons": dict(Counter(r["handoffReason"] for r in rows if r.get("handoffReason"))),
        "inputTokens": sum(r["inputTokens"] for r in rows),
        "outputTokens": sum(r["outputTokens"] for r in rows),
        "totalTokens": sum(r["tokens"] for r in rows),
        "failures": [{"id": r["id"], "reason": r.get("reason")} for r in rows if not r["passed"]],
    }
eligible = [p for p in pairs if p["off"]["inputTokens"] > 0 and p["on"]["inputTokens"] > 0]
off = sum(p["off"]["inputTokens"] for p in eligible)
on = sum(p["on"]["inputTokens"] for p in eligible)
summary["inputComparison"] = {
    "eligiblePairs": len(eligible),
    "meanOff": off / len(eligible) if eligible else None,
    "meanOn": on / len(eligible) if eligible else None,
    "aggregateReduction": 1 - on / off if off else None,
    "meanPerPairReduction": sum(1 - p["on"]["inputTokens"] / p["off"]["inputTokens"] for p in eligible) / len(eligible) if eligible else None,
}
lost = [p["id"] for p in pairs if p["off"]["passed"] and not p["on"]["passed"]]
gained = [p["id"] for p in pairs if not p["off"]["passed"] and p["on"]["passed"]]
n = len(lost) + len(gained)
pvalue = min(1, 2 * sum(math.comb(n, k) for k in range(min(len(lost), len(gained)) + 1)) / 2**n) if n else 1
summary["pairedPassChanges"] = {"lost": lost, "gained": gained, "exactMcNemarP": pvalue}
print(json.dumps(summary, ensure_ascii=False, indent=2))
(root / "docs/context-paired-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
