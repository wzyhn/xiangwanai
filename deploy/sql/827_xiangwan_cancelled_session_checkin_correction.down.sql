-- Local round-trip only: restore migration 747 cancellation immutability guard.
CREATE OR REPLACE FUNCTION xiangwan_enforce_session_cancellation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF OLD.status = 'cancelled' AND NEW IS DISTINCT FROM OLD THEN
        RAISE EXCEPTION 'xiangwan cancelled Session is immutable'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_session_cancellation_irreversible';
    END IF;

    IF NEW.status = 'cancelled' THEN
        IF OLD.status <> 'published'
            OR NEW.confirmed_registration_count <> 0
            OR NEW.active_hold_count <> 0
            OR NOT EXISTS (
                SELECT 1
                  FROM xiangwan_session_cancellation_receipts AS receipt
                 WHERE receipt.tenant_id = NEW.tenant_id
                   AND receipt.session_id = NEW.id
                   AND receipt.resulting_session_version = NEW.version
                   AND receipt.cancelled_at = NEW.updated_at
            )
            OR EXISTS (
                SELECT 1
                  FROM xiangwan_registrations AS registration
                 WHERE registration.tenant_id = NEW.tenant_id
                   AND registration.session_id = NEW.id
                   AND registration.participation_status IN (
                       'pending_payment', 'confirmed'
                   )
            )
            OR EXISTS (
                SELECT 1
                  FROM xiangwan_capacity_holds AS capacity_hold
                 WHERE capacity_hold.tenant_id = NEW.tenant_id
                   AND capacity_hold.session_id = NEW.id
                   AND capacity_hold.hold_status = 'active'
            ) THEN
            RAISE EXCEPTION
                'xiangwan cancelled Session has unconverged participation'
                USING ERRCODE = '23514',
                      CONSTRAINT = 'xiangwan_session_cancellation_incomplete';
        END IF;
    END IF;
    RETURN NULL;
END;
$$;
