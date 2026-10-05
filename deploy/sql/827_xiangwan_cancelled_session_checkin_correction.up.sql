-- Roll-forward: permit only proven attendance reversal after Session cancellation.
CREATE OR REPLACE FUNCTION xiangwan_enforce_session_cancellation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    -- A terminal cancellation cannot change activity or financial facts.
    -- Attendance alone may decrease after an immutable revocation and outbox
    -- have already been written in the same transaction.
    IF OLD.status = 'cancelled' AND NEW.status = 'cancelled'
       AND NEW.checked_in_registration_count = OLD.checked_in_registration_count - 1
       AND NEW.checked_in_registration_count >= 0
       AND NEW.version = OLD.version + 1
       AND NEW.updated_at >= OLD.updated_at
       AND (to_jsonb(NEW) - ARRAY['checked_in_registration_count','version','updated_at'])
           = (to_jsonb(OLD) - ARRAY['checked_in_registration_count','version','updated_at'])
       AND NEW.checked_in_registration_count = (
           SELECT COUNT(*) FROM xiangwan_checkins AS attendance
           WHERE attendance.tenant_id = NEW.tenant_id
             AND attendance.session_id = NEW.id
             AND attendance.checkin_status = 'checked_in'
       )
       AND EXISTS (
           SELECT 1 FROM xiangwan_checkins AS attendance
           JOIN xiangwan_checkin_events AS event
             ON event.tenant_id = attendance.tenant_id
            AND event.checkin_id = attendance.id
            AND event.event_type = 'revoked'
            AND event.resulting_checkin_version = attendance.version
            AND event.occurred_at = attendance.revoked_at
           JOIN xiangwan_checkin_correction_outbox AS correction
             ON correction.tenant_id = attendance.tenant_id
            AND correction.checkin_id = attendance.id
            AND correction.checkin_event_id = event.id
           WHERE attendance.tenant_id = NEW.tenant_id
             AND attendance.session_id = NEW.id
             AND attendance.instance_id = NEW.instance_id
             AND attendance.checkin_status = 'revoked'
             AND NEW.updated_at = GREATEST(OLD.updated_at, attendance.revoked_at)
       ) THEN
        RETURN NULL;
    END IF;

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
