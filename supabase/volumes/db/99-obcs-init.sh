#!/bin/bash
set -euo pipefail

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<-EOSQL
    -- 1. Create missing schemas/roles just in case the Supabase migrations aborted
    CREATE SCHEMA IF NOT EXISTS _realtime;
    
    DO \$\$
    BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'supabase_admin') THEN
            CREATE ROLE supabase_admin WITH SUPERUSER LOGIN PASSWORD '${POSTGRES_PASSWORD}';
        END IF;
    END
    \$\$;

    ALTER SCHEMA _realtime OWNER TO supabase_admin;

    -- 2. Align passwords for all Supabase internal roles so the stack can connect
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

    -- 3. Set required JWT configuration
    ALTER DATABASE "${POSTGRES_DB}" SET "app.settings.jwt_secret" TO '${JWT_SECRET}';
    ALTER DATABASE "${POSTGRES_DB}" SET "app.settings.jwt_exp" TO 3600;
EOSQL

echo "OBCS: supabase roles/jwt/realtime alignment complete."
