ALTER TABLE mod_actions DROP CONSTRAINT mod_actions_type_check;
ALTER TABLE mod_actions ADD CONSTRAINT mod_actions_type_check
    CHECK (type IN ('DELETE', 'TIMEOUT', 'UNTIMEOUT', 'BAN', 'UNBAN', 'PIN', 'UNPIN'));
ALTER TABLE mod_actions
    ADD COLUMN stream_id  uuid,
    ADD COLUMN actor_role varchar(10) CHECK (actor_role IN ('OWNER', 'MODERATOR', 'ADMIN'));
ALTER TABLE mod_actions ADD CONSTRAINT mod_actions_untimeout_target CHECK (type <> 'UNTIMEOUT' OR target_user_id IS NOT NULL);
-- Restrictions list (latest action per user) and the send-time lookup.
CREATE INDEX mod_actions_restrict_idx ON mod_actions (channel_id, target_user_id, created_at DESC)
    WHERE type IN ('BAN', 'UNBAN', 'TIMEOUT', 'UNTIMEOUT');

ALTER TABLE chat_settings ADD COLUMN block_links boolean NOT NULL DEFAULT false;

-- term is the normalized form (NFC, lower case, no diacritics, single spaces); display_term is what the user typed.
ALTER TABLE blocked_terms ADD COLUMN display_term varchar(100);
UPDATE blocked_terms SET display_term = term;
ALTER TABLE blocked_terms ALTER COLUMN display_term SET NOT NULL;
ALTER TABLE blocked_terms ADD CONSTRAINT blocked_terms_term_length CHECK (char_length(term) BETWEEN 2 AND 100);

-- Channel copy gains what /v1/me/channel-roles and the suspension rules need (channel.created / updated / suspended).
ALTER TABLE channels
    ADD COLUMN handle       varchar(25),
    ADD COLUMN display_name varchar(50),
    ADD COLUMN avatar_url   text,
    ADD COLUMN status       varchar(10) NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'SUSPENDED'));

CREATE INDEX channel_roles_list_idx ON channel_roles (channel_id, role, created_at DESC);
-- Evidence (a user's last messages in a room) and clearing a user's messages on timeout/ban.
CREATE INDEX chat_messages_user_idx ON chat_messages (stream_id, user_id, created_at DESC);

CREATE TABLE replica_backfills (
    name         varchar(30) PRIMARY KEY,
    completed_at timestamptz NOT NULL DEFAULT now()
);
