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


def _distinct_passages(passages: list[str]) -> list[str]:
    """Remove exact passage duplicates while preserving annotation order."""
    distinct = []
    seen: set[frozenset[str]] = set()
    for passage in passages:
        identity = frozenset(tokens(passage))
        if identity not in seen:
            seen.add(identity)
            distinct.append(passage)
    return distinct


def _matched_passages(retrieved: list[str], gold: list[str], threshold: float) -> dict[int, int]:
    """Return a deterministic maximum one-to-one retrieved/gold matching."""
    edges = []
    seen: set[frozenset[str]] = set()
    for item in retrieved:
        identity = frozenset(tokens(item))
        if identity in seen:
            edges.append([])
            continue
        seen.add(identity)
        edges.append([index for index, expected in enumerate(gold) if passage_similarity(item, expected) >= threshold])
    gold_matches: dict[int, int] = {}

    def augment(retrieved_index: int, visited: set[int]) -> bool:
        for gold_index in edges[retrieved_index]:
            if gold_index in visited:
                continue
            visited.add(gold_index)
            previous = gold_matches.get(gold_index)
            if previous is None or augment(previous, visited):
                gold_matches[gold_index] = retrieved_index
                return True
        return False

    for retrieved_index in range(len(retrieved)):
        augment(retrieved_index, set())
    return {retrieved_index: gold_index for gold_index, retrieved_index in gold_matches.items()}


def passage_metrics(retrieved: Iterable[str], gold: Iterable[str], threshold: float = 0.5) -> dict:
    retrieved = list(retrieved)
    gold = _distinct_passages(list(gold))
    if not gold:
        return {"retrievalPrecision": None, "retrievalRecall": None, "retrievalF1": None, "reciprocalRank": None, "nDCG": None, "matchedPassages": 0}
    matches = _matched_passages(retrieved, gold, threshold)
    relevant_ranks = [index + 1 for index in sorted(matches)]
    relevant_count = len(matches)
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
