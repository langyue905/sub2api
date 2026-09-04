-- One-time migration of the legacy agent relationships into native affiliates.
-- Run with psql against the Sub2API database after the new application image is live.
-- The migration is transactional and guarded by a marker, so re-running it is safe.

BEGIN;

SELECT pg_advisory_xact_lock(hashtext('sub2api:legacy-agent-affiliates:v1'));

CREATE TABLE IF NOT EXISTS legacy_agent_affiliate_migrations (
    migration_key TEXT PRIMARY KEY,
    executed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    relation_count INTEGER NOT NULL,
    agent_count INTEGER NOT NULL,
    credited_amount NUMERIC(20, 8) NOT NULL
);

CREATE TEMP TABLE legacy_agent_rows ON COMMIT DROP AS
SELECT customer.id AS customer_id,
       customer.agent_id AS agent_id,
       COALESCE(profile.pending_commission, 0)::NUMERIC(20, 8) AS pending_commission
FROM users AS customer
JOIN users AS agent ON agent.id = customer.agent_id
LEFT JOIN agent_profiles AS profile ON profile.user_id = agent.id
WHERE customer.agent_id IS NOT NULL
  AND NOT EXISTS (
      SELECT 1
      FROM legacy_agent_affiliate_migrations
      WHERE migration_key = 'legacy-agent-affiliates-v1'
  );

CREATE TEMP TABLE legacy_agent_totals ON COMMIT DROP AS
SELECT agent_id, MAX(pending_commission)::NUMERIC(20, 8) AS pending_commission
FROM legacy_agent_rows
GROUP BY agent_id;

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM legacy_agent_rows AS row
        JOIN user_affiliates AS affiliate ON affiliate.user_id = row.customer_id
        WHERE affiliate.inviter_id IS NOT NULL
          AND affiliate.inviter_id <> row.agent_id
    ) THEN
        RAISE EXCEPTION 'legacy agent migration found an existing conflicting inviter_id';
    END IF;
END
$$;

-- Every participant needs a native affiliate row. Existing rows are preserved.
INSERT INTO user_affiliates (user_id, aff_code, created_at, updated_at)
SELECT participant.user_id,
       CASE
           WHEN NOT EXISTS (
               SELECT 1 FROM user_affiliates existing
               WHERE existing.aff_code = 'LEGACY' || participant.user_id::TEXT
           ) THEN 'LEGACY' || participant.user_id::TEXT
           ELSE 'MIG' || SUBSTRING(MD5(participant.user_id::TEXT) FROM 1 FOR 29)
       END,
       NOW(),
       NOW()
FROM (
    SELECT customer_id AS user_id FROM legacy_agent_rows
    UNION
    SELECT agent_id AS user_id FROM legacy_agent_rows
) AS participant
WHERE NOT EXISTS (
    SELECT 1 FROM user_affiliates existing WHERE existing.user_id = participant.user_id
);

UPDATE user_affiliates AS invitee
SET inviter_id = row.agent_id,
    updated_at = NOW()
FROM legacy_agent_rows AS row
WHERE invitee.user_id = row.customer_id
  AND (invitee.inviter_id IS NULL OR invitee.inviter_id = row.agent_id);

UPDATE user_affiliates AS inviter
SET aff_count = counts.invitee_count,
    updated_at = NOW()
FROM (
    SELECT agent_id, COUNT(*)::INTEGER AS invitee_count
    FROM legacy_agent_rows
    GROUP BY agent_id
) AS counts
WHERE inviter.user_id = counts.agent_id;

-- Move only pending commission. transferred_amount was already credited historically.
UPDATE users AS account
SET balance = account.balance + totals.pending_commission,
    total_recharged = account.total_recharged + totals.pending_commission,
    updated_at = NOW()
FROM legacy_agent_totals AS totals
WHERE account.id = totals.agent_id
  AND totals.pending_commission <> 0;

INSERT INTO user_affiliate_ledger (
    user_id,
    action,
    amount,
    source_user_id,
    balance_after,
    aff_quota_after,
    aff_frozen_quota_after,
    aff_history_quota_after,
    created_at,
    updated_at
)
SELECT totals.agent_id,
       'transfer',
       totals.pending_commission,
       NULL,
       account.balance,
       affiliate.aff_quota,
       affiliate.aff_frozen_quota,
       affiliate.aff_history_quota,
       NOW(),
       NOW()
FROM legacy_agent_totals AS totals
JOIN users AS account ON account.id = totals.agent_id
JOIN user_affiliates AS affiliate ON affiliate.user_id = totals.agent_id
WHERE totals.pending_commission <> 0;

UPDATE agent_profiles AS profile
SET pending_commission = 0,
    updated_at = NOW()
FROM legacy_agent_totals AS totals
WHERE profile.user_id = totals.agent_id
  AND totals.pending_commission <> 0;

UPDATE users AS customer
SET agent_id = NULL,
    updated_at = NOW()
FROM legacy_agent_rows AS row
WHERE customer.id = row.customer_id;

INSERT INTO legacy_agent_affiliate_migrations (
    migration_key,
    relation_count,
    agent_count,
    credited_amount
)
SELECT 'legacy-agent-affiliates-v1',
       COUNT(*)::INTEGER,
       (SELECT COUNT(*)::INTEGER FROM legacy_agent_totals),
       COALESCE((SELECT SUM(pending_commission) FROM legacy_agent_totals), 0)
FROM legacy_agent_rows
HAVING COUNT(*) > 0;

COMMIT;
