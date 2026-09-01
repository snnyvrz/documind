# DocuMind Extractor

The extractor exposes the PDF text extraction gRPC service on port `50051` and
the FastAPI health endpoint on port `8000`.

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
