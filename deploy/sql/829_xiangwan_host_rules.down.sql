-- Local verification only; production changes roll forward.
DROP TABLE IF EXISTS xiangwan_host_application_consents;
DROP TABLE IF EXISTS xiangwan_host_rule_versions;
DROP FUNCTION IF EXISTS xiangwan_reject_host_policy_mutation();
