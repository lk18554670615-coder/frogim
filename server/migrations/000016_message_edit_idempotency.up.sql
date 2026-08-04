CREATE TABLE IF NOT EXISTS im_message_edit_requests(
  message_id text NOT NULL REFERENCES im_messages(id) ON DELETE CASCADE,
  edit_id text NOT NULL,
  body jsonb NOT NULL,
  version integer NOT NULL CHECK(version>=0),
  created_at timestamptz NOT NULL,
  PRIMARY KEY(message_id,edit_id)
);
