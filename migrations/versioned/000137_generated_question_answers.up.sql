CREATE TABLE IF NOT EXISTS generated_question_answers (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    knowledge_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL REFERENCES chunks(id) ON DELETE CASCADE,
    question_id VARCHAR(128) NOT NULL DEFAULT '',
    question_position INTEGER NOT NULL,
    question_revision INTEGER,
    question TEXT NOT NULL,
    source_hash VARCHAR(64) NOT NULL,
    answer TEXT NOT NULL DEFAULT '',
    answer_status VARCHAR(32) NOT NULL,
    answer_model_id VARCHAR(255) NOT NULL DEFAULT '',
    source_revision_status VARCHAR(32) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_generated_question_answers_chunk
    ON generated_question_answers(tenant_id, chunk_id);
