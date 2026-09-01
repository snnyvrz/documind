# DocuMind Document Processor

The document processor extracts PDF text, chunks it, and streams Ollama
embeddings over gRPC on port `50051`. Its FastAPI health endpoint is on port
`8000`.

## Development

Install locked dependencies and run the service with `uv`:

```sh
uv sync
uv run uvicorn service:app --reload
```

Run the gRPC server separately when developing locally:

```sh
uv run python service.py
```

The production container starts both servers without reinstalling dependencies.
