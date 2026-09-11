-- +goose Up
-- Existing installations may have these tables from GORM, which did not
-- create the foreign keys declared by 00002_schema.sql.
-- +goose StatementBegin
DO $$
BEGIN
    IF to_regclass('document_chunks') IS NOT NULL THEN
        DELETE FROM document_chunks chunks
        WHERE NOT EXISTS (SELECT 1 FROM documents WHERE id = chunks.document_id);

        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint constraint_row
            WHERE constraint_row.conrelid = 'document_chunks'::regclass
              AND constraint_row.confrelid = 'documents'::regclass
              AND constraint_row.contype = 'f'
              AND constraint_row.confdeltype = 'c'
              AND constraint_row.conkey = ARRAY[
                  (SELECT attnum FROM pg_attribute
                   WHERE attrelid = 'document_chunks'::regclass AND attname = 'document_id')
              ]::smallint[]
              AND constraint_row.confkey = ARRAY[
                  (SELECT attnum FROM pg_attribute
                   WHERE attrelid = 'documents'::regclass AND attname = 'id')
              ]::smallint[]
        ) THEN
            ALTER TABLE document_chunks
                ADD CONSTRAINT document_chunks_document_id_fkey
                FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE;
        END IF;
    END IF;

    IF to_regclass('document_questions') IS NOT NULL THEN
        DELETE FROM document_questions questions
        WHERE NOT EXISTS (SELECT 1 FROM documents WHERE id = questions.document_id);

        IF NOT EXISTS (
            SELECT 1
            FROM pg_constraint constraint_row
            WHERE constraint_row.conrelid = 'document_questions'::regclass
              AND constraint_row.confrelid = 'documents'::regclass
              AND constraint_row.contype = 'f'
              AND constraint_row.confdeltype = 'c'
              AND constraint_row.conkey = ARRAY[
                  (SELECT attnum FROM pg_attribute
                   WHERE attrelid = 'document_questions'::regclass AND attname = 'document_id')
              ]::smallint[]
              AND constraint_row.confkey = ARRAY[
                  (SELECT attnum FROM pg_attribute
                   WHERE attrelid = 'documents'::regclass AND attname = 'id')
              ]::smallint[]
        ) THEN
            ALTER TABLE document_questions
                ADD CONSTRAINT document_questions_document_id_fkey
                FOREIGN KEY (document_id) REFERENCES documents(id) ON DELETE CASCADE;
        END IF;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
ALTER TABLE IF EXISTS document_questions DROP CONSTRAINT IF EXISTS document_questions_document_id_fkey;
ALTER TABLE IF EXISTS document_chunks DROP CONSTRAINT IF EXISTS document_chunks_document_id_fkey;
