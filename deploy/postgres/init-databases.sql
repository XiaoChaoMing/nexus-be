-- Idempotent: CREATE DATABASE has no IF NOT EXISTS, so each statement is generated only when missing and run by \gexec.
SELECT format('CREATE DATABASE %I', name)
FROM unnest(ARRAY[
    'nxl_identity',
    'nxl_channel',
    'nxl_stream',
    'nxl_chat',
    'nxl_safety',
    'nxl_notification',
    'nxl_billing'
]) AS name
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = name)
\gexec
