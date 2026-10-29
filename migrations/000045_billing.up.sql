CREATE TABLE billing_plans (
 code TEXT PRIMARY KEY, title TEXT NOT NULL,
 list_price_kzt BIGINT NOT NULL CHECK (list_price_kzt >= 0),
 sale_price_kzt BIGINT CHECK (sale_price_kzt >= 0 AND sale_price_kzt <= list_price_kzt),
 access_days INTEGER NOT NULL CHECK (access_days > 0),
 meetings_count INTEGER NOT NULL DEFAULT 0 CHECK (meetings_count >= 0),
 meetings_kind TEXT CHECK (meetings_kind IN ('GROUP','PERSONAL')),
 sort_order INTEGER NOT NULL DEFAULT 0, active BOOLEAN NOT NULL DEFAULT true,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO billing_plans (code,title,list_price_kzt,sale_price_kzt,access_days,meetings_count,meetings_kind,sort_order) VALUES
 ('self','Платформа самостоятельно',9990,4990,30,0,NULL,1),
 ('group','Платформа + 4 встречи в мини-группе',24990,14990,30,4,'GROUP',2),
 ('personal','Платформа + 4 личные встречи по 30 минут',54990,34990,30,4,'PERSONAL',3);
CREATE TABLE billing_settings (
 id INTEGER PRIMARY KEY CHECK (id=1),
 free_listening INTEGER NOT NULL DEFAULT 3 CHECK (free_listening >= 0),
 free_reading INTEGER NOT NULL DEFAULT 3 CHECK (free_reading >= 0),
 free_writing INTEGER NOT NULL DEFAULT 3 CHECK (free_writing >= 0),
 free_speaking INTEGER NOT NULL DEFAULT 3 CHECK (free_speaking >= 0),
 free_assistant_daily_limit INTEGER NOT NULL DEFAULT 5 CHECK (free_assistant_daily_limit >= 0),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO billing_settings (id) VALUES (1);
CREATE TABLE billing_orders (
 id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 plan_code TEXT NOT NULL REFERENCES billing_plans(code),
 list_price_kzt BIGINT NOT NULL CHECK (list_price_kzt >= 0),
 amount_kzt BIGINT NOT NULL CHECK (amount_kzt >= 0),
 currency TEXT NOT NULL DEFAULT 'KZT' CHECK (currency='KZT'),
 status TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING','PAID','FAILED','CANCELLED','REFUNDED')),
 provider TEXT NOT NULL, provider_order_id TEXT,
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, paid_at TIMESTAMPTZ,
 payload JSONB NOT NULL DEFAULT '{}', UNIQUE(provider,provider_order_id)
);
CREATE INDEX billing_orders_user_created_idx ON billing_orders(user_id,created_at DESC);
CREATE TABLE user_entitlements (
 id UUID PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 plan_code TEXT NOT NULL REFERENCES billing_plans(code),
 order_id UUID UNIQUE REFERENCES billing_orders(id) ON DELETE CASCADE,
 source TEXT NOT NULL CHECK (source IN ('PAYMENT','ADMIN','BONUS')),
 starts_at TIMESTAMPTZ NOT NULL, ends_at TIMESTAMPTZ NOT NULL CHECK (ends_at > starts_at),
 revoked_at TIMESTAMPTZ,
 meetings_total INTEGER NOT NULL DEFAULT 0 CHECK (meetings_total >= 0),
 meetings_used INTEGER NOT NULL DEFAULT 0 CHECK (meetings_used >= 0 AND meetings_used <= meetings_total),
 granted_by UUID REFERENCES users(id) ON DELETE CASCADE, note TEXT NOT NULL DEFAULT '',
 created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX user_entitlements_user_ends_idx ON user_entitlements(user_id,ends_at);
CREATE TABLE free_usage (
 user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
 skill TEXT NOT NULL CHECK (skill IN ('listening','reading','writing','speaking')),
 source_kind TEXT NOT NULL CHECK (source_kind IN ('ATTEMPT','FULL_MOCK_SESSION')),
 source_id UUID NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY (source_kind,source_id,skill)
);
CREATE INDEX free_usage_user_skill_idx ON free_usage(user_id,skill);
