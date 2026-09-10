-- +goose Up
CREATE TABLE IF NOT EXISTS documents (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    original_filename text NOT NULL,
    stored_path text NOT NULL,
    mime_type text NOT NULL,
    size bigint NOT NULL,
    page_count bigint NOT NULL,
    status text NOT NULL,
    extracted_text_path text,
    extracted_text text,
    result jsonb,
    error_message text,
    attempt_count bigint NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    lease_token text,
    lease_expires_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS document_chunks (
    id uuid PRIMARY KEY,
    document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    chunk_index bigint NOT NULL,
    text text NOT NULL,
    start_offset bigint NOT NULL,
    end_offset bigint NOT NULL,
    page_start bigint NOT NULL,
    page_end bigint NOT NULL,
    embedding vector(768) NOT NULL,
    created_at timestamptz NOT NULL,
    CONSTRAINT document_chunk_index UNIQUE (document_id, chunk_index)
);
CREATE TABLE IF NOT EXISTS document_questions (
    id uuid PRIMARY KEY,
    owner_id text NOT NULL,
    document_id uuid NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    question text NOT NULL,
    answer text NOT NULL,
    sources jsonb NOT NULL,
    created_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS auth_users (
    id uuid PRIMARY KEY,
    email varchar(320) NOT NULL UNIQUE,
    password_hash text NOT NULL,
    session_version bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS db_owner_usages (
    owner_id text PRIMARY KEY,
    committed_bytes bigint NOT NULL DEFAULT 0,
    committed_documents bigint NOT NULL DEFAULT 0,
    reserved_bytes bigint NOT NULL DEFAULT 0,
    reserved_documents bigint NOT NULL DEFAULT 0,
    active_answers bigint NOT NULL DEFAULT 0,
    updated_at timestamptz NOT NULL
);
CREATE TABLE IF NOT EXISTS upload_reservations (
    id text PRIMARY KEY,
    owner_id text NOT NULL,
    document_id text NOT NULL,
    bytes bigint NOT NULL,
    state text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    committed_at timestamptz,
    released_at timestamptz
);
CREATE TABLE IF NOT EXISTS answer_reservations (
    id text PRIMARY KEY,
    owner_id text NOT NULL,
    request_id text NOT NULL UNIQUE,
    state text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL,
    finished_at timestamptz
);
CREATE TABLE IF NOT EXISTS rate_limit_buckets (
    kind text NOT NULL,
    key text NOT NULL,
    window_start timestamptz NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (kind, key, window_start)
);
CREATE INDEX IF NOT EXISTS idx_documents_owner_id ON documents(owner_id);
CREATE INDEX IF NOT EXISTS idx_document_chunks_document_id ON document_chunks(document_id);
CREATE INDEX IF NOT EXISTS idx_document_questions_owner_id ON document_questions(owner_id);
CREATE INDEX IF NOT EXISTS idx_document_questions_document_id ON document_questions(document_id);
CREATE INDEX IF NOT EXISTS idx_upload_reservations_owner_id ON upload_reservations(owner_id);
CREATE INDEX IF NOT EXISTS idx_upload_reservations_document_id ON upload_reservations(document_id);
CREATE INDEX IF NOT EXISTS idx_upload_reservations_state ON upload_reservations(state);
CREATE INDEX IF NOT EXISTS idx_upload_reservations_expires_at ON upload_reservations(expires_at);
CREATE INDEX IF NOT EXISTS idx_answer_reservations_owner_id ON answer_reservations(owner_id);
CREATE INDEX IF NOT EXISTS idx_answer_reservations_state ON answer_reservations(state);
CREATE INDEX IF NOT EXISTS idx_answer_reservations_expires_at ON answer_reservations(expires_at);

-- +goose Down
DROP TABLE IF EXISTS rate_limit_buckets, answer_reservations, upload_reservations, db_owner_usages, auth_users, document_questions, document_chunks, documents;
