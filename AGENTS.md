# AGENTS.md

DocuMind: a PDF-document upload and embedding service. The repo contains independent Go, Python, and React apps wired together by `compose.yaml`. CI runs Go unit and PostgreSQL integration tests, Python tests, frontend build/lint checks, and mocked Playwright workflow tests. The `Makefile` production-like target exercises the full Compose stack with Playwright.

## Layout

- `api/` — Go 1.26 backend. Module name is `api` (not a host path). HTTP server on `:1323` using **labstack/echo v5** (note: context params are `*echo.Context`, not v4's value type), GORM + PostgreSQL, and a background worker that calls the processor over gRPC.
- `document-processor/` — Python 3.14 `uv` project. FastAPI health server on `:8000`, gRPC processor on `:50051`, isolated resource-limited PDF extraction, paragraph/token-aware chunking, and Ollama embedding/chat integration. Scanned PDFs without extractable text are rejected because OCR is not enabled.
- `frontend/` — React 19 + Vite 8 + Tailwind v4 + shadcn/ui. **Bun** is the package manager (`bun.lock` committed; never add a `package-lock.json`). Path alias `@/*` → `src/*`. React Compiler is enabled via `@rolldown/plugin-babel` + `reactCompilerPreset` in `vite.config.ts`. Document polling and answer SSE streaming live in cancellation-aware hooks; the current UI displays the generated answer without rendering extracted text, chunks, or source citations.
- `proto/` — shared protobuf contract; generated bindings are committed under `api/proto/` and `document-processor/generated/`.
- `compose.yaml` — full-stack run: PostgreSQL (`db`, port 5432), Ollama, `document-processor`, `api` (1323), and `frontend` under nginx (8080, proxies `/documents` → `api`). Uploaded PDFs are bind-mounted from host `data/` to `/data` in the API container; PostgreSQL and Ollama use named volumes.

## Commands

API (from `api/`):
- Unit tests: `go test ./...` — runs standalone, using an in-memory `memoryDocumentStore` (no Postgres needed).
- PostgreSQL integration tests: `DATABASE_URL=postgres://documind:documind@localhost:5432/documind?sslmode=disable go test -tags=integration ./...` — requires the Compose `db` service and the `pgvector/pgvector` image.
- Run locally: `go run .` — requires `api/.env`, PostgreSQL, and a document processor reachable through `DOCUMENT_PROCESSOR_GRPC_URL`.
- Live reload: `air` (config in `.air.toml`, builds to `api/tmp/main`). Requires Postgres and env; see below.

Document processor (from `document-processor/`):
- Install locked dependencies: `uv sync --locked`.
- Run tests from `document-processor/`: `uv run python -m pytest`.
- Run the FastAPI health server: `uv run uvicorn service:app --host 0.0.0.0 --port 8000`.
- Run the gRPC server separately: `uv run python service.py`.

Frontend (from `frontend/`):
- `bun install` (or `bun install --frozen-lockfile` like the Dockerfile)
- `bun run dev` — Vite dev server, proxies `/documents` → `http://127.0.0.1:1323`, so the API must be running locally.
- `bun run build` — `tsc -b && vite build`.
- `bun run lint` — ESLint.
- `bun run test:e2e` — deterministic mocked Playwright workflows; no backend or processor required.
- `bun run test:e2e:production` — full-stack Playwright workflow; requires Docker, PostgreSQL, the processor, Ollama, and configured models. The repository Make target runs Compose with development auth and plain-HTTP-compatible cookies, then registers a temporary account through the UI.

Full stack: `docker compose up --build`.
Stop the stack: `docker compose down`.

CI-equivalent checks:
- API unit tests: `cd api && go test ./...`.
- API integration tests: set `DATABASE_URL` and run `cd api && go test -tags=integration ./...`.
- Processor tests: `cd document-processor && uv sync --locked && uv run python -m pytest`.
- Frontend checks: `cd frontend && bun install --frozen-lockfile && bun run build && bun run lint`.
- Mocked frontend workflow: `cd frontend && bunx playwright install --with-deps chromium && bun run test:e2e`.

## API local setup

- Copy `api/.env.example` → `api/.env` and start Postgres first: `docker compose up -d db`. `main.go` loads `.env` via godotenv; `DATABASE_URL` is required (panics if unset/unreachable) and `AutoMigrate` runs on startup. `UPLOAD_DIRECTORY` defaults to the repository `data/` directory with the example settings.
- Start the document processor locally, or set `DOCUMENT_PROCESSOR_GRPC_URL` to another processor instance before running the API. The processor also requires Ollama and the configured embedding/chat models for actual document processing.

## Gotchas

- Tailwind is v4: CSS-first config (`@import "tailwindcss"` in `src/index.css`), `@tailwindcss/vite` plugin, no `tailwind.config.*` file.
- shadcn/ui uses registry style `base-rhea` (`components.json`); shadcn components live in `src/components/ui/`.
- API upload limit is 20 MB (`http.MaxBytesReader`) and PDFs are validated by `%PDF-` magic bytes — keep this in sync with nginx `client_max_body_size` (currently local to the container nginx config).
- Document processing additionally limits pages, extracted text, chunks, and isolated extraction CPU/memory/time through `MAX_PDF_PAGES`, `MAX_EXTRACTED_TEXT_BYTES`, `MAX_CHUNKS`, `PDF_EXTRACTION_TIMEOUT`, `PDF_EXTRACTION_MEMORY_BYTES`, and `PDF_EXTRACTION_CPU_SECONDS`. `EMBEDDING_DIMENSIONS` must stay `768` for the `vector(768)` database column.
- Document routes are `POST /documents`, `GET /documents`, `GET /documents/:id`, `GET /documents/:id/chunks`, `DELETE /documents/:id`, and `POST /documents/:id/questions`.
- Document selection is a cancellation boundary: switching or deleting a document must abort its status polling and answer stream and clear the current answer.
- Question submission needs an in-flight guard. Enter and button actions must not start a second answer stream while one is active.
- `GET /documents/:id/chunks` exposes stored chunk metadata for API consumers, but the current frontend does not fetch or render those chunks.
- Deleting a document removes its database chunks and uploaded `documents/<id>/` directory.
- The RAG evaluator requires login or registration credentials, preserves the authentication cookie, rejects failed document processing, and requires a successful SSE `done` event with `ok: true`.
- API metrics are shared by the HTTP handler and worker. They count accepted/rejected uploads, terminal processing failures, and answer outcomes; answer latency is a Prometheus histogram; queue depth and oldest queued age are refreshed from database state.
- `data/`, `api/.env`, and `api/tmp/` are gitignored working-state (uploads, local env, air build artifacts).
