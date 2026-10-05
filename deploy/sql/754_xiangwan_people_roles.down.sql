-- 754_xiangwan_people_roles.down.sql

DROP TABLE IF EXISTS xiangwan_instance_role_bindings;
DROP TABLE IF EXISTS xiangwan_people_bindings;
DROP TABLE IF EXISTS xiangwan_people_profiles;

DROP FUNCTION IF EXISTS xiangwan_guard_people_history();
DROP FUNCTION IF EXISTS xiangwan_guard_people_profile();
DROP FUNCTION IF EXISTS xiangwan_reject_people_history_removal();
