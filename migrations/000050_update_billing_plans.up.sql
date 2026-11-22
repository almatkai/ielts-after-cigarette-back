UPDATE billing_plans SET active = false WHERE code IN ('group', 'personal');

UPDATE billing_plans SET
  title = '1 месяц',
  list_price_kzt = 14990,
  sale_price_kzt = 7990,
  access_days = 30,
  sort_order = 1,
  active = true,
  updated_at = CURRENT_TIMESTAMP
WHERE code = 'self';

INSERT INTO billing_plans (code, title, list_price_kzt, sale_price_kzt, access_days, meetings_count, meetings_kind, sort_order, active)
VALUES
  ('month_2', '2 месяца', 23990, 11990, 60, 0, NULL, 2, true),
  ('month_6', '6 месяцев', 59990, 31990, 180, 0, NULL, 3, true),
  ('year_1', '1 год', 99990, 53990, 365, 0, NULL, 4, true)
ON CONFLICT (code) DO UPDATE SET
  title = EXCLUDED.title,
  list_price_kzt = EXCLUDED.list_price_kzt,
  sale_price_kzt = EXCLUDED.sale_price_kzt,
  access_days = EXCLUDED.access_days,
  meetings_count = EXCLUDED.meetings_count,
  meetings_kind = EXCLUDED.meetings_kind,
  sort_order = EXCLUDED.sort_order,
  active = EXCLUDED.active,
  updated_at = CURRENT_TIMESTAMP;
