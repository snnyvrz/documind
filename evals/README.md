# RAG Evaluation

The evaluation set is a versioned JSONL contract for measuring retrieval,
answers, factuality, abstention, and adversarial behavior. Records identify
gold passages by page and text, rather than chunk index, so chunk settings can
change without rewriting the dataset. Answerable records may include
`gold_claims`, atomic facts required for a complete answer.

Run a live evaluation against a running API with:

```sh
python scripts/run_rag_eval.py --base-url http://127.0.0.1:1323 \
  --dataset evals/rag_questions.jsonl --documents evals/documents \
  --output evals/results/local.json
```

The benchmark is intentionally not part of pull-request CI. Model versions,
Ollama availability, and generation latency make it unsuitable for deterministic
unit tests. Results record the model, chunk settings, retrieval limit, git
revision, timestamp, and configuration snapshot.

## Metrics and Reports

The scorer reports passage precision, recall, F1, reciprocal rank, and nDCG
using token overlap. Page hit rate and exact match remain compatibility
diagnostics. The passage matching threshold is stored in
`evals/rag_eval_config.json`.

Run `scripts/judge_rag_eval.py` with a local Ollama model to assess answerable
responses against gold claims and retrieved passages. Judgments contain
supported, missing, contradicted, and unsupported claims, plus faithfulness
and completeness scores. Adversarial records should cover out-of-scope
questions, false premises, temporal facts, entity substitutions, and lexical
near-misses. Abstention is evaluated for the whole response.

Generate aggregate JSON and Markdown with `make rag-eval-report`.

The committed question file is a starter corpus contract. Its answerable
annotations are intentionally incomplete and produce unavailable
passage/factuality scores. Replace its fixtures and evidence with a licensed
document set before treating scores as published results.
