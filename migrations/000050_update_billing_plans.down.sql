UPDATE billing_plans SET active = false WHERE code IN ('month_1', 'month_2', 'month_6', 'year_1');
UPDATE billing_plans SET active = true WHERE code IN ('self', 'group', 'personal');
