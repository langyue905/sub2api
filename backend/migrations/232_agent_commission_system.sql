-- One-level agent/customer commission system.
--
-- This is intentionally separate from user_affiliates: that table implements
-- recharge invitation rebates, while agents earn a percentage of a direct
-- customer's actual usage cost.  All statements are safe to replay.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS agent_id BIGINT REFERENCES users(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_users_agent_id
    ON users(agent_id)
    WHERE agent_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS agent_profiles (
    user_id                 BIGINT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    enabled                 BOOLEAN NOT NULL DEFAULT TRUE,
    manual_rate_bps         INTEGER NOT NULL DEFAULT 0,
    current_rate_bps        INTEGER NOT NULL DEFAULT 700,
    total_customer_usage    NUMERIC(20,8) NOT NULL DEFAULT 0,
    pending_commission      NUMERIC(20,8) NOT NULL DEFAULT 0,
    transferred_amount      NUMERIC(20,8) NOT NULL DEFAULT 0,
    withdrawing_amount      NUMERIC(20,8) NOT NULL DEFAULT 0,
    withdrawn_amount        NUMERIC(20,8) NOT NULL DEFAULT 0,
    total_commission        NUMERIC(20,8) NOT NULL DEFAULT 0,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_profiles_manual_rate_bps_check
        CHECK (manual_rate_bps IN (0, 700, 1000, 1300)),
    CONSTRAINT agent_profiles_current_rate_bps_check
        CHECK (current_rate_bps IN (700, 1000, 1300)),
    CONSTRAINT agent_profiles_amounts_nonnegative_check
        CHECK (
            total_customer_usage >= 0
            AND pending_commission >= 0
            AND transferred_amount >= 0
            AND withdrawing_amount >= 0
            AND withdrawn_amount >= 0
            AND total_commission >= 0
        )
);

CREATE INDEX IF NOT EXISTS idx_agent_profiles_enabled
    ON agent_profiles(enabled);

CREATE TABLE IF NOT EXISTS agent_commissions (
    id                    BIGSERIAL PRIMARY KEY,
    agent_user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    customer_user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    usage_log_id          BIGINT NULL REFERENCES usage_logs(id) ON DELETE SET NULL,
    request_id            VARCHAR(255) NOT NULL DEFAULT '',
    idempotency_key       VARCHAR(255) NOT NULL UNIQUE,
    model_name            VARCHAR(255) NOT NULL DEFAULT '',
    group_name            VARCHAR(255) NOT NULL DEFAULT '',
    usage_amount          NUMERIC(20,8) NOT NULL DEFAULT 0,
    commission_amount     NUMERIC(20,8) NOT NULL DEFAULT 0,
    commission_rate_bps   INTEGER NOT NULL DEFAULT 700,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT agent_commissions_amounts_nonnegative_check
        CHECK (usage_amount >= 0 AND commission_amount >= 0),
    CONSTRAINT agent_commissions_rate_bps_check
        CHECK (commission_rate_bps IN (700, 1000, 1300))
);

