#!/bin/sh
# Runs once, when the data directory is first created.
# Creates the two service roles the infrastructure itself needs:
#   pgbouncer_auth  - lets PgBouncer look up client credentials (auth_query), nothing else
#   exporter        - read-only monitoring (pg_monitor)
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<SQL
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;

CREATE ROLE pgbouncer_auth LOGIN PASSWORD '${PGBOUNCER_AUTH_PASSWORD}';
CREATE FUNCTION public.pgbouncer_get_auth(p_usename text)
RETURNS TABLE (usename name, passwd text)
LANGUAGE sql SECURITY DEFINER SET search_path = pg_catalog AS
\$\$ SELECT usename, passwd FROM pg_catalog.pg_shadow WHERE usename = p_usename \$\$;
REVOKE ALL ON FUNCTION public.pgbouncer_get_auth(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.pgbouncer_get_auth(text) TO pgbouncer_auth;

CREATE ROLE exporter LOGIN PASSWORD '${POSTGRES_EXPORTER_PASSWORD}' CONNECTION LIMIT 3;
GRANT pg_monitor TO exporter;
SQL
