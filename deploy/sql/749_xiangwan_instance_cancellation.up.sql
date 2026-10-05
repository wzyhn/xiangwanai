-- 749_xiangwan_instance_cancellation.up.sql
-- Preview and cancel a complete published Instance in one PostgreSQL
-- transaction, leaving no partially participable Session.

CREATE TABLE IF NOT EXISTS xiangwan_instance_cancellation_previews (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    requested_by UUID NOT NULL REFERENCES principals(id),
    idempotency_key VARCHAR(128) NOT NULL,
    snapshot_digest CHAR(64) NOT NULL,
    expected_instance_version BIGINT NOT NULL,
    session_count INTEGER NOT NULL,
    target_session_count INTEGER NOT NULL,
    already_cancelled_session_count INTEGER NOT NULL,
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
    cancellation_reason VARCHAR(500) NOT NULL,
    notification_strategy VARCHAR(32) NOT NULL,
    session_impacts JSONB NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_instance_cancellation_previews_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND snapshot_digest ~ '^[0-9a-f]{64}$'
            AND BTRIM(cancellation_reason) <> ''
            AND notification_strategy = 'manual_required'
        ),
    CONSTRAINT xiangwan_instance_cancellation_previews_counts_check
        CHECK (
            expected_instance_version >= 1
            AND session_count >= 1
            AND target_session_count >= 1
            AND already_cancelled_session_count >= 0
            AND session_count
                = target_session_count + already_cancelled_session_count
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
    CONSTRAINT xiangwan_instance_cancellation_previews_impacts_check
        CHECK (
            jsonb_typeof(session_impacts) = 'array'
            AND jsonb_array_length(session_impacts) = session_count
        ),
    CONSTRAINT xiangwan_instance_cancellation_previews_lifetime_check
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
    CONSTRAINT xiangwan_instance_cancellation_previews_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xiangwan_instance_cancellation_previews_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_instance_cancellation_previews_hierarchy_id_key
        UNIQUE (tenant_id, series_id, instance_id, id),
    CONSTRAINT xiangwan_instance_cancellation_previews_tenant_idempotency_key
        UNIQUE (tenant_id, idempotency_key)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_instance_cancellation_previews_expiry
    ON xiangwan_instance_cancellation_previews (expires_at, tenant_id, id)
    WHERE consumed_at IS NULL;

CREATE OR REPLACE FUNCTION xiangwan_guard_instance_cancellation_preview_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
        OR NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.series_id IS DISTINCT FROM OLD.series_id
        OR NEW.instance_id IS DISTINCT FROM OLD.instance_id
        OR NEW.requested_by IS DISTINCT FROM OLD.requested_by
        OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
        OR NEW.snapshot_digest IS DISTINCT FROM OLD.snapshot_digest
        OR NEW.expected_instance_version
            IS DISTINCT FROM OLD.expected_instance_version
        OR NEW.session_count IS DISTINCT FROM OLD.session_count
        OR NEW.target_session_count IS DISTINCT FROM OLD.target_session_count
        OR NEW.already_cancelled_session_count
            IS DISTINCT FROM OLD.already_cancelled_session_count
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
        OR NEW.cancellation_reason IS DISTINCT FROM OLD.cancellation_reason
        OR NEW.notification_strategy IS DISTINCT FROM OLD.notification_strategy
        OR NEW.session_impacts IS DISTINCT FROM OLD.session_impacts
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR OLD.consumed_at IS NOT NULL
        OR NEW.consumed_at IS NULL THEN
        RAISE EXCEPTION
            'xiangwan Instance cancellation preview is immutable or consumed'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_instance_cancellation_preview_update';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_previews_update_guard
    ON xiangwan_instance_cancellation_previews;
CREATE TRIGGER trg_xiangwan_instance_cancellation_previews_update_guard
    BEFORE UPDATE ON xiangwan_instance_cancellation_previews
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_guard_instance_cancellation_preview_update();

ALTER TABLE xiangwan_session_cancellation_previews
    ADD COLUMN IF NOT EXISTS parent_instance_preview_id UUID;

ALTER TABLE xiangwan_session_cancellation_previews
    DROP CONSTRAINT IF EXISTS xiangwan_session_cancellation_previews_parent_fkey,
    ADD CONSTRAINT xiangwan_session_cancellation_previews_parent_fkey
        FOREIGN KEY (
            tenant_id, series_id, instance_id, parent_instance_preview_id
        )
        REFERENCES xiangwan_instance_cancellation_previews (
            tenant_id, series_id, instance_id, id
        );

CREATE OR REPLACE FUNCTION xiangwan_guard_session_preview_parent_update()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.parent_instance_preview_id
        IS DISTINCT FROM OLD.parent_instance_preview_id THEN
        RAISE EXCEPTION
            'xiangwan Session cancellation preview parent is immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_session_cancellation_preview_parent';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_session_cancellation_previews_parent_guard
    ON xiangwan_session_cancellation_previews;
CREATE TRIGGER trg_xiangwan_session_cancellation_previews_parent_guard
    BEFORE UPDATE ON xiangwan_session_cancellation_previews
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_guard_session_preview_parent_update();

CREATE TABLE IF NOT EXISTS xiangwan_instance_cancellation_receipts (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    series_id UUID NOT NULL,
    instance_id UUID NOT NULL,
    preview_id UUID NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    cancelled_by UUID NOT NULL REFERENCES principals(id),
    cancellation_reason VARCHAR(500) NOT NULL,
    notification_strategy VARCHAR(32) NOT NULL,
    session_count INTEGER NOT NULL,
    newly_cancelled_session_count INTEGER NOT NULL,
    already_cancelled_session_count INTEGER NOT NULL,
    cancelled_registration_count INTEGER NOT NULL,
    released_confirmed_count INTEGER NOT NULL,
    released_hold_count INTEGER NOT NULL,
    closed_pending_order_count INTEGER NOT NULL,
    refund_case_count INTEGER NOT NULL,
    requested_refund_cents BIGINT NOT NULL,
    cancelled_at TIMESTAMPTZ NOT NULL,
    resulting_instance_version BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT xiangwan_instance_cancellation_receipts_text_check
        CHECK (
            idempotency_key ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
            AND BTRIM(cancellation_reason) <> ''
            AND notification_strategy = 'manual_required'
        ),
    CONSTRAINT xiangwan_instance_cancellation_receipts_counts_check
        CHECK (
            session_count >= 1
            AND newly_cancelled_session_count >= 1
            AND already_cancelled_session_count >= 0
            AND session_count
                = newly_cancelled_session_count
                    + already_cancelled_session_count
            AND cancelled_registration_count >= 0
            AND released_confirmed_count >= 0
            AND released_hold_count >= 0
            AND closed_pending_order_count >= 0
            AND refund_case_count >= 0
            AND requested_refund_cents >= 0
            AND cancelled_registration_count
                = released_confirmed_count + released_hold_count
            AND closed_pending_order_count <= released_hold_count
            AND refund_case_count <= released_confirmed_count
            AND (
                (refund_case_count = 0 AND requested_refund_cents = 0)
                OR (refund_case_count > 0 AND requested_refund_cents > 0)
            )
        ),
    CONSTRAINT xiangwan_instance_cancellation_receipts_time_check
        CHECK (created_at >= cancelled_at),
    CONSTRAINT xiangwan_instance_cancellation_receipts_version_check
        CHECK (resulting_instance_version >= 2),
    CONSTRAINT xiangwan_instance_cancellation_receipts_instance_fkey
        FOREIGN KEY (tenant_id, series_id, instance_id)
        REFERENCES xiangwan_activity_instances (tenant_id, series_id, id),
    CONSTRAINT xiangwan_instance_cancellation_receipts_preview_fkey
        FOREIGN KEY (tenant_id, preview_id)
        REFERENCES xiangwan_instance_cancellation_previews (tenant_id, id),
    CONSTRAINT xiangwan_instance_cancellation_receipts_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_instance_cancellation_receipts_tenant_instance_key
        UNIQUE (tenant_id, instance_id),
    CONSTRAINT xiangwan_instance_cancellation_receipts_tenant_preview_key
        UNIQUE (tenant_id, preview_id),
    CONSTRAINT xiangwan_instance_cancellation_receipts_tenant_idempotency_key
        UNIQUE (tenant_id, idempotency_key)
);

CREATE OR REPLACE FUNCTION xiangwan_validate_instance_cancellation_receipt()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    current_instance xiangwan_activity_instances%ROWTYPE;
BEGIN
    SELECT *
      INTO current_instance
      FROM xiangwan_activity_instances
     WHERE tenant_id = NEW.tenant_id
       AND series_id = NEW.series_id
       AND id = NEW.instance_id;

    IF NOT FOUND
        OR current_instance.status <> 'cancelled'
        OR current_instance.version <> NEW.resulting_instance_version
        OR current_instance.updated_at IS DISTINCT FROM NEW.cancelled_at
        OR NOT EXISTS (
            SELECT 1
              FROM xiangwan_instance_cancellation_previews AS preview
             WHERE preview.tenant_id = NEW.tenant_id
               AND preview.id = NEW.preview_id
               AND preview.series_id = NEW.series_id
               AND preview.instance_id = NEW.instance_id
               AND preview.requested_by = NEW.cancelled_by
               AND preview.expected_instance_version + 1
                   = NEW.resulting_instance_version
               AND preview.session_count = NEW.session_count
               AND preview.target_session_count
                   = NEW.newly_cancelled_session_count
               AND preview.already_cancelled_session_count
                   = NEW.already_cancelled_session_count
               AND preview.cancelled_registration_count
                   = NEW.cancelled_registration_count
               AND preview.confirmed_registration_count
                   = NEW.released_confirmed_count
               AND preview.active_hold_count = NEW.released_hold_count
               AND preview.pending_order_count
                   = NEW.closed_pending_order_count
               AND preview.refund_case_count = NEW.refund_case_count
                AND preview.requested_refund_cents
                    = NEW.requested_refund_cents
                AND preview.cancellation_reason = NEW.cancellation_reason
                AND preview.notification_strategy
                   = NEW.notification_strategy
               AND preview.consumed_at = NEW.cancelled_at
        )
        OR EXISTS (
            SELECT 1
              FROM xiangwan_activity_sessions AS activity_session
             WHERE activity_session.tenant_id = NEW.tenant_id
               AND activity_session.instance_id = NEW.instance_id
               AND activity_session.status <> 'cancelled'
        )
        OR NOT EXISTS (
            SELECT 1
              FROM (
                    SELECT
                        COUNT(*) AS session_count,
                        COALESCE(SUM(
                            receipt.cancelled_registration_count
                        ), 0) AS cancelled_registration_count,
                        COALESCE(SUM(
                            receipt.released_confirmed_count
                        ), 0) AS released_confirmed_count,
                        COALESCE(SUM(
                            receipt.released_hold_count
                        ), 0) AS released_hold_count,
                        COALESCE(SUM(
                            receipt.closed_pending_order_count
                        ), 0) AS closed_pending_order_count,
                        COALESCE(SUM(
                            receipt.refund_case_count
                        ), 0) AS refund_case_count,
                        COALESCE(SUM(
                            receipt.requested_refund_cents
                        ), 0) AS requested_refund_cents
                      FROM xiangwan_session_cancellation_receipts AS receipt
                      JOIN xiangwan_session_cancellation_previews AS child_preview
                        ON child_preview.tenant_id = receipt.tenant_id
                       AND child_preview.id = receipt.preview_id
                     WHERE receipt.tenant_id = NEW.tenant_id
                       AND receipt.series_id = NEW.series_id
                       AND receipt.instance_id = NEW.instance_id
                       AND receipt.cancelled_by = NEW.cancelled_by
                       AND receipt.cancellation_reason = NEW.cancellation_reason
                       AND receipt.notification_strategy
                           = NEW.notification_strategy
                       AND receipt.cancelled_at = NEW.cancelled_at
                       AND child_preview.parent_instance_preview_id
                           = NEW.preview_id
              ) AS child_totals
             WHERE child_totals.session_count
                       = NEW.newly_cancelled_session_count
               AND child_totals.cancelled_registration_count
                       = NEW.cancelled_registration_count
               AND child_totals.released_confirmed_count
                       = NEW.released_confirmed_count
               AND child_totals.released_hold_count = NEW.released_hold_count
               AND child_totals.closed_pending_order_count
                       = NEW.closed_pending_order_count
               AND child_totals.refund_case_count = NEW.refund_case_count
               AND child_totals.requested_refund_cents
                       = NEW.requested_refund_cents
        )
        OR EXISTS (
            SELECT 1
              FROM xiangwan_registrations AS registration
             WHERE registration.tenant_id = NEW.tenant_id
               AND registration.instance_id = NEW.instance_id
               AND registration.participation_status IN (
                   'pending_payment', 'confirmed'
               )
        )
        OR EXISTS (
            SELECT 1
              FROM xiangwan_capacity_holds AS capacity_hold
             WHERE capacity_hold.tenant_id = NEW.tenant_id
               AND EXISTS (
                   SELECT 1
                     FROM xiangwan_activity_sessions AS activity_session
                    WHERE activity_session.tenant_id
                        = capacity_hold.tenant_id
                      AND activity_session.instance_id = NEW.instance_id
                      AND activity_session.id = capacity_hold.session_id
               )
               AND capacity_hold.hold_status = 'active'
        ) THEN
        RAISE EXCEPTION
            'xiangwan Instance cancellation receipt does not match terminal state'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_instance_cancellation_receipt_state';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_receipts_validate
    ON xiangwan_instance_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_instance_cancellation_receipts_validate
    BEFORE INSERT ON xiangwan_instance_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_validate_instance_cancellation_receipt();

CREATE OR REPLACE FUNCTION xiangwan_reject_instance_cancellation_receipt_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan Instance cancellation receipts are immutable'
        USING ERRCODE = '23514',
              CONSTRAINT = 'xiangwan_instance_cancellation_receipt_immutable';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_receipts_no_update_delete
    ON xiangwan_instance_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_instance_cancellation_receipts_no_update_delete
    BEFORE UPDATE OR DELETE ON xiangwan_instance_cancellation_receipts
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_instance_cancellation_receipt_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_instance_cancellation_receipts_no_truncate
    ON xiangwan_instance_cancellation_receipts;
CREATE TRIGGER trg_xiangwan_instance_cancellation_receipts_no_truncate
    BEFORE TRUNCATE ON xiangwan_instance_cancellation_receipts
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_instance_cancellation_receipt_mutation();

CREATE OR REPLACE FUNCTION xiangwan_enforce_instance_cancellation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'cancelled' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'xiangwan cancelled Instance is immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_instance_cancellation_irreversible';
    END IF;

    IF NEW.status = 'cancelled' AND (
        OLD.status <> 'published'
        OR NOT EXISTS (
            SELECT 1
              FROM xiangwan_instance_cancellation_receipts AS receipt
             WHERE receipt.tenant_id = NEW.tenant_id
               AND receipt.instance_id = NEW.id
               AND receipt.resulting_instance_version = NEW.version
               AND receipt.cancelled_at = NEW.updated_at
        )
        OR EXISTS (
            SELECT 1
              FROM xiangwan_activity_sessions AS activity_session
             WHERE activity_session.tenant_id = NEW.tenant_id
               AND activity_session.instance_id = NEW.id
               AND activity_session.status <> 'cancelled'
        )
    ) THEN
        RAISE EXCEPTION
            'xiangwan cancelled Instance has unconverged Sessions'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_instance_cancellation_incomplete';
    END IF;
    RETURN NULL;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_activity_instances_cancellation_guard
    ON xiangwan_activity_instances;
CREATE CONSTRAINT TRIGGER trg_xiangwan_activity_instances_cancellation_guard
    AFTER UPDATE ON xiangwan_activity_instances
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_enforce_instance_cancellation();
