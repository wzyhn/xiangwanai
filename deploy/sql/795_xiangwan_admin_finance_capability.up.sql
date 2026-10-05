-- 795_xiangwan_admin_finance_capability.up.sql
-- Finance is a distinct, tenant-scoped Grant for RefundCase operations. It
-- must not be inherited from activity_operator or onsite_checkin.
ALTER TABLE xiangwan_admin_grants
    DROP CONSTRAINT IF EXISTS xw_admin_grant_capability_check,
    ADD CONSTRAINT xw_admin_grant_capability_check CHECK (
        capability IN ('super_admin', 'activity_operator', 'onsite_checkin', 'finance')
    );
