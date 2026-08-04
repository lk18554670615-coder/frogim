CREATE INDEX IF NOT EXISTS im_messages_media_id_idx
ON im_messages((body->>'mediaId'))
WHERE message_type IN ('image','audio','video','file');
