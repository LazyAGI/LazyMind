#!/bin/sh
set -e

# Wait for PostgreSQL to accept connections to avoid startup race.
max_attempts="${DB_BOOTSTRAP_MAX_ATTEMPTS:-60}"
attempt=1
until pg_isready -h db -U root -d postgres >/dev/null 2>&1; do
  if [ "$attempt" -ge "$max_attempts" ]; then
    echo "db-bootstrap: database did not become ready after ${max_attempts} attempts" >&2
    exit 1
  fi
  echo "db-bootstrap: waiting for database (${attempt}/${max_attempts})..."
  attempt=$((attempt + 1))
  sleep 2
done

psql -v ON_ERROR_STOP=1 -h db -U root -d postgres <<'SQL'
SELECT 'CREATE ROLE app LOGIN CREATEDB PASSWORD ''app'''
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'app')
\gexec

ALTER ROLE app WITH LOGIN CREATEDB PASSWORD 'app';

SELECT 'CREATE DATABASE authservice'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'authservice')
\gexec

SELECT 'CREATE DATABASE core'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'core')
\gexec

SELECT 'CREATE DATABASE doc_task'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'doc_task')
\gexec

SELECT 'CREATE DATABASE app OWNER app'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'app')
\gexec

SELECT 'CREATE DATABASE scan_control_plane'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'scan_control_plane')
\gexec

ALTER DATABASE app OWNER TO app;
GRANT ALL PRIVILEGES ON DATABASE app TO app;
SQL

psql -v ON_ERROR_STOP=1 -h db -U root -d app <<'SQL'
GRANT USAGE, CREATE ON SCHEMA public TO app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON SEQUENCES TO app;
SQL