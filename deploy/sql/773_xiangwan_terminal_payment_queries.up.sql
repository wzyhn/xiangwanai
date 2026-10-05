-- 773_xiangwan_terminal_payment_queries.up.sql
-- Finalize authoritative unpaid WeChat merchant-query outcomes. This is
-- intentionally roll-forward-only because deployed terminal lease evidence
-- cannot be represented by the preceding four-state constraint without loss.

ALTER TABLE xiangwan_payment_query_leases
    DROP CONSTRAINT IF EXISTS xiangwan_payment_query_leases_status_check,
    ADD CONSTRAINT xiangwan_payment_query_leases_status_check
        CHECK (
            query_status IN (
                'in_progress', 'pending', 'unknown', 'converged', 'closed'
            )
        ),
    DROP CONSTRAINT IF EXISTS xiangwan_payment_query_leases_state_shape_check,
    ADD CONSTRAINT xiangwan_payment_query_leases_state_shape_check
        CHECK (
            (
                query_status = 'in_progress'
                AND owner_token IS NOT NULL
                AND lease_expires_at IS NOT NULL
                AND next_query_at IS NULL
                AND last_trade_state IS NULL
                AND last_error_class IS NULL
                AND last_provider_request_id IS NULL
                AND completed_at IS NULL
            )
            OR (
                query_status = 'pending'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NOT NULL
                AND last_trade_state IN (
                    'NOTPAY', 'USERPAYING', 'CLOSED', 'REVOKED', 'PAYERROR', 'REFUND'
                )
                AND last_error_class IS NULL
                AND completed_at IS NULL
            )
            OR (
                query_status = 'unknown'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NOT NULL
                AND last_trade_state IS NULL
                AND last_error_class ~ '^[a-z][a-z0-9_]{0,63}$'
                AND completed_at IS NULL
            )
            OR (
                query_status = 'converged'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NULL
                AND last_trade_state = 'SUCCESS'
                AND last_error_class IS NULL
                AND completed_at IS NOT NULL
            )
            OR (
                query_status = 'closed'
                AND owner_token IS NULL
                AND lease_expires_at IS NULL
                AND next_query_at IS NULL
                AND last_trade_state IN ('CLOSED', 'REVOKED', 'PAYERROR')
                AND last_error_class IS NULL
                AND completed_at IS NOT NULL
            )
        );

ALTER TABLE xiangwan_payment_transaction_observations
    DROP CONSTRAINT IF EXISTS
        xiangwan_payment_transaction_observations_payment_shape_check,
    ADD CONSTRAINT xiangwan_payment_transaction_observations_payment_shape_check
        CHECK (
            (
                trade_state = 'SUCCESS'
                AND BTRIM(COALESCE(transaction_id, '')) <> ''
                AND success_at IS NOT NULL
            )
            OR (
                trade_state IN ('CLOSED', 'REVOKED', 'PAYERROR')
                AND transaction_id IS NULL
                AND success_at IS NULL
            )
            OR (
                trade_state IN ('REFUND', 'NOTPAY', 'USERPAYING')
                AND (transaction_id IS NULL) = (success_at IS NULL)
            )
        );

CREATE OR REPLACE FUNCTION xiangwan_guard_payment_query_lease_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.order_id IS DISTINCT FROM OLD.order_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.payment_app_id IS DISTINCT FROM OLD.payment_app_id
        OR NEW.payment_merchant_id IS DISTINCT FROM OLD.payment_merchant_id
        OR NEW.out_trade_no IS DISTINCT FROM OLD.out_trade_no
        OR NEW.amount_cents IS DISTINCT FROM OLD.amount_cents
        OR NEW.currency IS DISTINCT FROM OLD.currency
        OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'xiangwan payment query identity is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF OLD.query_status IN ('converged', 'closed') THEN
        RAISE EXCEPTION 'xiangwan terminal payment query is immutable'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.version <> OLD.version + 1 OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'xiangwan payment query version/time must advance'
            USING ERRCODE = '23514';
    END IF;
    IF NEW.query_status = 'in_progress' AND (
        (OLD.query_status = 'in_progress' AND NEW.updated_at < OLD.lease_expires_at)
        OR (
            OLD.query_status IN ('pending', 'unknown')
            AND NEW.updated_at < OLD.next_query_at
        )
    ) THEN
        RAISE EXCEPTION 'xiangwan payment query lease is not due'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
