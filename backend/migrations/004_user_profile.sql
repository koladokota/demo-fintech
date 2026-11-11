-- Existing accounts keep their current name and get the default initial avatar.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS avatar_key TEXT NOT NULL DEFAULT 'initial';

ALTER TABLE users
    ADD CONSTRAINT users_avatar_key_allowed
    CHECK (avatar_key IN ('initial', 'rocket', 'planet', 'bolt', 'star', 'shield'));
