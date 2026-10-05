-- 772_xiangwan_wechat_payment_queries.down.sql
-- Roll back schema only. Immutable payment observations are intentionally lost
-- only when an operator explicitly chooses this migration rollback.

DROP TRIGGER IF EXISTS trg_xiangwan_payment_transaction_observation_no_truncate
    ON xiangwan_payment_transaction_observations;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_transaction_observation_immutable
    ON xiangwan_payment_transaction_observations;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_no_truncate
    ON xiangwan_payment_query_leases;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_no_delete
    ON xiangwan_payment_query_leases;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_guard_mutation
    ON xiangwan_payment_query_leases;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_eligibility_acquire
    ON xiangwan_payment_query_leases;
DROP TRIGGER IF EXISTS trg_xiangwan_payment_query_eligibility_insert
    ON xiangwan_payment_query_leases;

DROP FUNCTION IF EXISTS xiangwan_guard_payment_query_lease_mutation();
DROP FUNCTION IF EXISTS xiangwan_require_eligible_payment_query();

DROP TABLE IF EXISTS xiangwan_payment_transaction_observations;
DROP TABLE IF EXISTS xiangwan_payment_query_leases;
