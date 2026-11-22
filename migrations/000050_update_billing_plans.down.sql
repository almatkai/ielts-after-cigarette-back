UPDATE billing_plans SET active = false WHERE code IN ('month_2', 'month_6', 'year_1');
UPDATE billing_plans SET
  title = 'Платформа самостоятельно',
  list_price_kzt = 9990,
  sale_price_kzt = 4990,
  access_days = 30,
  sort_order = 1,
  active = true
WHERE code = 'self';
UPDATE billing_plans SET active = true WHERE code IN ('group', 'personal');
