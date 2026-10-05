DROP TRIGGER IF EXISTS trg_xw_resource_publications_publication_lock
    ON xiangwan_resource_publications;
DROP TRIGGER IF EXISTS trg_xw_resource_relations_publication_lock
    ON xiangwan_resource_relations;
DROP TRIGGER IF EXISTS trg_xw_people_profiles_publication_lock
    ON xiangwan_people_profiles;
DROP TRIGGER IF EXISTS trg_xw_people_bindings_publication_lock
    ON xiangwan_people_bindings;
DROP TRIGGER IF EXISTS trg_xw_instance_roles_publication_lock
    ON xiangwan_instance_role_bindings;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_fields_publication_lock
    ON xiangwan_questionnaire_fields;
DROP TRIGGER IF EXISTS trg_xw_questionnaire_versions_publication_lock
    ON xiangwan_questionnaire_versions;
DROP TRIGGER IF EXISTS trg_xw_questionnaires_publication_lock
    ON xiangwan_instance_questionnaires;
DROP TRIGGER IF EXISTS trg_xw_brand_publications_publication_lock
    ON xiangwan_brand_profile_publications;
DROP TRIGGER IF EXISTS trg_xw_brand_profiles_publication_lock
    ON xiangwan_brand_profiles;
DROP FUNCTION IF EXISTS xiangwan_lock_publication_reference_aggregate();

DROP TRIGGER IF EXISTS trg_xw_admin_audit_no_truncate
    ON xiangwan_admin_audit_events;
DROP TRIGGER IF EXISTS trg_xw_admin_audit_append_only
    ON xiangwan_admin_audit_events;
DROP TRIGGER IF EXISTS trg_xw_admin_operations_no_truncate
    ON xiangwan_admin_operations;
DROP TRIGGER IF EXISTS trg_xw_admin_operations_append_only
    ON xiangwan_admin_operations;
DROP TRIGGER IF EXISTS trg_xw_admin_grant_no_truncate
    ON xiangwan_admin_grants;
DROP TRIGGER IF EXISTS trg_xw_admin_grant_no_delete
    ON xiangwan_admin_grants;
DROP TRIGGER IF EXISTS trg_xw_admin_identity_link_no_truncate
    ON xiangwan_admin_identity_links;
DROP TRIGGER IF EXISTS trg_xw_admin_identity_link_no_delete
    ON xiangwan_admin_identity_links;
DROP TRIGGER IF EXISTS trg_xw_admin_grant_mutation
    ON xiangwan_admin_grants;
DROP TRIGGER IF EXISTS trg_xw_admin_session_mutation
    ON xiangwan_admin_sessions;
DROP TRIGGER IF EXISTS trg_xw_admin_identity_link_mutation
    ON xiangwan_admin_identity_links;

DROP FUNCTION IF EXISTS xiangwan_reject_admin_append_only_mutation();
DROP FUNCTION IF EXISTS xiangwan_reject_admin_authority_deletion();
DROP FUNCTION IF EXISTS xiangwan_guard_admin_grant_mutation();
DROP FUNCTION IF EXISTS xiangwan_guard_admin_session_mutation();
DROP FUNCTION IF EXISTS xiangwan_guard_admin_identity_link_mutation();

DROP TABLE IF EXISTS xiangwan_admin_audit_events;
DROP TABLE IF EXISTS xiangwan_admin_operations;
DROP TABLE IF EXISTS xiangwan_admin_grants;
DROP TABLE IF EXISTS xiangwan_admin_sessions;
DROP TABLE IF EXISTS xiangwan_admin_login_attempts;
DROP TABLE IF EXISTS xiangwan_admin_identity_links;
