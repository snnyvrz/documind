-- +goose Up
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_name = 'documents' AND column_name = 'owner_id') THEN
        IF EXISTS (SELECT 1 FROM documents WHERE owner_id IS NULL OR owner_id = '') THEN
            IF current_setting('app.legacy_document_owner', true) IS NULL OR current_setting('app.legacy_document_owner', true) = '' THEN
                RAISE EXCEPTION 'legacy documents require LEGACY_DOCUMENT_OWNER before migrations can complete';
            END IF;
            UPDATE documents SET owner_id = current_setting('app.legacy_document_owner') WHERE owner_id IS NULL OR owner_id = '';
        END IF;
        ALTER TABLE documents ALTER COLUMN owner_id SET NOT NULL;
    ELSIF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'documents') THEN
        ALTER TABLE documents ADD COLUMN owner_id text;
        IF current_setting('app.legacy_document_owner', true) IS NULL OR current_setting('app.legacy_document_owner', true) = '' THEN
            RAISE EXCEPTION 'legacy documents require LEGACY_DOCUMENT_OWNER before migrations can complete';
        END IF;
        UPDATE documents SET owner_id = current_setting('app.legacy_document_owner') WHERE owner_id IS NULL OR owner_id = '';
        ALTER TABLE documents ALTER COLUMN owner_id SET NOT NULL;
    END IF;
END $$;
CREATE INDEX IF NOT EXISTS idx_documents_queued_jobs ON documents(next_attempt_at, created_at, id) WHERE status = 'queued';
CREATE INDEX IF NOT EXISTS idx_documents_owner_created_id ON documents(owner_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_documents_expired_leases ON documents(lease_expires_at, created_at, id) WHERE status = 'processing';

-- +goose Down
DROP INDEX IF EXISTS idx_documents_expired_leases, idx_documents_owner_created_id, idx_documents_queued_jobs;
