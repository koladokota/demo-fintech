-- Number guessing sessions. The secret never leaves the server while the session is active.
CREATE TABLE IF NOT EXISTS game_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_number SMALLINT NOT NULL CHECK (secret_number BETWEEN 1 AND 20),
    attempts_left SMALLINT NOT NULL DEFAULT 5 CHECK (attempts_left BETWEEN 0 AND 5),
    status TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'won', 'lost')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finished_at TIMESTAMPTZ,
    CONSTRAINT game_session_state_valid CHECK (
        (status = 'active' AND attempts_left > 0 AND finished_at IS NULL)
        OR (status IN ('won', 'lost') AND finished_at IS NOT NULL)
    )
);

-- Only one active session per player, even under concurrent /start calls.
CREATE UNIQUE INDEX IF NOT EXISTS idx_game_sessions_one_active_per_user
    ON game_sessions(user_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_game_sessions_user_latest
    ON game_sessions(user_id, created_at DESC, id DESC);

-- Winning a number game grants XP at most once per UTC calendar day.
CREATE TABLE IF NOT EXISTS game_daily_rewards (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    reward_date DATE NOT NULL,
    game_id UUID NOT NULL UNIQUE REFERENCES game_sessions(id) ON DELETE CASCADE,
    awarded_xp BIGINT NOT NULL CHECK (awarded_xp > 0),
    awarded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, reward_date)
);
