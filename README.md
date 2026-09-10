# DocuMind

DocuMind is a PDF document upload and embedding service. It provides a React
frontend, a Go API, PostgreSQL with pgvector, and a Python document processor
connected to the API over gRPC.

## Architecture

```text
Browser
  |
  | HTTP
  v
Frontend (React + Vite + nginx)
  |
  | /documents
  v
Go API (Echo + GORM)
  |                 \
  | PostgreSQL        | gRPC with PDF bytes
  v                 v
PostgreSQL      Python document processor
                (FastAPI + gRPC + pypdf + Ollama)
```

The frontend and API are independent applications. Docker Compose connects the
applications and provides PostgreSQL for local full-stack development.

## Document Processing

PDF processing is asynchronous:

1. The frontend uploads a PDF to `POST /documents`.
2. The Go API validates the PDF magic bytes and stores it under `data/` or in the configured object store.
3. The API commits the document metadata and upload quota accounting in one PostgreSQL transaction, then returns `202 Accepted`.
4. A background worker claims queued documents, increments their attempt count,
   and marks them `processing` under a renewable lease.
5. The worker sends the PDF bytes to the Python document processor over gRPC.
6. The processor extracts text, chunks it, and streams embedding batches from Ollama.
7. The API stores the extracted text and chunks/vectors in PostgreSQL and marks the document `completed`.
8. Temporary processor or Ollama failures return the document to `queued` with
   exponential backoff. Invalid PDFs fail immediately, and temporary failures
   become `failed` after five attempts.
9. Expired processing leases are reclaimed automatically, so a worker or API
   crash cannot leave a document permanently stuck in `processing`.
10. The frontend polls `GET /documents/{id}` sequentially until processing finishes,
    and cancels polling when another document is selected.
11. The frontend lists completed and in-progress documents, lets users reopen or
    delete previous documents, and asks questions about completed documents.
12. Questions stream answers over SSE. The frontend displays only the generated
    answer; switching documents or stopping an answer cancels the active stream,
    and pressing Enter while a stream is active is ignored.

The document processor handles PDFs with embedded text. Scanned PDFs require an
OCR implementation, which can be added later.

Database metadata and upload quota commitment are atomic. Storage writes are
intentionally outside that transaction: a failed database commit triggers
best-effort deletion, while startup reconciliation removes old unreferenced
objects using `UPLOAD_ORPHAN_MIN_AGE`. Storage cleanup failures do not block API
startup or alter database accounting.

## Repository Layout

```text
api/                 Go HTTP API, database access, worker, and gRPC client
document-processor/  Python uv project with FastAPI, gRPC, and Ollama processing
frontend/            React application and nginx configuration
proto/               Shared protobuf contract
compose.yaml         Local full-stack Docker Compose configuration
```

## Requirements

For the full Docker workflow:

- Docker with Docker Compose

For local service development:

- Go 1.26 or newer
- Bun for the frontend
- `uv` for the document processor
- PostgreSQL, or the Compose PostgreSQL service

## Run the Full Stack

Start all services with:

```sh
cp .env.example .env
docker compose up --build
```

The root `.env` is loaded by Docker Compose and configures the full stack. It
must contain the PostgreSQL credentials and production authentication settings,
including an `AUTH_JWT_SECRET` with at least 32 characters. Keep this file
local; it is ignored by Git.

