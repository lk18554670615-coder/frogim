CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX IF NOT EXISTS im_messages_text_search_trgm_idx
ON im_messages USING gin (lower(body->>'text') gin_trgm_ops)
WHERE message_type='text' AND recalled_at IS NULL;
