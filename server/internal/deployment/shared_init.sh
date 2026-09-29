#!/bin/sh
# Executed by the pinned PostgreSQL Linux image only when its NEW volume is empty.
set -eu
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
  -v platform_password="$PLATFORM_DATABASE_PASSWORD" \
  -v enterprise_password="$ENTERPRISE_DATABASE_PASSWORD" <<'SQL'
CREATE ROLE platform LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'platform_password';
CREATE ROLE enterprise LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD :'enterprise_password';
CREATE DATABASE platform OWNER platform;
CREATE DATABASE enterprise OWNER enterprise;
REVOKE ALL ON DATABASE platform FROM PUBLIC;
REVOKE ALL ON DATABASE enterprise FROM PUBLIC;
GRANT CONNECT, TEMPORARY ON DATABASE platform TO platform;
GRANT CONNECT, TEMPORARY ON DATABASE enterprise TO enterprise;
CREATE SCHEMA frogim_shared;
CREATE TABLE frogim_shared.redis_reservations(db integer PRIMARY KEY CHECK(db BETWEEN 0 AND 15), database_name text NOT NULL UNIQUE);
INSERT INTO frogim_shared.redis_reservations VALUES(0,'platform'),(1,'enterprise');
\connect enterprise
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
SQL
