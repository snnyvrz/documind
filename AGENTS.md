# AGENTS.md

DocuMind: a PDF-document upload and embedding service. The repo contains independent Go, Python, and React apps wired together by `compose.yaml`. CI runs Go unit and PostgreSQL integration tests, Python tests, and frontend build/lint checks. The frontend also has Playwright workflow tests.

## Layout

- `api/` — Go 1.26 backend. Module name is `api` (not a host path). HTTP server on `:1323` using **labstack/echo v5** (note: context params are `*echo.Context`, not v4's value type), GORM + PostgreSQL, and a background worker that calls the processor over gRPC.
- `document-processor/` — Python 3.14 `uv` project. FastAPI health server on `:8000`, gRPC processor on `:50051`, PDF extraction/chunking, and Ollama embedding/chat integration.
- `frontend/` — React 19 + Vite 8 + Tailwind v4 + shadcn/ui. **Bun** is the package manager (`bun.lock` committed; never add a `package-lock.json`). Path alias `@/*` → `src/*`. React Compiler is enabled via `@rolldown/plugin-babel` + `reactCompilerPreset` in `vite.config.ts`. Document polling and answer SSE streaming live in cancellation-aware hooks; document citations navigate to extracted-text chunks in the page.
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
- `bun run test:e2e:production` — full-stack Playwright workflow through Compose; requires Docker, PostgreSQL, the processor, Ollama, and configured models.

Full stack: `docker compose up --build`.
Stop the stack: `docker compose down`.

CI-equivalent checks:
- API unit tests: `cd api && go test ./...`.
- API integration tests: set `DATABASE_URL` and run `cd api && go test -tags=integration ./...`.
- Processor tests: `cd document-processor && uv sync --locked && uv run python -m pytest`.
- Frontend checks: `cd frontend && bun install --frozen-lockfile && bun run build && bun run lint`.
- Mocked frontend workflow: `cd frontend && bun run test:e2e`.

## API local setup

- Copy `api/.env.example` → `api/.env` and start Postgres first: `docker compose up -d db`. `main.go` loads `.env` via godotenv; `DATABASE_URL` is required (panics if unset/unreachable) and `AutoMigrate` runs on startup. `UPLOAD_DIRECTORY` defaults to the repository `data/` directory with the example settings.
- Start the document processor locally, or set `DOCUMENT_PROCESSOR_GRPC_URL` to another processor instance before running the API. The processor also requires Ollama and the configured embedding/chat models for actual document processing.

## Gotchas

- Tailwind is v4: CSS-first config (`@import "tailwindcss"` in `src/index.css`), `@tailwindcss/vite` plugin, no `tailwind.config.*` file.
- shadcn/ui uses registry style `base-rhea` (`components.json`); shadcn components live in `src/components/ui/`.
- API upload limit is 20 MB (`http.MaxBytesReader`) and PDFs are validated by `%PDF-` magic bytes — keep this in sync with nginx `client_max_body_size` (currently local to the container nginx config).
- Document routes are `POST /documents`, `GET /documents`, `GET /documents/:id`, `GET /documents/:id/chunks`, `DELETE /documents/:id`, and `POST /documents/:id/questions`.
- Document selection is a cancellation boundary: switching or deleting a document must abort its status polling and answer stream and clear answer, sources, extracted text, and citation highlights.
- Question submission needs an in-flight guard. Enter and button actions must not start a second answer stream while one is active.
- `GET /documents/:id/chunks` exposes stored chunk metadata for in-app citation navigation. Citations scroll to `chunk-{chunkIndex}` targets; they do not open the original PDF.
- Deleting a document removes its database chunks and uploaded `documents/<id>/` directory.
- `data/`, `api/.env`, and `api/tmp/` are gitignored working-state (uploads, local env, air build artifacts).
