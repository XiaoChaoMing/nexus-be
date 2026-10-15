DROP TABLE IF EXISTS replica_backfills;
DROP INDEX IF EXISTS chat_messages_user_idx;
DROP INDEX IF EXISTS channel_roles_list_idx;
DROP INDEX IF EXISTS mod_actions_restrict_idx;

ALTER TABLE channels DROP COLUMN IF EXISTS status, DROP COLUMN IF EXISTS avatar_url, DROP COLUMN IF EXISTS display_name,
    DROP COLUMN IF EXISTS handle;

ALTER TABLE blocked_terms DROP CONSTRAINT IF EXISTS blocked_terms_term_length;
ALTER TABLE blocked_terms DROP COLUMN IF EXISTS display_term;

ALTER TABLE chat_settings DROP COLUMN IF EXISTS block_links;

-- UNTIMEOUT rows cannot exist under the old CHECK (their audit copy stays in safety-svc).
DELETE FROM mod_actions WHERE type = 'UNTIMEOUT';
ALTER TABLE mod_actions DROP CONSTRAINT IF EXISTS mod_actions_untimeout_target;
ALTER TABLE mod_actions DROP COLUMN IF EXISTS actor_role, DROP COLUMN IF EXISTS stream_id;
ALTER TABLE mod_actions DROP CONSTRAINT mod_actions_type_check;
ALTER TABLE mod_actions ADD CONSTRAINT mod_actions_type_check CHECK (type IN ('DELETE', 'TIMEOUT', 'BAN', 'UNBAN', 'PIN', 'UNPIN'));
