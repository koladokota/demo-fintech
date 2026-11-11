-- A referral is a registration attribution, not a financial reward.
-- Existing accounts receive stable invite codes; past data stays intact.
ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_code TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_by TEXT REFERENCES users(id) ON DELETE SET NULL;

-- Generates short unique invitation identifiers for pre-existing accounts.
UPDATE users
SET referral_code = UPPER(SUBSTRING(MD5(id), 1, 16))
WHERE referral_code IS NULL;

CREATE UNIQUE INDEX IF NOT EXISTS users_referral_code_unique
    ON users(referral_code) WHERE referral_code IS NOT NULL;
CREATE INDEX IF NOT EXISTS users_referred_by_idx
    ON users(referred_by) WHERE referred_by IS NOT NULL;

-- Do not allow an account to count itself as its own referral.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'users_not_self_referred'
          AND conrelid = 'users'::regclass
    ) THEN
        ALTER TABLE users ADD CONSTRAINT users_not_self_referred
            CHECK (referred_by IS NULL OR referred_by <> id);
    END IF;
END
$$;
