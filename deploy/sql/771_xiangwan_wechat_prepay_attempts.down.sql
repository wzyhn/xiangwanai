-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_payment_observation_no_truncate
    ON xiangwan_payment_observations;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_observation_immutable
    ON xiangwan_payment_observations;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_attempt_no_truncate
    ON xiangwan_payment_attempts;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_attempt_no_delete
    ON xiangwan_payment_attempts;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_attempt_guard_mutation
    ON xiangwan_payment_attempts;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_attempt_eligibility_takeover
    ON xiangwan_payment_attempts;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_attempt_eligibility_insert
    ON xiangwan_payment_attempts;

DROP TABLE IF EXISTS xiangwan_payment_observations;
DROP TABLE IF EXISTS xiangwan_payment_attempts;

DROP FUNCTION IF EXISTS xiangwan_reject_payment_evidence_mutation();
DROP FUNCTION IF EXISTS xiangwan_guard_payment_attempt_mutation();
DROP FUNCTION IF EXISTS xiangwan_require_eligible_prepay_attempt();

ALTER TABLE IF EXISTS xiangwan_orders
    DROP CONSTRAINT IF EXISTS xiangwan_orders_prepay_identity_key;
