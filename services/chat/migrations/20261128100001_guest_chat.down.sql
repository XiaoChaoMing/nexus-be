-- Old code would show a guest's message with a "user-xxxxxxxx" fallback name: hide them before dropping the name.
UPDATE chat_messages SET deleted_at = now(), pinned = false WHERE guest_name IS NOT NULL AND deleted_at IS NULL;

ALTER TABLE mod_actions DROP COLUMN IF EXISTS target_guest_name;
ALTER TABLE chat_messages DROP COLUMN IF EXISTS guest_name;
ALTER TABLE chat_settings DROP COLUMN IF EXISTS guest_chat;
