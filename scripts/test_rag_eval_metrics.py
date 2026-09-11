import math
import unittest

from rag_eval_metrics import is_abstention, passage_metrics, passage_similarity

class RagEvalMetricsTest(unittest.TestCase):
    def test_passage_similarity(self):
        self.assertGreaterEqual(passage_similarity("A blue bird flies", "blue bird flies quickly"), 0.5)

    def test_ranked_metrics(self):
        result = passage_metrics(["noise", "the policy requires two steps"], ["The policy requires two steps"])
        self.assertEqual(result["retrievalPrecision"], 0.5)
        self.assertEqual(result["retrievalRecall"], 1.0)
        self.assertEqual(result["reciprocalRank"], 0.5)

    def test_duplicate_retrieved_passages_get_one_credit(self):
        result = passage_metrics(["the policy requires two steps"] * 2, ["The policy requires two steps"])
        self.assertEqual(result["retrievalPrecision"], 0.5)
        self.assertEqual(result["retrievalRecall"], 1.0)
        self.assertEqual(result["retrievalF1"], 2 / 3)
        self.assertEqual(result["reciprocalRank"], 1.0)
        self.assertEqual(result["matchedPassages"], 1)

    def test_overlapping_retrieved_passages_do_not_double_count_gold(self):
        result = passage_metrics(
            ["the policy requires two steps and approval", "policy requires two steps"],
            ["The policy requires two steps"],
        )
        self.assertEqual(result["matchedPassages"], 1)
        self.assertEqual(result["retrievalRecall"], 1.0)
        self.assertEqual(result["retrievalPrecision"], 0.5)

    def test_one_retrieved_passage_matches_at_most_one_gold_passage(self):
        result = passage_metrics(
            ["the policy requires two steps and approval"],
            ["The policy requires two steps", "The policy requires approval"],
        )
        self.assertEqual(result["matchedPassages"], 1)
        self.assertEqual(result["retrievalRecall"], 0.5)
        self.assertLessEqual(result["retrievalRecall"], 1.0)

    def test_matching_maximizes_distinct_gold_passages(self):
        result = passage_metrics(
            ["policy requires two steps approval", "policy requires two steps"],
            ["policy requires two steps", "policy requires approval"],
        )
        self.assertEqual(result["matchedPassages"], 2)
        self.assertEqual(result["retrievalRecall"], 1.0)
        self.assertEqual(result["retrievalPrecision"], 1.0)

    def test_duplicate_gold_passages_are_one_target(self):
        result = passage_metrics(["The policy requires two steps"], ["The policy requires two steps", "the POLICY requires two steps"])
        self.assertEqual(result["matchedPassages"], 1)
        self.assertEqual(result["retrievalRecall"], 1.0)

    def test_duplicate_retrieved_evidence_cannot_match_separate_gold_passages(self):
        result = passage_metrics(
            ["the policy requires two steps"] * 2,
            ["the policy requires two steps", "the policy requires approval"],
        )
        self.assertEqual(result["matchedPassages"], 1)
        self.assertEqual(result["retrievalRecall"], 0.5)

    def test_ndcg_counts_only_unique_matches_at_their_ranks(self):
        result = passage_metrics(["noise", "The policy requires two steps", "The policy requires two steps"], ["The policy requires two steps"])
        self.assertAlmostEqual(result["nDCG"], (1 / math.log2(3)) / 1.0)

    def test_abstention(self):
        self.assertTrue(is_abstention("I don't know based on this document."))
        self.assertFalse(is_abstention("The document says Tuesday."))

if __name__ == "__main__":
    unittest.main()
