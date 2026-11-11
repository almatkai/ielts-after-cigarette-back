-- provider_order_id already has UNIQUE(provider,provider_order_id) in 000045.
-- Keep internal UUIDs separate; new BCC mappings are numeric and also serve as
-- the bank's stable 16-character MERCH_RN_ID. NULL is allowed during checkout.
ALTER TABLE billing_orders ADD CONSTRAINT billing_orders_bcc_numeric_order
 CHECK (provider <> 'bcc' OR provider_order_id IS NULL OR provider_order_id ~ '^[1-9][0-9]{15}$');

-- BCC recognizes duplicates by the last six digits of ORDER. A sequence avoids
-- birthday collisions; leases also guard wraparound across processes/restarts.
CREATE SEQUENCE billing_bcc_order_sequence MAXVALUE 8999999999999999 NO CYCLE;
CREATE TABLE billing_bcc_order_suffixes (
 suffix INTEGER PRIMARY KEY CHECK (suffix BETWEEN 0 AND 999999),
 reserved_until TIMESTAMPTZ NOT NULL
);
-- Preserve any pre-existing BCC identifiers and their deduplication window.
INSERT INTO billing_bcc_order_suffixes(suffix,reserved_until)
 SELECT RIGHT(provider_order_id,6)::INTEGER, MAX(created_at)+INTERVAL '48 hours 30 minutes'
 FROM billing_orders WHERE provider='bcc' AND provider_order_id IS NOT NULL
 GROUP BY RIGHT(provider_order_id,6)::INTEGER;
SELECT setval('billing_bcc_order_sequence',
 GREATEST(COALESCE(MAX(provider_order_id::BIGINT)-1000000000000000,0)+1,1),false)
 FROM billing_orders WHERE provider='bcc' AND provider_order_id IS NOT NULL;
