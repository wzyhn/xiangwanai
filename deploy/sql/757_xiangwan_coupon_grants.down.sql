-- 757_xiangwan_coupon_grants.down.sql

DROP TRIGGER IF EXISTS trg_xw_coupon_entries_no_truncate
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_coupon_entries_no_delete
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_coupon_entries_no_update
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_coupon_entries_validate
    ON xiangwan_coupon_entries;
DROP TRIGGER IF EXISTS trg_xw_coupons_no_truncate ON xiangwan_coupons;
DROP TRIGGER IF EXISTS trg_xw_coupons_no_delete ON xiangwan_coupons;
DROP TRIGGER IF EXISTS trg_xw_coupons_no_update ON xiangwan_coupons;
DROP TRIGGER IF EXISTS trg_xw_coupons_validate ON xiangwan_coupons;

DROP FUNCTION IF EXISTS xiangwan_reject_coupon_mutation();
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_entry();
DROP FUNCTION IF EXISTS xiangwan_validate_coupon_grant();

DROP TABLE IF EXISTS xiangwan_coupon_entries;
DROP TABLE IF EXISTS xiangwan_coupons;
