#!/usr/bin/env python3
"""Publish a readable report while retaining the raw JSON result."""

import argparse
import json
from pathlib import Path

from rag_eval_metrics import passage_metrics
from summarize_rag_eval import summarize

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    payload = json.loads(args.input.read_text())
    lines = ["# RAG Evaluation Report", "", f"- Git revision: `{payload.get('gitRevision', 'unknown')}`", f"- Timestamp: `{payload.get('timestamp', 'unknown')}`", "", "## Configuration", "", "```json", json.dumps(payload.get("configuration", {}), indent=2), "```", "", "## Aggregate Metrics", ""]
    lines.extend(f"- {key}: {value}" for key, value in summarize(payload).items())
    lines += ["", "## Per-question Results", "", "| ID | Answerable | Passage F1 | MRR | Answer |", "| --- | --- | ---: | ---: | --- |"]
    for item in payload["results"]:
        score = passage_metrics([source.get("text", "") for source in item.get("sources", [])], [passage["text"] for passage in item.get("goldPassages", [])], payload.get("configuration", {}).get("passageMatchThreshold", 0.5))
        lines.append(f"| {item['id']} | {item['answerable']} | {score['retrievalF1']} | {score['reciprocalRank']} | {' '.join(item.get('answer', '').split()).replace('|', '\\|')} |")
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text("\n".join(lines) + "\n")

if __name__ == "__main__":
    main()
