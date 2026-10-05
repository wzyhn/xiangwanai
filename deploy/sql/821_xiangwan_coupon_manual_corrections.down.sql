DROP TRIGGER IF EXISTS trg_xw_coupon_correction_event_guard
    ON xiangwan_coupon_correction_events;
DROP TRIGGER IF EXISTS trg_xw_coupon_correction_event_no_truncate
    ON xiangwan_coupon_correction_events;
DROP FUNCTION IF EXISTS xiangwan_guard_coupon_correction_event();
DROP TABLE IF EXISTS xiangwan_coupon_correction_events;
