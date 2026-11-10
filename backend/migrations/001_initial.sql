CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  virtual_balance BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS sessions (
  token_hash TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS wallet_transactions (
  id BIGSERIAL PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  amount BIGINT NOT NULL CHECK (amount <> 0),
  type TEXT NOT NULL,
  description TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_wallet_transactions_user ON wallet_transactions(user_id, created_at DESC);
CREATE TABLE IF NOT EXISTS shop_items (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  description TEXT NOT NULL,
  price BIGINT NOT NULL CHECK (price >= 0),
  rarity TEXT NOT NULL CHECK (rarity IN ('common','rare','epic','legendary')),
  icon TEXT NOT NULL DEFAULT 'package',
  active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE IF NOT EXISTS user_inventory (
  id BIGSERIAL PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  item_id TEXT NOT NULL REFERENCES shop_items(id),
  purchased_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (user_id, item_id)
);
CREATE INDEX IF NOT EXISTS idx_user_inventory_user ON user_inventory(user_id, purchased_at DESC);
