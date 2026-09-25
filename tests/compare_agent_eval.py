"""Compare two reports produced by the same fixed Agent evaluation fixture."""

import argparse
import json
from pathlib import Path


def summarize(report):
    results = report["results"]
    expected = report["cases"] * report["repetitions"]
    if len(results) != expected:
        raise ValueError(f"expected {expected} runs, found {len(results)}")
    keys = [(item["id"], item["run"]) for item in results]
    if len(set(keys)) != len(keys):
        raise ValueError("duplicate case/run pair")
    case_ids = {item["id"] for item in results}
    if len(case_ids) != report["cases"]:
        raise ValueError("case count does not match results")
    stable = sum(
        all(item["passed"] for item in results if item["id"] == case_id)
        for case_id in case_ids
    )
    return {
        "keys": set(keys),
        "cases": case_ids,
        "pass_rate": sum(item["passed"] for item in results) / len(results),
        "stable_rate": stable / len(case_ids),
        "mean_tokens": sum(item["tokens"] for item in results) / len(results),
        "input_tokens": sum(item.get("inputTokens", 0) for item in results),
        "output_tokens": sum(item.get("outputTokens", 0) for item in results),
        "mean_latency_ms": sum(item["latencyMs"] for item in results) / len(results),
        "mean_steps": sum(item["steps"] for item in results) / len(results),
        "failed_cases": {
            item["id"] for item in results if not item["passed"]
        },
    }


def compare(baseline, candidate):
    if baseline["dataset"] != candidate["dataset"]:
        raise ValueError("reports use different dataset versions")
    before, after = summarize(baseline), summarize(candidate)
    if before["keys"] != after["keys"]:
        raise ValueError("reports cover different cases or repetitions")
    return before, after


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("baseline", type=Path)
    parser.add_argument("candidate", type=Path)
    parser.add_argument("--input-usd-per-million", type=float)
    parser.add_argument("--output-usd-per-million", type=float)
    args = parser.parse_args()
    baseline = json.loads(args.baseline.read_text(encoding="utf-8"))
    candidate = json.loads(args.candidate.read_text(encoding="utf-8"))
    before, after = compare(baseline, candidate)
    print(f"Dataset: {baseline['dataset']} | {baseline['cases']} cases x {baseline['repetitions']} runs")
    print("| Provider / model | Pass rate | Stable cases | Mean tokens | Mean latency (ms) | Mean steps |")
    print("| --- | ---: | ---: | ---: | ---: | ---: |")
    for report, summary in ((baseline, before), (candidate, after)):
        print(
            f"| {report['provider']} / {report['model']} | "
            f"{summary['pass_rate']:.1%} | {summary['stable_rate']:.1%} | "
            f"{summary['mean_tokens']:.1f} | {summary['mean_latency_ms']:.1f} | "
            f"{summary['mean_steps']:.2f} |"
        )
    print(f"New failed cases: {', '.join(sorted(after['failed_cases'] - before['failed_cases'])) or 'none'}")
    prices = (args.input_usd_per_million, args.output_usd_per_million)
    if all(price is not None and price >= 0 for price in prices):
        if any(
            "inputTokens" not in item or "outputTokens" not in item
            for report in (baseline, candidate)
            for item in report["results"]
        ):
            raise ValueError("token breakdown is required for currency cost")
        for report, summary in ((baseline, before), (candidate, after)):
            cost = (
                summary["input_tokens"] * prices[0]
                + summary["output_tokens"] * prices[1]
            ) / 1_000_000
            print(f"{report['provider']} / {report['model']}: estimated ${cost:.4f} at supplied rates")
    else:
        print("Currency cost omitted: supply the gateway's input/output rates per million tokens.")


if __name__ == "__main__":
    main()
