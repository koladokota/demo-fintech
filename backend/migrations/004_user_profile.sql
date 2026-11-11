-- Existing accounts keep their names and receive the default initial avatar.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS avatar_key TEXT NOT NULL DEFAULT 'initial';

-- Safe even when an environment previously applied this SQL manually.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'users_avatar_key_allowed'
          AND conrelid = 'users'::regclass
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT users_avatar_key_allowed
            CHECK (avatar_key IN ('initial', 'rocket', 'planet', 'bolt', 'star', 'shield'));
    END IF;
END
$$;
