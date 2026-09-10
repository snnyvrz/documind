"""Deterministic metrics for passage retrieval and answerability evaluation."""

from __future__ import annotations

import math
import re
from collections.abc import Iterable


TOKEN_RE = re.compile(r"[a-z0-9]+")
ABSTENTION_PATTERNS = (
    "i don't know based on this document",
    "i do not know based on this document",
    "cannot be determined from this document",
    "not stated in this document",
    "not enough information in this document",
)


def tokens(value: str) -> set[str]:
    return set(TOKEN_RE.findall(value.lower()))


def passage_similarity(retrieved: str, gold: str) -> float:
    """Return token Jaccard similarity, ignoring formatting and word order."""
    left, right = tokens(retrieved), tokens(gold)
    return len(left & right) / len(left | right) if left and right else 0.0


def passage_metrics(retrieved: Iterable[str], gold: Iterable[str], threshold: float = 0.5) -> dict:
    retrieved = list(retrieved)
    gold = list(gold)
    if not gold:
        return {"retrievalPrecision": None, "retrievalRecall": None, "retrievalF1": None, "reciprocalRank": None, "nDCG": None, "matchedPassages": 0}
    matches = [any(passage_similarity(item, expected) >= threshold for expected in gold) for item in retrieved]
    relevant_ranks = [index + 1 for index, matched in enumerate(matches) if matched]
    relevant_count = sum(matches)
    precision = relevant_count / len(retrieved) if retrieved else 0.0
    recall = relevant_count / len(gold) if gold else None
    f1 = (2 * precision * recall / (precision + recall)) if recall is not None and precision + recall else 0.0
    reciprocal_rank = 1 / relevant_ranks[0] if relevant_ranks else 0.0
    dcg = sum(1 / math.log2(rank + 1) for rank in relevant_ranks)
    ideal = sum(1 / math.log2(rank + 1) for rank in range(1, min(len(gold), len(retrieved)) + 1))
    return {
        "retrievalPrecision": precision,
        "retrievalRecall": recall,
        "retrievalF1": f1 if recall is not None else None,
        "reciprocalRank": reciprocal_rank,
        "nDCG": dcg / ideal if ideal else 0.0,
        "matchedPassages": relevant_count,
    }


def is_abstention(answer: str) -> bool:
    normalized = " ".join(answer.lower().split())
    return any(pattern in normalized for pattern in ABSTENTION_PATTERNS)


def claim_metrics(claims: list[dict], judgment: dict | None) -> dict:
    """Summarize a structured factuality judgment without hiding missing claims."""
    if not judgment:
        return {"factualityAvailable": False}
    required = len(claims)
    supported = len(judgment.get("supportedClaims", []))
    missing = len(judgment.get("missingClaims", []))
    contradicted = len(judgment.get("contradictedClaims", []))
    return {
        "factualityAvailable": True,
        "supportedClaimCount": supported,
        "missingClaimCount": missing,
        "contradictedClaimCount": contradicted,
        "claimRecall": supported / required if required else None,
        "faithfulnessScore": judgment.get("faithfulnessScore"),
        "answerCompletenessScore": judgment.get("answerCompletenessScore"),
        "factualityCorrect": judgment.get("overallCorrect") is True,
    }
