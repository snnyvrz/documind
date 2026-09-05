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
2. The Go API validates the PDF magic bytes and stores it under `data/`.
3. The API stores a database record with status `queued` and returns `202 Accepted`.
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
10. The frontend polls `GET /documents/{id}` and displays the extracted text.

The document processor handles PDFs with embedded text. Scanned PDFs require an
OCR implementation, which can be added later.

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
docker compose up --build
```

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

Run the API locally:

1. Start PostgreSQL:

   ```sh
   docker compose up -d db
   ```

2. Configure the local environment:

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

There is currently no frontend test script. The existing lint configuration has
Fast Refresh warnings in some pre-existing shared components.

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
  "attemptCount": 1,
  "text": "Extracted document text..."
}
```

Possible statuses are `queued`, `processing`, `completed`, and `failed`.
`attemptCount` reports how many processing attempts have started, and
`nextAttemptAt` is present while a retry is waiting for its backoff delay.

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
generated, followed by a `sources` event with the retrieved chunk text and
offsets, and a final `done` event. Questions are single-turn and must target a
completed document. Authentication and user ownership are not implemented yet;
this endpoint should remain local-only until documents are associated with
users.

## Configuration

The main API environment variables are:

| Variable | Description | Compose default |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL connection string | `postgres://documind:documind@db:5432/documind?sslmode=disable` |
| `UPLOAD_DIRECTORY` | Directory where uploaded PDFs are stored | `/data` |
| `DOCUMENT_PROCESSOR_GRPC_URL` | gRPC address of the document processor | `document-processor:50051` |
| `DOCUMENT_JOB_MAX_ATTEMPTS` | Maximum processing attempts before permanent failure | `5` |
| `DOCUMENT_JOB_INITIAL_BACKOFF` | Delay after the first temporary failure | `5s` |
| `DOCUMENT_JOB_MAX_BACKOFF` | Maximum exponential retry delay | `5m` |
| `DOCUMENT_JOB_LEASE_DURATION` | Processing lease lifetime | `2m` |
| `DOCUMENT_JOB_LEASE_RENEWAL` | Interval for renewing active leases | `30s` |
| `DOCUMENT_JOB_PROCESSING_TIMEOUT` | Maximum duration of one processing attempt | `30m` |
| `DOCUMENT_JOB_POLL_INTERVAL` | Worker delay when no job is available or claiming fails | `1s` |
| `OLLAMA_URL` | Ollama embedding API address | `http://ollama:11434` |
| `OLLAMA_EMBEDDING_MODEL` | Ollama embedding model | `nomic-embed-text` |
| `OLLAMA_CHAT_MODEL` | Ollama answer-generation model | `qwen2.5:7b` |
| `EMBEDDING_DIMENSIONS` | Expected vector dimension | `768` |
| `CHUNK_SIZE` | Chunk size in characters | `4000` |
| `CHUNK_OVERLAP` | Chunk overlap in characters | `400` |
| `EMBEDDING_BATCH_SIZE` | Chunks embedded per streamed batch | `32` |

The API and nginx both enforce the 20 MiB upload limit. Keep these values in
sync if the limit changes.

The default embedding model is `nomic-embed-text`, which produces
768-dimensional vectors. A different model must produce the configured
`EMBEDDING_DIMENSIONS` value unless the vector column is migrated.

## Protobuf

The shared gRPC contract is defined in `proto/extractor.proto`. It describes
the streaming `DocumentProcessor.Process` RPC, which accepts a document ID and
PDF bytes and returns metadata followed by chunk and embedding batches.

Generated bindings are committed at:

- `api/proto/` for Go
- `document-processor/generated/` for Python

If the contract changes, regenerate both sets of bindings with the protobuf
compiler and the Go and Python plugins.