Open the application at [http://localhost:8080](http://localhost:8080).

The services use these local ports:

| Service | Address | Purpose |
| --- | --- | --- |
| Frontend | `http://localhost:8080` | Web application |
| Go API | `http://localhost:1323` | HTTP API |
| PostgreSQL | `localhost:5432` | Document metadata |
| Extractor | internal only | HTTP health on `8000`, gRPC on `50051` |

The document processor is not published to the host. The Go API reaches it
through the Compose network at `document-processor:50051`.

Stop the stack with:

```sh
docker compose down
```

PostgreSQL data is stored in the Docker volume `postgres_data`. Uploaded PDFs
are stored in the local `data/` directory.

## Go API Development

The API is in `api/` and uses Echo, GORM, and PostgreSQL.

Run the API tests:

```sh
cd api
go test ./...
```

Run the PostgreSQL integration tests with the Compose database running:

```sh
DATABASE_URL=postgres://documind:documind@localhost:5432/documind?sslmode=disable \
  go test -tags=integration ./...
```

The integration tests use the `pgvector/pgvector` PostgreSQL image because the
production schema requires the `vector` extension. They are skipped when
`DATABASE_URL` is unset.

Run the API locally:

1. Start PostgreSQL:

   ```sh
   docker compose up -d db
   ```

2. Configure the API-only local environment:

   ```sh
   cp api/.env.example api/.env
   ```

3. Start the document processor locally, or set `DOCUMENT_PROCESSOR_GRPC_URL`
   to another processor instance.

4. Start the API:

   ```sh
   cd api
   go run .
   ```

The API loads `api/.env` with `godotenv`. `DATABASE_URL` is required. The
default local upload directory is `../data` when using the example settings.
This file is separate from the root `.env`: `api/.env` is used by `go run .`,
while the root `.env` is used by Docker Compose.

To reset only the local PostgreSQL database while preserving the Ollama model
cache, run:

```sh
docker compose stop db
docker compose rm -f db
docker volume rm documind_postgres_data
docker compose up --build
```

This removes PostgreSQL data and credentials, but does not remove the
`documind_ollama_data` volume or uploaded PDFs in `data/`.

## Extractor Development

The document processor is a `uv` project. Its dependencies and exact versions
are defined in `document-processor/pyproject.toml` and `document-processor/uv.lock`.

Install the locked environment:

```sh
cd document-processor
uv sync --locked
```

Run the FastAPI health server:

```sh
uv run uvicorn service:app --host 0.0.0.0 --port 8000
```

Run the gRPC server in a second terminal:

```sh
uv run python service.py
```

Check the health endpoint:

```sh
curl http://localhost:8000/health
```

Expected response:

```json
{"status":"ok"}
```

The production container starts both the FastAPI and gRPC servers.

## Frontend Development

Install dependencies and start the Vite development server:

```sh
cd frontend
bun install --frozen-lockfile
bun run dev
```

The Vite server proxies `/documents` to `http://127.0.0.1:1323`, so the Go API
must be running locally. The frontend is typically available at
[http://localhost:5173](http://localhost:5173).

Run the frontend checks:

```sh
bun run build
bun run lint
```

The same mocked workflow is available through the repository Make target:

```sh
bun run test:e2e
```

Run the deterministic mocked browser workflow tests:

```sh
make test-e2e
```

Install the Playwright browser once before running browser tests:

```sh
cd frontend && bun run test:e2e:install
```

Run the production-like browser workflow against the full Docker Compose stack:

```sh
make test-e2e-production
```

This starts the stack, waits for the frontend on port `8080`, registers a
temporary account, uploads a real PDF, waits for document processing, and asks a
question through the real authenticated API. The Make target runs the stack with
development authentication, HTTP-compatible cookies, and HTTPS enforcement
disabled because the local test endpoint is plain HTTP. It requires Docker
Compose and healthy Ollama models, and may take several minutes.
The stack is stopped when the test finishes. Keep it running for debugging with:

```sh
KEEP_STACK=1 make test-e2e-production
```

The mocked suite is deterministic and does not require the backend, database,
processor, or Ollama. The production-like suite exercises the full Compose stack.

## HTTP API

### Upload a document

```http
POST /documents
Content-Type: multipart/form-data
```

The multipart field must be named `file`. Uploads are limited to 20 MiB and
must begin with the PDF `%PDF-` signature.

Example response:

```json
{
  "documentId": "ef77c3f5-2345-462c-a693-789588855a14",
  "status": "queued"
}
```

### List documents

```http
GET /documents
```

Returns document summaries ordered from newest to oldest. Each summary includes
the document ID, original filename, processing status, page count when available,
timestamps, and a processing error when applicable.

### Get document status

```http
GET /documents/{documentId}
```

Queued response:

```json
{
  "documentId": "ef77c3f5-2345-462c-a693-789588855a14",
  "filename": "document.pdf",
  "status": "queued",
  "attemptCount": 1,
  "nextAttemptAt": "2026-09-05T12:00:05Z"
}
```

Completed response:

```json
{
  "documentId": "ef77c3f5-2345-462c-a693-789588855a14",
  "filename": "document.pdf",
  "status": "completed",
  "pageCount": 3,
  "attemptCount": 1,
  "text": "Extracted document text..."
}
```

Possible statuses are `queued`, `processing`, `completed`, and `failed`.
`attemptCount` reports how many processing attempts have started, and
`nextAttemptAt` is present while a retry is waiting for its backoff delay.

### List extracted chunks

```http
GET /documents/{documentId}/chunks
```

Returns the stored extracted-text chunks with their `chunkIndex`, text offsets,
page ranges, and text. The current frontend does not fetch or render this data,
but the endpoint remains available for API consumers.

### Delete a document

```http
DELETE /documents/{documentId}
```

Deletes the document, its stored chunks, and its uploaded file directory. The
endpoint returns `204 No Content` on success and `404 Not Found` when the document
does not exist.

### Ask a question

```http
POST /documents/{documentId}/questions
Content-Type: application/json
Accept: text/event-stream
```

Request body:

```json
{"question":"What are the main conclusions?"}
```

The response is an SSE stream containing `token` events while the answer is
generated, optionally followed by a `sources` event with retrieved chunk text,
offsets, and page ranges, and a final `done` event. Page ranges use one-based
PDF page numbers. Questions are single-turn and must target a completed
document. The frontend prevents concurrent questions, displays the generated
answer only, and cancels the stream when the user stops it or switches
documents. Source metadata remains part of the API stream for API consumers.
PDFs without extractable text, including scanned PDFs without an embedded text
layer, fail processing and cannot be questioned. Document routes require an
 JWT cookie. The API validates the configured issuer and expiry and
uses the token `sub` claim as the immutable document owner. Reads, chunk
retrieval, deletion, and questions are owner-scoped; unauthorized IDs are
reported as not found.

Initial per-owner limits are 10 GiB and 1,000 documents, 10 uploads per hour, 2
active answer generations, and 100 answer requests per UTC day. Uploads remain
limited to 20 MiB and questions to 4,000 characters. Quota rejection returns 429;
request-size rejection returns 413. The API exposes `/health` for liveness, `/ready`
for readiness, and `/metrics` in Prometheus text format. Processor readiness
requires Ollama plus both configured embedding and chat models. Existing documents
must be explicitly assigned an owner before enabling authentication.

Browser authentication uses local email/password accounts. `POST /auth/register`
and `POST /auth/login` issue an HTTP-only, Secure JWT cookie, `/auth/session`
exposes the current user, and `POST /auth/logout` clears the cookie and revokes
all sessions for that account. JWTs are never exposed to frontend JavaScript or
stored in browser storage. Configure `AUTH_JWT_SECRET` with at least 32 random
characters for shared deployments. Production also requires
`AUTH_COOKIE_SECURE=true` and `AUTH_REQUIRE_HTTPS=true`; TLS must terminate at
the external reverse proxy, which must forward `X-Forwarded-Proto: https`.
The bundled nginx proxy preserves that header when forwarding to the API.

Authentication requests are limited by source IP and normalized account, use an
8 KiB request-body limit, and reject passwords shorter than 8 characters or
longer than 72 bytes. These rate limits are process-local and must be replaced
with shared rate-limit storage when running multiple API replicas.

For local API development, `AUTH_MODE=development` allows requests to use the
fixed local principal when no JWT secret is configured. The local Compose E2E
Make target instead enables development authentication with the configured JWT
secret, disables Secure cookies and HTTPS enforcement for its plain HTTP test
endpoint, and registers a temporary account through the real UI. These settings
are for local testing only and must not be used for shared deployments.

When upgrading a database created before ownership was enabled, set
`LEGACY_DOCUMENT_OWNER` to an explicitly chosen operator subject. The startup
migration assigns that owner only to rows without an owner and then enforces the
non-null constraint. Do not use `local-dev` for a shared deployment.

## RAG Evaluation

The versioned evaluation contract lives in `evals/`. It records reference
answers, answerability, and supporting page passages independently of chunk
indexes. The live runner in `scripts/run_rag_eval.py` exercises the public
upload, processing, and SSE question APIs and writes raw JSON results. Run it
against a local stack as documented in `evals/README.md`. Live model evaluation
is intentionally separate from deterministic CI. The runner requires an
authenticated account and preserves the session cookie across upload, polling,
and question requests:

```sh
DOCUMIND_EVAL_EMAIL=eval@example.com \
DOCUMIND_EVAL_PASSWORD='password-at-least-8-chars' \
make rag-eval
```

Use `--register` with `scripts/run_rag_eval.py` when creating a new evaluation
account. The runner fails when a document fails processing, an SSE `error` event
is received, or the stream does not finish with `event: done` and `{"ok": true}`.

## Configuration

The main API environment variables are:

| Variable | Description | Compose default |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL connection string, using the required production database variables | `postgres://${POSTGRES_USER}@db:5432/${POSTGRES_DB}?sslmode=disable` |
| `UPLOAD_DIRECTORY` | Directory where uploaded PDFs are stored | `/data` |
| `UPLOAD_ORPHAN_MIN_AGE` | Minimum age before an unreferenced uploaded object is removed | `1h` |
| `DOCUMENT_PROCESSOR_GRPC_URL` | gRPC address of the document processor | `document-processor:50051` |
| `DOCUMENT_JOB_MAX_ATTEMPTS` | Maximum processing attempts before permanent failure | `5` |
| `DOCUMENT_JOB_INITIAL_BACKOFF` | Delay after the first temporary failure | `5s` |
| `DOCUMENT_JOB_MAX_BACKOFF` | Maximum exponential retry delay | `5m` |
| `DOCUMENT_JOB_LEASE_DURATION` | Processing lease lifetime | `2m` |
| `DOCUMENT_JOB_LEASE_RENEWAL` | Interval for renewing active leases | `30s` |
| `DOCUMENT_JOB_PROCESSING_TIMEOUT` | Maximum duration of one processing attempt | `30m` |
| `DOCUMENT_JOB_POLL_INTERVAL` | Worker delay when no job is available or claiming fails | `1s` |
| `ANSWER_GENERATION_TIMEOUT` | Maximum duration of question embedding, retrieval, and answer generation | `5m` |
| `OLLAMA_URL` | Ollama embedding API address | `http://ollama:11434` |
| `OLLAMA_EMBEDDING_MODEL` | Ollama embedding model | `nomic-embed-text` |
| `OLLAMA_CHAT_MODEL` | Ollama answer-generation model | `qwen2.5:7b` |
| `OLLAMA_CHAT_TIMEOUT` | Ollama answer request timeout in seconds | `120` |
| `EMBEDDING_DIMENSIONS` | Expected vector dimension | `768` |
| `INGESTION_CAPACITY` | Maximum concurrent PDF processing streams | `2` |
| `QUESTION_CAPACITY` | Maximum concurrent answer streams | `2` |
| `EMBEDDING_CAPACITY` | Maximum concurrent question/document embedding requests | `2` |
| `MAX_PDF_PAGES` | Maximum pages extracted from one PDF | `500` |
| `MAX_EXTRACTED_TEXT_BYTES` | Maximum UTF-8 extracted text per PDF | `26214400` |
| `MAX_CHUNKS` | Maximum chunks produced per PDF | `10000` |
| `PDF_EXTRACTION_TIMEOUT` | Maximum isolated extraction time in seconds | `60` |
| `PDF_EXTRACTION_MEMORY_BYTES` | Maximum isolated extraction address space | `536870912` |
| `PDF_EXTRACTION_CPU_SECONDS` | Maximum isolated extraction CPU time | `60` |
| `TOKEN_CHUNK_SIZE` | Target chunk size in whitespace-token units | `800` |
| `TOKEN_CHUNK_OVERLAP` | Chunk overlap in whitespace-token units | `80` |
| `EMBEDDING_BATCH_SIZE` | Chunks embedded per streamed batch | `32` |
| `RETRIEVAL_LIMIT` | Number of nearest chunks supplied to answer generation | `5` |

Chunk `start_offset` and `end_offset` values refer to the original spans in the
processor's normalized extracted document text. Long paragraphs are split using
the absolute spans of their tokens, so repeated words and irregular whitespace
do not cause offsets to point to an earlier occurrence. Chunk page ranges are
calculated from those same spans.

Quota reservations are crash-safe. Document metadata and upload quota commitment
are atomic in one database transaction. On API startup, expired upload and answer
reservations are reclaimed in a transaction and per-owner usage counters are
reconciled from documents and reservation rows. The same owner-scoped recovery
runs transactionally before upload and answer quota admission, so expired
reservations do not block an account while the API remains running. No extra
configuration is required for quota recovery. Object-storage reconciliation is
separate and uses `UPLOAD_ORPHAN_MIN_AGE` to avoid deleting recent uploads.

The `/metrics` endpoint exposes Prometheus text metrics for accepted and rejected
uploads, terminal document-processing failures, answer successes and failures,
answer-latency histogram buckets/sum/count, current queued-job depth, and the
oldest eligible queued-job age. Queue gauges are refreshed from database state;
transient processing retries are not counted as terminal processing failures.

The API and nginx both enforce the 20 MiB upload limit. Keep these values in
sync if the limit changes.

Compose does not publish PostgreSQL to the host. Set `POSTGRES_DB`,
`POSTGRES_USER`, and `POSTGRES_PASSWORD` to deployment-specific values, and set
`AUTH_JWT_SECRET` to a randomly generated secret. The Compose API defaults to
production mode and refuses to start when these values are missing or insecure.
The API is private to the Compose network; publish only the frontend or an
external TLS reverse proxy.
For local development, use `api/.env.example` with `AUTH_MODE=development` and
an explicitly configured local database.

The default embedding model is `nomic-embed-text`, which produces
768-dimensional vectors. `EMBEDDING_DIMENSIONS` must remain `768` because the
database column is `vector(768)`; changing dimensions requires a coordinated
embedding-model and database migration.

PDFs with no extractable text are rejected with an OCR-required error. OCR is
not currently enabled, so scanned PDFs must be OCR-processed before upload.

## Protobuf

The shared gRPC contract is defined in `proto/extractor.proto`. It describes
the streaming `DocumentProcessor.Process` RPC, which accepts a document ID and
PDF bytes and returns metadata followed by chunk and embedding batches.

Generated bindings are committed at:

- `api/proto/` for Go
- `document-processor/generated/` for Python

If the contract changes, regenerate both sets of bindings with the protobuf
compiler and the Go and Python plugins.
