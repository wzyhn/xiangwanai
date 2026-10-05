-- 779_xiangwan_final_convergence.up.sql
-- Forward-only convergence for the final stacked Xiangwan review. This file
-- intentionally repairs already-applied 700-738 schemas instead of rewriting
-- their migration history.

-- The pre-739 worker had no terminal attempt ceiling. Normalize every legacy
-- row before installing the bounded constraint, retaining the terminal fact
-- for any still-pending exhausted job.
UPDATE xiangwan_consumer_profile_moderation_jobs
SET attempt_count = 8,
    updated_at = clock_timestamp()
WHERE attempt_count > 8;

UPDATE xiangwan_consumer_profile_moderation_jobs
SET job_status = 'manual_required',
    lease_token = NULL,
    lease_expires_at = NULL,
    last_result = 'manual_required',
    updated_at = clock_timestamp()
WHERE job_status = 'pending'
  AND attempt_count = 8;

ALTER TABLE xiangwan_consumer_profile_moderation_jobs
    DROP CONSTRAINT IF EXISTS xiangwan_consumer_profile_moderation_job_status_check;
ALTER TABLE xiangwan_consumer_profile_moderation_jobs
    ADD CONSTRAINT xiangwan_consumer_profile_moderation_job_status_check
    CHECK (job_status IN ('pending', 'completed', 'superseded', 'manual_required'));

ALTER TABLE xiangwan_consumer_profile_moderation_jobs
    DROP CONSTRAINT IF EXISTS xiangwan_consumer_profile_moderation_job_attempt_check;
ALTER TABLE xiangwan_consumer_profile_moderation_jobs
    ADD CONSTRAINT xiangwan_consumer_profile_moderation_job_attempt_check
    CHECK (attempt_count BETWEEN 0 AND 8);

ALTER TABLE xiangwan_consumer_profile_moderation_jobs
    DROP CONSTRAINT IF EXISTS xiangwan_consumer_profile_moderation_job_result_check;
ALTER TABLE xiangwan_consumer_profile_moderation_jobs
    ADD CONSTRAINT xiangwan_consumer_profile_moderation_job_result_check
    CHECK (
        last_result IS NULL OR last_result IN (
            'provider_unavailable',
            'wechat_text_v2',
            'approved',
            'pending_review',
            'rejected',
            'superseded',
            'manual_required'
        )
    );

ALTER TABLE xiangwan_consumer_profile_moderation_decisions
    DROP CONSTRAINT IF EXISTS xiangwan_consumer_profile_moderation_evidence_check;
ALTER TABLE xiangwan_consumer_profile_moderation_decisions
    ADD CONSTRAINT xiangwan_consumer_profile_moderation_evidence_check
    CHECK (
        (
            decision_source = 'wechat_text_v2'
            AND provider_trace_id IS NOT NULL
            AND provider_trace_id = btrim(provider_trace_id)
            AND char_length(provider_trace_id) BETWEEN 1 AND 128
            AND provider_trace_id !~ '[[:cntrl:]]'
            AND provider_observed_at IS NOT NULL
            AND provider_observed_at = occurred_at
            AND provider_policy_version IS NOT NULL
            AND provider_policy_version = 'wechat_msg_sec_check_v2.scene_1'
            AND provider_label IS NOT NULL
            AND provider_label BETWEEN 0 AND 1000000
            AND provider_suggest IS NOT NULL
            AND (
                (provider_suggest = 'pass' AND verdict = 'approved')
                OR (provider_suggest = 'review' AND verdict = 'pending_review')
                OR (provider_suggest = 'risky' AND verdict = 'rejected')
            )
        ) OR (
            decision_source <> 'wechat_text_v2'
            AND provider_trace_id IS NULL
            AND provider_observed_at IS NULL
            AND provider_policy_version IS NULL
            AND provider_label IS NULL
            AND provider_suggest IS NULL
            AND (
                decision_source = 'human_review'
                OR (decision_source = 'empty_content' AND verdict = 'approved')
                OR (
                    decision_source = 'provider_unavailable'
                    AND verdict = 'pending_review'
                )
            )
        )
    );

-- Login is the first collection of a stable WeChat subject. Keep the policy
-- basis as a product-owned immutable fact in the same serializable transaction
-- as the Auth-owned Principal/IdentityLink convergence. Provider subjects and
-- one-time codes are deliberately absent from this ledger.
CREATE TABLE IF NOT EXISTS xiangwan_identity_login_events (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    generation_id UUID NOT NULL,
    app_id TEXT NOT NULL,
    privacy_policy_version TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT xiangwan_identity_login_events_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_identity_login_events_app_id_check
        CHECK (app_id ~ '^wx[A-Za-z0-9]{1,62}$'),
    CONSTRAINT xiangwan_identity_login_events_policy_check
        CHECK (
            privacy_policy_version ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
        )
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_identity_login_events_principal_time
    ON xiangwan_identity_login_events (
        tenant_id, principal_id, occurred_at DESC, id DESC
    );

CREATE OR REPLACE FUNCTION xiangwan_reject_identity_login_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan identity login events are append-only'
        USING ERRCODE = '0A000';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_identity_login_events_append_only
    ON xiangwan_identity_login_events;
CREATE TRIGGER trg_xiangwan_identity_login_events_append_only
    BEFORE UPDATE OR DELETE ON xiangwan_identity_login_events
    FOR EACH ROW
    EXECUTE FUNCTION xiangwan_reject_identity_login_event_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_identity_login_events_no_truncate
    ON xiangwan_identity_login_events;
CREATE TRIGGER trg_xiangwan_identity_login_events_no_truncate
    BEFORE TRUNCATE ON xiangwan_identity_login_events
    FOR EACH STATEMENT
    EXECUTE FUNCTION xiangwan_reject_identity_login_event_mutation();
