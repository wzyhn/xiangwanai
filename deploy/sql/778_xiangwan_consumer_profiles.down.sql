-- 778_xiangwan_consumer_profiles.down.sql

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profiles_no_delete
    ON xiangwan_consumer_profiles;
DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_publications_append_only
    ON xiangwan_consumer_profile_publications;
DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_moderation_append_only
    ON xiangwan_consumer_profile_moderation_decisions;
DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_candidates_append_only
    ON xiangwan_consumer_profile_candidates;
DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_publication_required
    ON xiangwan_consumer_profiles;
DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_update
    ON xiangwan_consumer_profiles;
DROP FUNCTION IF EXISTS xiangwan_reject_consumer_profile_delete();
DROP FUNCTION IF EXISTS xiangwan_reject_consumer_profile_fact_mutation();
DROP FUNCTION IF EXISTS xiangwan_assert_consumer_profile_publication();
DROP FUNCTION IF EXISTS xiangwan_guard_consumer_profile_update();
DROP TABLE IF EXISTS xiangwan_consumer_profiles;
DROP TABLE IF EXISTS xiangwan_consumer_profile_publications;
DROP INDEX IF EXISTS idx_xiangwan_consumer_profile_moderation_jobs_due;
DROP TABLE IF EXISTS xiangwan_consumer_profile_moderation_jobs;
DROP TABLE IF EXISTS xiangwan_consumer_profile_moderation_decisions;
DROP INDEX IF EXISTS idx_xiangwan_consumer_profile_candidates_owner_time;
DROP TABLE IF EXISTS xiangwan_consumer_profile_candidates;
DROP FUNCTION IF EXISTS xiangwan_valid_consumer_profile_tags(JSONB);
