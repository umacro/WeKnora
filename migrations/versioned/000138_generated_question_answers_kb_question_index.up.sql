CREATE INDEX IF NOT EXISTS idx_generated_question_answers_kb_question
    ON generated_question_answers(tenant_id, knowledge_base_id, chunk_id, question_revision);
