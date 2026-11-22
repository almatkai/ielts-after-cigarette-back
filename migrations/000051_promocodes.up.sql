CREATE TABLE promocodes (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT NOT NULL,
  discount_type TEXT NOT NULL CHECK (discount_type IN ('PERCENT', 'FIXED', 'FREE')),
  discount_value INTEGER NOT NULL DEFAULT 0 CHECK (discount_value >= 0),
  plan_code TEXT REFERENCES billing_plans(code) ON DELETE SET NULL,
  min_price_kzt BIGINT CHECK (min_price_kzt IS NULL OR min_price_kzt >= 0),
  allow_on_sale BOOLEAN NOT NULL DEFAULT true,
  max_uses INTEGER CHECK (max_uses IS NULL OR max_uses > 0),
  used_count INTEGER NOT NULL DEFAULT 0 CHECK (used_count >= 0),
  max_uses_per_user INTEGER NOT NULL DEFAULT 1 CHECK (max_uses_per_user > 0),
  starts_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMPTZ CHECK (expires_at IS NULL OR expires_at > starts_at),
  is_active BOOLEAN NOT NULL DEFAULT true,
  partner_user_id UUID REFERENCES users(id) ON DELETE SET NULL,
  created_by UUID REFERENCES users(id) ON DELETE SET NULL,
  note TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE UNIQUE INDEX promocodes_code_lower_unique ON promocodes (lower(btrim(code)));
CREATE INDEX promocodes_partner_user_idx ON promocodes (partner_user_id) WHERE partner_user_id IS NOT NULL;
CREATE INDEX promocodes_active_window_idx ON promocodes (is_active, starts_at, expires_at);

CREATE TABLE promocode_redemptions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  promocode_id UUID NOT NULL REFERENCES promocodes(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  order_id UUID REFERENCES billing_orders(id) ON DELETE SET NULL,
  discount_amount BIGINT NOT NULL CHECK (discount_amount >= 0),
  final_price BIGINT NOT NULL CHECK (final_price >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX promocode_redemptions_promo_user_idx ON promocode_redemptions (promocode_id, user_id);
CREATE INDEX promocode_redemptions_user_idx ON promocode_redemptions (user_id);
CREATE INDEX promocode_redemptions_order_idx ON promocode_redemptions (order_id) WHERE order_id IS NOT NULL;

ALTER TABLE billing_orders
  ADD COLUMN IF NOT EXISTS promocode_id UUID REFERENCES promocodes(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS discount_amount BIGINT NOT NULL DEFAULT 0 CHECK (discount_amount >= 0);

ALTER TABLE user_entitlements DROP CONSTRAINT IF EXISTS user_entitlements_source_check;
ALTER TABLE user_entitlements ADD CONSTRAINT user_entitlements_source_check CHECK (source IN ('PAYMENT', 'ADMIN', 'BONUS', 'PROMOCODE'));
