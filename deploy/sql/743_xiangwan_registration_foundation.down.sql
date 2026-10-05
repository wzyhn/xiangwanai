-- Development/test rollback reference only. Production rollback is roll-forward.

DROP TRIGGER IF EXISTS trg_xiangwan_registrations_no_truncate
    ON xiangwan_registrations;
DROP TRIGGER IF EXISTS trg_xiangwan_registrations_no_delete
    ON xiangwan_registrations;
DROP TRIGGER IF EXISTS trg_xiangwan_registrations_guard_mutation
    ON xiangwan_registrations;

DROP TABLE IF EXISTS xiangwan_registrations;

DROP FUNCTION IF EXISTS xiangwan_reject_registration_removal();
DROP FUNCTION IF EXISTS xiangwan_guard_registration_mutation();

ALTER TABLE IF EXISTS xiangwan_activity_sessions
    DROP CONSTRAINT IF EXISTS xiangwan_activity_sessions_tenant_instance_id_key;
