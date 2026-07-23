-- Cross-platform bundle contracts. Legacy subscription tables remain unchanged.
CREATE TABLE IF NOT EXISTS bundle_plans (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    product_name VARCHAR(100) NOT NULL DEFAULT '',
    price DECIMAL(20,2) NOT NULL,
    original_price DECIMAL(20,2),
    currency VARCHAR(3) NOT NULL DEFAULT 'USD',
    validity_days INTEGER NOT NULL DEFAULT 30,
    validity_unit VARCHAR(10) NOT NULL DEFAULT 'day',
    shared_daily_limit_usd DECIMAL(20,8),
    shared_monthly_limit_usd DECIMAL(20,8),
    features TEXT NOT NULL DEFAULT '',
    for_sale BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS bundle_plans_for_sale_idx ON bundle_plans(for_sale);
CREATE INDEX IF NOT EXISTS bundle_plans_sort_order_idx ON bundle_plans(sort_order);

CREATE TABLE IF NOT EXISTS bundle_plan_groups (
    id BIGSERIAL PRIMARY KEY,
    bundle_plan_id BIGINT NOT NULL REFERENCES bundle_plans(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    daily_limit_usd DECIMAL(20,8),
    monthly_limit_usd DECIMAL(20,8),
    UNIQUE(bundle_plan_id, group_id)
);
CREATE INDEX IF NOT EXISTS bundle_plan_groups_group_idx ON bundle_plan_groups(group_id);

CREATE TABLE IF NOT EXISTS bundle_subscriptions (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bundle_plan_id BIGINT NOT NULL REFERENCES bundle_plans(id) ON DELETE RESTRICT,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    starts_at TIMESTAMPTZ NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    daily_window_start TIMESTAMPTZ,
    monthly_window_start TIMESTAMPTZ,
    daily_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    monthly_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    payment_order_id BIGINT REFERENCES payment_orders(id) ON DELETE SET NULL,
    assigned_by BIGINT REFERENCES users(id) ON DELETE SET NULL,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS bundle_subscriptions_user_idx ON bundle_subscriptions(user_id);
CREATE INDEX IF NOT EXISTS bundle_subscriptions_status_idx ON bundle_subscriptions(status);
CREATE INDEX IF NOT EXISTS bundle_subscriptions_expiry_idx ON bundle_subscriptions(expires_at);
CREATE UNIQUE INDEX IF NOT EXISTS bundle_subscriptions_one_active_idx
    ON bundle_subscriptions(user_id) WHERE status = 'active';
CREATE UNIQUE INDEX IF NOT EXISTS bundle_subscriptions_one_pending_idx
    ON bundle_subscriptions(user_id) WHERE status = 'pending';

CREATE TABLE IF NOT EXISTS bundle_subscription_entitlements (
    id BIGSERIAL PRIMARY KEY,
    bundle_subscription_id BIGINT NOT NULL REFERENCES bundle_subscriptions(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE RESTRICT,
    platform VARCHAR(50) NOT NULL,
    daily_limit_usd DECIMAL(20,8),
    monthly_limit_usd DECIMAL(20,8),
    daily_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    monthly_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0,
    UNIQUE(bundle_subscription_id, group_id)
);
CREATE INDEX IF NOT EXISTS bundle_subscription_entitlements_group_platform_idx
    ON bundle_subscription_entitlements(group_id, platform);

ALTER TABLE bundle_subscription_entitlements ADD COLUMN IF NOT EXISTS daily_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0;
ALTER TABLE bundle_subscription_entitlements ADD COLUMN IF NOT EXISTS monthly_usage_usd DECIMAL(20,10) NOT NULL DEFAULT 0;

ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS bundle_plan_id BIGINT REFERENCES bundle_plans(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS payment_orders_bundle_plan_idx ON payment_orders(bundle_plan_id);
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS bundle_subscription_id BIGINT REFERENCES bundle_subscriptions(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS payment_orders_bundle_subscription_idx ON payment_orders(bundle_subscription_id);
