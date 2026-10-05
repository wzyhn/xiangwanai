-- 755_xiangwan_host_applications.down.sql

DROP TABLE IF EXISTS xiangwan_host_applications;

DROP FUNCTION IF EXISTS xiangwan_guard_host_application();
DROP FUNCTION IF EXISTS xiangwan_reject_host_application_removal();
