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

    def test_abstention(self):
        self.assertTrue(is_abstention("I don't know based on this document."))
        self.assertFalse(is_abstention("The document says Tuesday."))

if __name__ == "__main__":
    unittest.main()
