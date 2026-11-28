-- Guests (no account) may send messages in the channel's rooms; off by default.
ALTER TABLE chat_settings ADD COLUMN guest_chat boolean NOT NULL DEFAULT false;

-- NULL for an account's message; for a guest's message, the nickname it was sent under (user_id then holds the
-- guest key derived from the browser's anonId, never a users.id). Added on the partitioned parent, so every
-- existing and future daily partition gets it.
ALTER TABLE chat_messages ADD COLUMN guest_name varchar(20);

-- The guest's nickname when target_user_id is a guest key (restrictions list in the Studio).
ALTER TABLE mod_actions ADD COLUMN target_guest_name varchar(20);
