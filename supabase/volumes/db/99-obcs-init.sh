#!/bin/bash
# Idempotent Supabase alignment run once on first DB init:
#   * ensure the internal Supabase roles share POSTGRES_PASSWORD so GoTrue,
#     PostgREST and Realtime can authenticate;
#   * set the JWT GUCs PostgREST/Realtime read;
#   * create the supabase_realtime publication the UI subscribes to.
# Guards make each step safe if the base image already performed it.
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    DO \$\$
    DECLARE
        r text;
    BEGIN
        FOREACH r IN ARRAY ARRAY[
            'authenticator', 'supabase_auth_admin', 'supabase_storage_admin',
            'supabase_admin', 'supabase_realtime_admin', 'supabase_read_only_user'
        ] LOOP
            IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = r) THEN
                EXECUTE format('ALTER ROLE %I WITH LOGIN PASSWORD %L', r, '${POSTGRES_PASSWORD}');
            END IF;
        END LOOP;
    END
    \$\$;

    ALTER DATABASE "${POSTGRES_DB}" SET "app.settings.jwt_secret" TO '${JWT_SECRET}';
    ALTER DATABASE "${POSTGRES_DB}" SET "app.settings.jwt_exp" TO 3600;

    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
            CREATE PUBLICATION supabase_realtime;
        END IF;
    END
    \$\$;
EOSQL

echo "OBCS: supabase roles/jwt/realtime alignment complete."
