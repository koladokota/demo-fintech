-- Virtual coin rewards for product missions. XP and real-world money are unaffected.
-- period_key is a UTC date for daily missions and 'once' for one-time missions.
CREATE TABLE IF NOT EXISTS mission_claims (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    mission_id TEXT NOT NULL CHECK (mission_id IN ('daily_checkin', 'first_purchase', 'reach_level_two')),
    period_key TEXT NOT NULL CHECK (length(period_key) BETWEEN 1 AND 10),
    reward_coins BIGINT NOT NULL CHECK (reward_coins > 0),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, mission_id, period_key)
);
CREATE INDEX IF NOT EXISTS idx_mission_claims_user_date
    ON mission_claims(user_id, claimed_at DESC);
