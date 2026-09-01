FROM ghcr.io/astral-sh/uv:python3.14-bookworm-slim

WORKDIR /app
COPY pyproject.toml uv.lock ./
RUN uv sync --locked --no-dev
COPY . ./

EXPOSE 8000 50051
CMD ["sh", "-c", "uv run --no-sync uvicorn service:app --host 0.0.0.0 --port 8000 & uv run --no-sync python service.py"]
