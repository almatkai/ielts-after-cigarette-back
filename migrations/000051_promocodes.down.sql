ALTER TABLE user_entitlements DROP CONSTRAINT IF EXISTS user_entitlements_source_check;
ALTER TABLE user_entitlements ADD CONSTRAINT user_entitlements_source_check CHECK (source IN ('PAYMENT', 'ADMIN', 'BONUS'));

ALTER TABLE billing_orders
  DROP COLUMN IF EXISTS discount_amount,
  DROP COLUMN IF EXISTS promocode_id;

DROP TABLE IF EXISTS promocode_redemptions;
DROP TABLE IF EXISTS promocodes;
