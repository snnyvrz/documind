#!/usr/bin/env python3
"""Summarize raw RAG evaluation output into stable, transparent metrics."""

import argparse
import json
import re
from pathlib import Path

from rag_eval_metrics import claim_metrics, is_abstention, passage_metrics


def normalized(value: str) -> str:
    return re.sub(r"\s+", " ", value.strip().lower())


def summarize(payload: dict) -> dict:
    results = payload["results"]
    answerable = [item for item in results if item["answerable"]]
    unanswerable = [item for item in results if not item["answerable"]]
    retrieval = []
    factuality = []
    for item in results:
        gold = [passage["text"] for passage in item.get("goldPassages", [])]
        retrieved = [source.get("text", "") for source in item.get("sources", [])]
        retrieval.append(passage_metrics(retrieved, gold, payload.get("configuration", {}).get("passageMatchThreshold", 0.5)))
        factuality.append(claim_metrics(item.get("goldClaims", []), item.get("factuality")))
    page_hits = [bool(set(item.get("goldPages", [])) & {page for source in item.get("sources", []) for page in range(source.get("pageStart", 0), source.get("pageEnd", 0) + 1)}) for item in answerable]
    exact = [normalized(item["answer"]) == normalized(item["referenceAnswer"]) for item in answerable if item["referenceAnswer"]]
    abstention_truth = [is_abstention(item["answer"]) == (not item["answerable"]) for item in results]
    abstentions = [is_abstention(item["answer"]) for item in unanswerable]
    def mean(key, values):
        values = [value[key] for value in values if value.get(key) is not None]
        return sum(values) / len(values) if values else None
    return {
        "questionCount": len(results), "answerableCount": len(answerable), "unanswerableCount": len(unanswerable),
        "evidencePageHitRate": sum(page_hits) / len(page_hits) if page_hits else None,
        "passagePrecision": mean("retrievalPrecision", retrieval), "passageRecall": mean("retrievalRecall", retrieval),
        "passageF1": mean("retrievalF1", retrieval), "meanReciprocalRank": mean("reciprocalRank", retrieval), "nDCG": mean("nDCG", retrieval),
        "exactMatch": sum(exact) / len(exact) if exact else None,
        "abstentionAccuracy": sum(abstentions) / len(abstentions) if abstentions else None,
        "abstentionAccuracyAllCases": sum(abstention_truth) / len(abstention_truth) if abstention_truth else None,
        "factualityAccuracy": mean("factualityCorrect", factuality),
        "meanClaimRecall": mean("claimRecall", factuality),
        "meanLatencySeconds": sum(item["latencySeconds"] for item in results) / len(results) if results else None,
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=Path)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    summary = summarize(json.loads(args.input.read_text()))
    text = json.dumps(summary, indent=2) + "\n"
    if args.output:
        args.output.write_text(text)
    else:
        print(text, end="")


if __name__ == "__main__":
    main()
