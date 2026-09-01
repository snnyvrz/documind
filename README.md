# DocuMind

DocuMind is a PDF document upload and text-extraction service. It provides a
React frontend, a Go API, PostgreSQL persistence, and a Python extraction
service connected to the API over gRPC.

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
PostgreSQL      Python extractor
                (FastAPI + gRPC + pypdf)
```

The frontend and API are independent applications. Docker Compose connects the
applications and provides PostgreSQL for local full-stack development.

## Document Processing

PDF processing is asynchronous:

1. The frontend uploads a PDF to `POST /documents`.
2. The Go API validates the PDF magic bytes and stores it under `data/`.
3. The API stores a database record with status `queued` and returns `202 Accepted`.
4. A background worker claims queued documents and marks them `processing`.
5. The worker sends the PDF bytes to the Python extractor over gRPC.
6. The extracted text is stored in PostgreSQL and the document becomes `completed`.
7. If extraction fails, the document becomes `failed` and stores an error message.
8. The frontend polls `GET /documents/{id}` and displays the extracted text.

The current extractor handles PDFs with embedded text. Scanned PDFs require an
OCR implementation, which can be added to the Python service later.

## Repository Layout

```text
api/                 Go HTTP API, database access, worker, and gRPC client
extractor/           Python uv project with FastAPI and gRPC server
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
- `uv` for the extractor
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

The extractor is not published to the host. The Go API reaches it through the
Compose network at `extractor:50051`.

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

3. Start the extractor locally, or set `EXTRACTOR_GRPC_URL` to another
   extractor instance.

4. Start the API:

   ```sh
   cd api
   go run .
   ```

The API loads `api/.env` with `godotenv`. `DATABASE_URL` is required. The
default local upload directory is `../data` when using the example settings.

## Extractor Development

The extractor is a `uv` project. Its dependencies and exact versions are
defined in `extractor/pyproject.toml` and `extractor/uv.lock`.

Install the locked environment:

```sh
cd extractor
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
  "status": "processing"
}
```

Completed response:

```json
{
  "documentId": "ef77c3f5-2345-462c-a693-789588855a14",
  "filename": "document.pdf",
  "status": "completed",
  "text": "Extracted document text..."
}
```

Possible statuses are `queued`, `processing`, `completed`, and `failed`.

## Configuration

The main API environment variables are:

| Variable | Description | Compose default |
| --- | --- | --- |
| `DATABASE_URL` | PostgreSQL connection string | `postgres://documind:documind@db:5432/documind?sslmode=disable` |
| `UPLOAD_DIRECTORY` | Directory where uploaded PDFs are stored | `/data` |
| `EXTRACTOR_GRPC_URL` | gRPC address of the extractor | `extractor:50051` |

The API and nginx both enforce the 20 MiB upload limit. Keep these values in
sync if the limit changes.

## Protobuf

The shared gRPC contract is defined in `proto/extractor.proto`. It describes
the `TextExtractor.Extract` RPC, which accepts a document ID and PDF bytes and
returns the document ID, extracted text, and page count.

Generated bindings are committed at:

- `api/proto/` for Go
- `extractor/generated/` for Python

If the contract changes, regenerate both sets of bindings with the protobuf
compiler and the Go and Python plugins.
