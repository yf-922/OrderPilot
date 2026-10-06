import unittest

import tiktoken

from context_token_eval import evaluate


class ContextTokenEvaluationTest(unittest.TestCase):
    def test_identical_envelopes_save_no_tokens(self):
        rows = [
            {"id": f"C{i:02}", "scenario": "fixture", "raw": '{"message":"订单尚未取消"}',
             "compact": '{"message":"订单尚未取消"}', "window": '{"message":"订单尚未取消"}'}
            for i in range(30)
        ]
        report = evaluate(rows, tiktoken.get_encoding("cl100k_base"))
        self.assertEqual(report["averageSavedTokens"], 0)
        self.assertEqual(report["meanCaseReductionPercent"], 0)
        self.assertGreater(report["averageRawTokens"], 0)

    def test_rejects_wrong_case_count_and_duplicate_ids(self):
        encoding = tiktoken.get_encoding("cl100k_base")
        with self.assertRaises(ValueError):
            evaluate([], encoding)
        with self.assertRaises(ValueError):
            evaluate([{"id": "same"}] * 30, encoding)


if __name__ == "__main__":
    unittest.main()
