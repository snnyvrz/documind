#!/usr/bin/env python3
"""Assess answer claims against retrieved passages with a local Ollama judge."""

import argparse
import json
import urllib.request
from pathlib import Path

PROMPT_VERSION = "factuality-v1"

def judge_prompt(item: dict) -> str:
    return f'''Return JSON only with supportedClaims, missingClaims, contradictedClaims, unsupportedClaims, faithfulnessScore, answerCompletenessScore, and overallCorrect. Only accept claims supported by the passages; do not use outside knowledge.
Gold claims: {json.dumps(item.get("goldClaims", []), ensure_ascii=False)}
Answer: {json.dumps(item.get("answer", ""), ensure_ascii=False)}
Retrieved passages: {json.dumps([source.get("text", "") for source in item.get("sources", [])], ensure_ascii=False)}'''

def call_judge(endpoint: str, model: str, prompt: str) -> dict:
    body = json.dumps({"model": model, "stream": False, "options": {"temperature": 0}, "format": "json", "prompt": prompt}).encode()
    request = urllib.request.Request(endpoint.rstrip("/") + "/api/generate", data=body, headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(request) as response:
        return json.loads(json.loads(response.read())["response"])

def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--endpoint", default="http://127.0.0.1:11434")
    parser.add_argument("--model", required=True)
    args = parser.parse_args()
    payload = json.loads(args.input.read_text())
    for item in payload["results"]:
        if item.get("answerable") and item.get("goldClaims"):
            item["factuality"] = call_judge(args.endpoint, args.model, judge_prompt(item))
            item["factuality"]["promptVersion"] = PROMPT_VERSION
    payload.setdefault("configuration", {}).update({"judgeModel": args.model, "judgePromptVersion": PROMPT_VERSION})
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(payload, indent=2) + "\n")

if __name__ == "__main__":
    main()
