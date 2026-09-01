-- Allow a user to be both a direct customer and an agent.
--
-- "One level" describes commission payout, not the shape of the relationship
-- graph: every usage event pays only the user's current direct agent, and is
-- never walked farther up the graph.

CREATE OR REPLACE FUNCTION sub2api_enforce_agent_one_level()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_deleted_at TIMESTAMPTZ;
BEGIN
    IF NEW.agent_id IS NULL THEN
        RETURN NEW;
    END IF;

    IF NEW.id = NEW.agent_id THEN
        RAISE EXCEPTION 'agent cannot be assigned to itself'
            USING ERRCODE = '23514';
    END IF;

    SELECT u.deleted_at
      INTO target_deleted_at
      FROM users u
     WHERE u.id = NEW.agent_id
     FOR UPDATE;

    IF NOT FOUND OR target_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'agent user does not exist or is deleted'
            USING ERRCODE = '23503';
    END IF;

    -- An agent may itself have an upstream agent. The direct edge remains
    -- valid because commission recording never follows that upstream edge.
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

CREATE OR REPLACE FUNCTION sub2api_enforce_agent_profile_one_level()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    agent_deleted_at TIMESTAMPTZ;
BEGIN
    SELECT u.deleted_at
      INTO agent_deleted_at
      FROM users u
     WHERE u.id = NEW.user_id
     FOR UPDATE;

    IF NOT FOUND OR agent_deleted_at IS NOT NULL THEN
        RAISE EXCEPTION 'agent user does not exist or is deleted'
            USING ERRCODE = '23503';
    END IF;

    -- agent_id and agent_profiles are independent roles. A user can be an
    -- agent while also remaining a direct customer of another agent.
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION sub2api_enforce_agent_commission_one_level()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    customer_agent_id BIGINT;
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

    -- The agent may also be a customer, and the customer may also be an
    -- agent. Only the relationship on this row determines the payout.
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

COMMENT ON TABLE agent_profiles IS '代理配置与佣金汇总；佣金仅按直属客户关系结算，允许代理兼任客户';
COMMENT ON COLUMN users.agent_id IS '直属代理用户 ID；同一用户可同时是客户和代理；佣金不向上递归';
