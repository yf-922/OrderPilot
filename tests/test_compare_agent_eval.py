import unittest

from compare_agent_eval import compare, summarize


def report(results, dataset="v1"):
    return {
        "dataset": dataset,
        "cases": 2,
        "repetitions": 2,
        "results": [
            {
                "id": case_id,
                "run": run,
                "passed": passed,
                "tokens": 10,
                "latencyMs": 20,
                "steps": 1,
            }
            for case_id, run, passed in results
        ],
    }


class CompareAgentEvalTest(unittest.TestCase):
    def setUp(self):
        self.baseline = report(
            [("A", 1, True), ("A", 2, True), ("B", 1, True), ("B", 2, False)]
        )

    def test_summary_counts_stable_cases(self):
        summary = summarize(self.baseline)
        self.assertEqual(summary["pass_rate"], 0.75)
        self.assertEqual(summary["stable_rate"], 0.5)
        self.assertEqual(summary["failed_cases"], {"B"})

    def test_rejects_different_dataset(self):
        with self.assertRaisesRegex(ValueError, "different dataset"):
            candidate = dict(self.baseline, dataset="v2")
            compare(self.baseline, candidate)

    def test_rejects_partial_or_duplicate_runs(self):
        candidate = report([("A", 1, True)] * 4)
        with self.assertRaisesRegex(ValueError, "duplicate"):
            compare(self.baseline, candidate)
        candidate["results"] = candidate["results"][:2]
        with self.assertRaisesRegex(ValueError, "expected 4"):
            compare(self.baseline, candidate)


if __name__ == "__main__":
    unittest.main()
