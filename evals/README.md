# RAG Evaluation

The evaluation set is a versioned JSONL contract for measuring retrieval,
answers, abstention, and citations. Each record names a PDF fixture and gold
evidence by page and text, rather than by chunk index, so experiments can
change chunk size and overlap without rewriting the dataset.

Run a live evaluation against a running API with:

```sh
python scripts/run_rag_eval.py --base-url http://127.0.0.1:1323 \
  --dataset evals/rag_questions.jsonl --documents evals/documents \
  --output evals/results/local.json
```

The benchmark is intentionally not part of pull-request CI. Model versions,
Ollama availability, and generation latency make it unsuitable as a
deterministic unit test. Record the model, chunk settings, retrieval limit, git
revision, and timestamp in every result before publishing it.

The committed question file is a starter corpus contract. Replace its fixture
references and evidence with a licensed document set before treating scores as
published results.