CREATE INDEX IF NOT EXISTS idx_agent_commissions_agent_created
    ON agent_commissions(agent_user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_agent_commissions_customer_created
    ON agent_commissions(customer_user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_agent_commissions_request_id
    ON agent_commissions(request_id)
    WHERE request_id <> '';

CREATE TABLE IF NOT EXISTS agent_withdrawals (
    id                BIGSERIAL PRIMARY KEY,
    agent_user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    amount            NUMERIC(20,8) NOT NULL,
    payment_account   VARCHAR(255) NOT NULL DEFAULT '',
    payment_qr_code   TEXT NOT NULL DEFAULT '',
    note              VARCHAR(500) NOT NULL DEFAULT '',
    admin_note        VARCHAR(500) NOT NULL DEFAULT '',
    status            VARCHAR(20) NOT NULL DEFAULT 'pending',
    processed_by      BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at      TIMESTAMPTZ NULL,
    CONSTRAINT agent_withdrawals_amount_positive_check CHECK (amount > 0),
    CONSTRAINT agent_withdrawals_status_check CHECK (status IN ('pending', 'paid', 'rejected'))
);

CREATE INDEX IF NOT EXISTS idx_agent_withdrawals_agent_created
    ON agent_withdrawals(agent_user_id, created_at DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_agent_withdrawals_status_created
    ON agent_withdrawals(status, created_at DESC, id DESC);

-- Database-level guard for the one-level invariant.  The service performs the
-- same check before assignment, while this trigger protects direct SQL writes
-- and concurrent writers.  A customer cannot be assigned to an agent that is
-- itself assigned to another agent, and self-assignment is always rejected.
CREATE OR REPLACE FUNCTION sub2api_enforce_agent_one_level()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    parent_agent_id BIGINT;
    target_deleted_at TIMESTAMPTZ;
BEGIN
    IF NEW.agent_id IS NULL THEN
        RETURN NEW;
    END IF;

    IF NEW.id = NEW.agent_id THEN
        RAISE EXCEPTION 'agent cannot be assigned to itself'
            USING ERRCODE = '23514';
    END IF;

    -- A user with an agent profile is an agent, never a customer.  This check
    -- closes the path where a direct SQL writer assigns an existing agent
    -- profile without going through the service layer.
    IF EXISTS (SELECT 1 FROM agent_profiles ap WHERE ap.user_id = NEW.id) THEN
        RAISE EXCEPTION 'agent user cannot be assigned as a customer'
            USING ERRCODE = '23514';
    END IF;

    SELECT u.agent_id, u.deleted_at
      INTO parent_agent_id, target_deleted_at
      FROM users u
     WHERE u.id = NEW.agent_id
     FOR UPDATE;

    IF NOT FOUND OR target_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'agent user does not exist or is deleted'
            USING ERRCODE = '23503';
    END IF;

    IF parent_agent_id IS NOT NULL THEN
        RAISE EXCEPTION 'nested agent relationships are not allowed'
            USING ERRCODE = '23514';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM agent_profiles ap
        WHERE ap.user_id = NEW.agent_id AND ap.enabled = TRUE
    ) THEN
        RAISE EXCEPTION 'target user is not an enabled agent'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS users_agent_one_level_guard ON users;
CREATE TRIGGER users_agent_one_level_guard
    BEFORE INSERT OR UPDATE OF agent_id ON users
    FOR EACH ROW
    EXECUTE FUNCTION sub2api_enforce_agent_one_level();

-- Keep the role boundary symmetric: creating an agent profile for a user that
-- is already somebody's customer would otherwise create a hidden second level.
CREATE OR REPLACE FUNCTION sub2api_enforce_agent_profile_one_level()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    customer_agent_id BIGINT;
    customer_deleted_at TIMESTAMPTZ;
BEGIN
    SELECT u.agent_id, u.deleted_at
      INTO customer_agent_id, customer_deleted_at
      FROM users u
     WHERE u.id = NEW.user_id
     FOR UPDATE;

    IF NOT FOUND OR customer_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'agent user does not exist or is deleted'
            USING ERRCODE = '23503';
    END IF;

    IF customer_agent_id IS NOT NULL THEN
        RAISE EXCEPTION 'agent user cannot also be a customer'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agent_profiles_one_level_guard ON agent_profiles;
CREATE TRIGGER agent_profiles_one_level_guard
    BEFORE INSERT OR UPDATE OF user_id ON agent_profiles
    FOR EACH ROW
    EXECUTE FUNCTION sub2api_enforce_agent_profile_one_level();

-- Commission rows are immutable evidence of a direct customer usage event.
-- Reject rows that do not describe the current direct relationship so a raw
-- SQL write cannot create an indirect or fabricated commission.
CREATE OR REPLACE FUNCTION sub2api_enforce_agent_commission_one_level()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    customer_agent_id BIGINT;
    agent_parent_id BIGINT;
BEGIN
    IF NEW.agent_user_id = NEW.customer_user_id THEN
        RAISE EXCEPTION 'agent commission cannot reference itself'
            USING ERRCODE = '23514';
    END IF;

    SELECT u.agent_id
      INTO customer_agent_id
      FROM users u
     WHERE u.id = NEW.customer_user_id
       AND u.deleted_at IS NULL
     FOR UPDATE;
    IF NOT FOUND OR customer_agent_id IS DISTINCT FROM NEW.agent_user_id THEN
        RAISE EXCEPTION 'agent commissions must reference a direct customer'
            USING ERRCODE = '23514';
    END IF;

    IF EXISTS (SELECT 1 FROM agent_profiles ap WHERE ap.user_id = NEW.customer_user_id) THEN
        RAISE EXCEPTION 'agent commissions cannot be generated for an agent customer'
            USING ERRCODE = '23514';
    END IF;

    SELECT u.agent_id
      INTO agent_parent_id
      FROM users u
     WHERE u.id = NEW.agent_user_id
       AND u.deleted_at IS NULL
     FOR UPDATE;
    IF NOT FOUND OR agent_parent_id IS NOT NULL THEN
        RAISE EXCEPTION 'agent commissions cannot be indirect'
            USING ERRCODE = '23514';
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM agent_profiles ap
        WHERE ap.user_id = NEW.agent_user_id AND ap.enabled = TRUE
    ) THEN
        RAISE EXCEPTION 'agent commission target is not enabled'
            USING ERRCODE = '23514';
    END IF;

    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS agent_commissions_one_level_guard ON agent_commissions;
CREATE TRIGGER agent_commissions_one_level_guard
    BEFORE INSERT OR UPDATE OF agent_user_id, customer_user_id ON agent_commissions
    FOR EACH ROW
    EXECUTE FUNCTION sub2api_enforce_agent_commission_one_level();

COMMENT ON TABLE agent_profiles IS '代理配置与佣金汇总；代理关系严格只有一层';
COMMENT ON TABLE agent_commissions IS '按直属客户实际消费金额产生的幂等代理佣金';
COMMENT ON TABLE agent_withdrawals IS '代理佣金提现申请与管理员处理记录';
COMMENT ON COLUMN users.agent_id IS '直属代理用户 ID；代理用户自身不得再有 agent_id';
COMMENT ON COLUMN agent_profiles.total_customer_usage IS '直属客户 usage_logs.actual_cost 累计';
