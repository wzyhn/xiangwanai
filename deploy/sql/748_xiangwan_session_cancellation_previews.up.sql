-- 748_xiangwan_session_cancellation_previews.up.sql
-- Short-lived, PostgreSQL-backed impact snapshots fence Session cancellation
-- commits against changed participation or financial facts.

CREATE TABLE IF NOT EXISTS xiangwan_session_cancellation_previews (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    session_id UUID NOT NULL,
    requested_by UUID NOT NULL REFERENCES principals(id),
    idempotency_key VARCHAR(128) NOT NULL,
    snapshot_digest CHAR(64) NOT NULL,
    expected_session_version BIGINT NOT NULL,
    cancelled_registration_count INTEGER NOT NULL,
    confirmed_registration_count INTEGER NOT NULL,
    active_hold_count INTEGER NOT NULL,
    free_registration_count INTEGER NOT NULL,
    paid_refund_registration_count INTEGER NOT NULL,
    pending_order_count INTEGER NOT NULL,
    unknown_payment_count INTEGER NOT NULL,
    refund_case_count INTEGER NOT NULL,
    requested_refund_cents BIGINT NOT NULL,
    coupon_adjustment_count INTEGER NOT NULL DEFAULT 0,
    notification_strategy VARCHAR(32) NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_session_cancellation_previews_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND snapshot_digest ~ '^[0-9a-f]{64}$'
            AND notification_strategy = 'manual_required'
        ),
    CONSTRAINT xiangwan_session_cancellation_previews_counts_check
        CHECK (
            expected_session_version >= 1
            AND cancelled_registration_count >= 0
            AND confirmed_registration_count >= 0
            AND active_hold_count >= 0
            AND free_registration_count >= 0
            AND paid_refund_registration_count >= 0
            AND pending_order_count >= 0
            AND unknown_payment_count >= 0
            AND refund_case_count >= 0
            AND requested_refund_cents >= 0
            AND coupon_adjustment_count >= 0
            AND cancelled_registration_count
                = confirmed_registration_count + active_hold_count
            AND confirmed_registration_count
                = free_registration_count + paid_refund_registration_count
            AND active_hold_count
                = pending_order_count + unknown_payment_count
            AND refund_case_count = paid_refund_registration_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        ),
    CONSTRAINT xiangwan_session_cancellation_previews_lifetime_check
        CHECK (
            expires_at > created_at
            AND expires_at <= created_at + INTERVAL '15 minutes'
            AND (
                consumed_at IS NULL
                OR (
                    consumed_at >= created_at
                    AND consumed_at <= expires_at
                )
            )
        ),
    CONSTRAINT xiangwan_session_cancellation_previews_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xiangwan_session_cancellation_previews_session_fkey
        FOREIGN KEY (tenant_id, instance_id, session_id)
        REFERENCES xiangwan_activity_sessions (tenant_id, instance_id, id),
    CONSTRAINT xiangwan_session_cancellation_previews_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_session_cancellation_previews_tenant_idempotency_key
        UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_session_cancellation_previews_expiry
    ON xiangwan_session_cancellation_previews (expires_at, tenant_id, id)
    WHERE consumed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_xiangwan_session_cancellation_previews_session
    ON xiangwan_session_cancellation_previews (
        tenant_id, session_id, created_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_guard_session_cancellation_preview_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.session_id IS DISTINCT FROM OLD.session_id
        OR NEW.requested_by IS DISTINCT FROM OLD.requested_by
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.snapshot_digest IS DISTINCT FROM OLD.snapshot_digest
        OR NEW.expected_session_version IS DISTINCT FROM OLD.expected_session_version
        OR NEW.cancelled_registration_count
            IS DISTINCT FROM OLD.cancelled_registration_count
        OR NEW.confirmed_registration_count
            IS DISTINCT FROM OLD.confirmed_registration_count
        OR NEW.active_hold_count IS DISTINCT FROM OLD.active_hold_count
        OR NEW.free_registration_count
            IS DISTINCT FROM OLD.free_registration_count
        OR NEW.paid_refund_registration_count
            IS DISTINCT FROM OLD.paid_refund_registration_count
        OR NEW.pending_order_count IS DISTINCT FROM OLD.pending_order_count
        OR NEW.unknown_payment_count IS DISTINCT FROM OLD.unknown_payment_count
        OR NEW.refund_case_count IS DISTINCT FROM OLD.refund_case_count
        OR NEW.requested_refund_cents IS DISTINCT FROM OLD.requested_refund_cents
        OR NEW.coupon_adjustment_count
            IS DISTINCT FROM OLD.coupon_adjustment_count
        OR NEW.notification_strategy IS DISTINCT FROM OLD.notification_strategy
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.consumed_at IS NOT NULL
        OR NEW.consumed_at IS NULL THEN
        RAISE EXCEPTION
            'xiangwan Session cancellation preview is immutable or consumed'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_session_cancellation_preview_update';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_previews_update_guard
    ON xiangwan_session_cancellation_previews;
CREATE TRIGGER trg_xiangwan_session_cancellation_previews_update_guard
    BEFORE UPDATE ON xiangwan_session_cancellation_previews
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_guard_session_cancellation_preview_update();

ALTER TABLE xiangwan_session_cancellation_receipts
    ADD COLUMN IF NOT EXISTS preview_id UUID,
    ADD COLUMN IF NOT EXISTS notification_strategy VARCHAR(32)
        NOT NULL DEFAULT 'manual_required';

ALTER TABLE xiangwan_session_cancellation_receipts
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_preview_fkey,
    ADD CONSTRAINT xiangwan_session_cancellation_receipts_preview_fkey
        FOREIGN KEY (tenant_id, preview_id)
        REFERENCES xiangwan_session_cancellation_previews (tenant_id, id),
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_preview_key,
    ADD CONSTRAINT xiangwan_session_cancellation_receipts_preview_key
        UNIQUE (tenant_id, preview_id),
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_preview_required,
    ADD CONSTRAINT xiangwan_session_cancellation_receipts_preview_required
        CHECK (preview_id IS NOT NULL) NOT VALID,
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_receipts_notification_check,
    ADD CONSTRAINT xiangwan_session_cancellation_receipts_notification_check
        CHECK (notification_strategy = 'manual_required');

CREATE OR REPLACE FUNCTION xiangwan_validate_session_cancellation_preview_receipt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM xiangwan_session_cancellation_previews AS preview
         WHERE preview.tenant_id = NEW.tenant_id
           AND preview.id = NEW.preview_id
           AND preview.series_id = NEW.series_id
           AND preview.instance_id = NEW.instance_id
           AND preview.session_id = NEW.session_id
           AND preview.requested_by = NEW.cancelled_by
           AND preview.expected_session_version + 1
               = NEW.resulting_session_version
           AND preview.cancelled_registration_count
               = NEW.cancelled_registration_count
           AND preview.confirmed_registration_count
               = NEW.released_confirmed_count
           AND preview.active_hold_count = NEW.released_hold_count
           AND preview.pending_order_count
               = NEW.closed_pending_order_count
           AND preview.refund_case_count = NEW.refund_case_count
           AND preview.requested_refund_cents = NEW.requested_refund_cents
           AND preview.notification_strategy = NEW.notification_strategy
           AND preview.consumed_at = NEW.cancelled_at
    ) THEN
        RAISE EXCEPTION
            'xiangwan Session cancellation receipt lacks consumed preview'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_session_cancellation_receipt_preview';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_receipts_preview
    ON xiangwan_session_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_session_cancellation_receipts_preview
    BEFORE INSERT ON xiangwan_session_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_session_cancellation_preview_receipt();
