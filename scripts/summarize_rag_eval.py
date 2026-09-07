#!/usr/bin/env python3
"""Summarize raw RAG evaluation output into stable, transparent metrics."""

import argparse
import json
import re
from pathlib import Path


ABSTENTION = "i don't know based on this document."


def normalized(value: str) -> str:
    return re.sub(r"\s+", " ", value.strip().lower())


def summarize(payload: dict) -> dict:
    results = payload["results"]
    answerable = [item for item in results if item["answerable"]]
    unanswerable = [item for item in results if not item["answerable"]]
    hits = [bool(set(item.get("goldPages", [])) & {page for source in item.get("sources", []) for page in range(source.get("pageStart", 0), source.get("pageEnd", 0) + 1)}) for item in answerable]
    exact = [normalized(item["answer"]) == normalized(item["referenceAnswer"]) for item in answerable if item["referenceAnswer"]]
    abstentions = [ABSTENTION in normalized(item["answer"]) for item in unanswerable]
    return {"questionCount": len(results), "answerableCount": len(answerable), "unanswerableCount": len(unanswerable), "evidencePageHitRate": sum(hits) / len(hits) if hits else None, "exactMatch": sum(exact) / len(exact) if exact else None, "abstentionAccuracy": sum(abstentions) / len(abstentions) if abstentions else None, "meanLatencySeconds": sum(item["latencySeconds"] for item in results) / len(results) if results else None}


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
