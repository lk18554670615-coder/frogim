-- psql migration wrapper. The same schema is embedded and applied idempotently
-- by full-mode startup, so a separate migrate binary is not required.
\ir ../internal/store/schema.sql
