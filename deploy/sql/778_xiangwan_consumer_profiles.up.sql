-- 778_xiangwan_consumer_profiles.up.sql
-- Versioned Xiangwan-owned consumer profile candidates, verified moderation
-- observations, leased retries, and publications. Auth remains the sole owner
-- of nickname and avatar.

CREATE OR REPLACE FUNCTION xiangwan_valid_consumer_profile_tags(value JSONB)
RETURNS BOOLEAN
LANGUAGE plpgsql
IMMUTABLE
AS $$
DECLARE
    item JSONB;
    tag TEXT;
    seen JSONB := '{}'::jsonb;
BEGIN
    IF jsonb_typeof(value) IS DISTINCT FROM 'array'
        OR jsonb_array_length(value) > 8 THEN
        RETURN FALSE;
    END IF;
    FOR item IN SELECT * FROM jsonb_array_elements(value)
    LOOP
        IF jsonb_typeof(item) IS DISTINCT FROM 'string' THEN
            RETURN FALSE;
        END IF;
        tag := item #>> '{}';
        IF tag IS DISTINCT FROM btrim(tag)
            OR char_length(tag) < 1
            OR char_length(tag) > 30
            OR tag ~ '[[:cntrl:]]'
            OR seen ? tag THEN
            RETURN FALSE;
        END IF;
        seen := seen || jsonb_build_object(tag, TRUE);
    END LOOP;
    RETURN TRUE;
END;
$$;

CREATE TABLE IF NOT EXISTS xiangwan_consumer_profile_candidates (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    operation_key UUID NOT NULL,
    request_fingerprint BYTEA NOT NULL,
    base_profile_version BIGINT NOT NULL,
    occupation TEXT NOT NULL DEFAULT '',
    introduction TEXT NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    occupation_public BOOLEAN NOT NULL DEFAULT FALSE,
    introduction_public BOOLEAN NOT NULL DEFAULT FALSE,
    tags_public BOOLEAN NOT NULL DEFAULT FALSE,
    privacy_policy_version TEXT NOT NULL,
    submitted_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xiangwan_consumer_profile_candidates_tenant_id_id_key
        UNIQUE (tenant_id, id),
    CONSTRAINT xiangwan_consumer_profile_candidates_owner_id_key
        UNIQUE (tenant_id, principal_id, id),
    CONSTRAINT xiangwan_consumer_profile_candidates_owner_operation_key
        UNIQUE (tenant_id, principal_id, operation_key),
    CONSTRAINT xiangwan_consumer_profile_candidates_fingerprint_check
        CHECK (octet_length(request_fingerprint) = 32),
    CONSTRAINT xiangwan_consumer_profile_candidates_base_version_check
        CHECK (base_profile_version >= 0),
    CONSTRAINT xiangwan_consumer_profile_candidates_occupation_check
        CHECK (
            occupation = btrim(occupation)
            AND char_length(occupation) <= 80
            AND occupation !~ '[[:cntrl:]]'
        ),
    CONSTRAINT xiangwan_consumer_profile_candidates_introduction_check
        CHECK (
            introduction = btrim(introduction)
            AND char_length(introduction) <= 500
            AND regexp_replace(introduction, chr(10), '', 'g')
                !~ '[[:cntrl:]]'
        ),
    CONSTRAINT xiangwan_consumer_profile_candidates_tags_check
        CHECK (xiangwan_valid_consumer_profile_tags(tags)),
    CONSTRAINT xiangwan_consumer_profile_candidates_policy_check
        CHECK (
            privacy_policy_version
                ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
        )
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_consumer_profile_candidates_owner_time
    ON xiangwan_consumer_profile_candidates (
        tenant_id, principal_id, submitted_at DESC, id DESC
    );

CREATE TABLE IF NOT EXISTS xiangwan_consumer_profile_moderation_decisions (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    candidate_id UUID NOT NULL,
    decision_version BIGINT NOT NULL,
    verdict TEXT NOT NULL,
    decision_source TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    provider_trace_id TEXT,
    provider_observed_at TIMESTAMPTZ,
    provider_policy_version TEXT,
    provider_label INTEGER,
    provider_suggest TEXT,
    CONSTRAINT xiangwan_consumer_profile_moderation_owner_candidate_fkey
        FOREIGN KEY (tenant_id, principal_id, candidate_id)
        REFERENCES xiangwan_consumer_profile_candidates (
            tenant_id, principal_id, id
        ),
    CONSTRAINT xiangwan_consumer_profile_moderation_candidate_version_key
        UNIQUE (tenant_id, candidate_id, decision_version),
    CONSTRAINT xiangwan_consumer_profile_moderation_version_check
        CHECK (decision_version >= 1),
    CONSTRAINT xiangwan_consumer_profile_moderation_verdict_check
        CHECK (verdict IN ('approved', 'pending_review', 'rejected')),
    CONSTRAINT xiangwan_consumer_profile_moderation_source_check
        CHECK (
            decision_source IN (
                'wechat_text_v2',
                'empty_content',
                'provider_unavailable',
                'human_review'
            )
        ),
    CONSTRAINT xiangwan_consumer_profile_moderation_evidence_check
        CHECK (
            (
                decision_source = 'wechat_text_v2'
                AND provider_trace_id IS NOT NULL
                AND provider_trace_id = btrim(provider_trace_id)
                AND char_length(provider_trace_id) BETWEEN 1 AND 128
                AND provider_trace_id !~ '[[:cntrl:]]'
                AND provider_observed_at = occurred_at
                AND provider_policy_version =
                    'wechat_msg_sec_check_v2.scene_1'
                AND provider_label BETWEEN 0 AND 1000000
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
                    OR (
                        decision_source = 'empty_content'
                        AND verdict = 'approved'
                    )
                    OR (
                        decision_source = 'provider_unavailable'
                        AND verdict = 'pending_review'
                    )
                )
            )
        )
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_consumer_profile_moderation_latest
    ON xiangwan_consumer_profile_moderation_decisions (
        tenant_id, candidate_id, decision_version DESC
    );

CREATE TABLE IF NOT EXISTS xiangwan_consumer_profile_moderation_jobs (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    candidate_id UUID NOT NULL,
    job_status TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    available_at TIMESTAMPTZ NOT NULL,
    lease_token UUID,
    lease_expires_at TIMESTAMPTZ,
    last_result TEXT,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, candidate_id),
    CONSTRAINT xiangwan_consumer_profile_moderation_job_candidate_fkey
        FOREIGN KEY (tenant_id, principal_id, candidate_id)
        REFERENCES xiangwan_consumer_profile_candidates (
            tenant_id, principal_id, id
        ),
    CONSTRAINT xiangwan_consumer_profile_moderation_job_status_check
        CHECK (job_status IN ('pending', 'completed', 'superseded')),
    CONSTRAINT xiangwan_consumer_profile_moderation_job_attempt_check
        CHECK (attempt_count >= 0),
    CONSTRAINT xiangwan_consumer_profile_moderation_job_lease_check
        CHECK (
            (lease_token IS NULL) = (lease_expires_at IS NULL)
            AND (lease_expires_at IS NULL OR lease_expires_at > created_at)
        ),
    CONSTRAINT xiangwan_consumer_profile_moderation_job_result_check
        CHECK (
            last_result IS NULL OR last_result IN (
                'provider_unavailable',
                'wechat_text_v2',
                'approved',
                'pending_review',
                'rejected',
                'superseded'
            )
        ),
    CONSTRAINT xiangwan_consumer_profile_moderation_job_time_check
        CHECK (available_at >= created_at AND updated_at >= created_at)
);

CREATE INDEX IF NOT EXISTS idx_xiangwan_consumer_profile_moderation_jobs_due
    ON xiangwan_consumer_profile_moderation_jobs (
        available_at, candidate_id
    )
    WHERE job_status = 'pending';

CREATE TABLE IF NOT EXISTS xiangwan_consumer_profile_publications (
    id UUID PRIMARY KEY,
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    candidate_id UUID NOT NULL,
    profile_version BIGINT NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT xiangwan_consumer_profile_publications_owner_candidate_fkey
        FOREIGN KEY (tenant_id, principal_id, candidate_id)
        REFERENCES xiangwan_consumer_profile_candidates (
            tenant_id, principal_id, id
        ),
    CONSTRAINT xiangwan_consumer_profile_publications_candidate_key
        UNIQUE (tenant_id, candidate_id),
    CONSTRAINT xiangwan_consumer_profile_publications_owner_version_key
        UNIQUE (tenant_id, principal_id, profile_version),
    CONSTRAINT xiangwan_consumer_profile_publications_version_check
        CHECK (profile_version >= 1)
);

CREATE TABLE IF NOT EXISTS xiangwan_consumer_profiles (
    tenant_id UUID NOT NULL REFERENCES tenants(id),
    principal_id UUID NOT NULL REFERENCES principals(id),
    occupation TEXT NOT NULL DEFAULT '',
    introduction TEXT NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    occupation_public BOOLEAN NOT NULL DEFAULT FALSE,
    introduction_public BOOLEAN NOT NULL DEFAULT FALSE,
    tags_public BOOLEAN NOT NULL DEFAULT FALSE,
    privacy_policy_version TEXT NOT NULL,
    version BIGINT NOT NULL,
    published_candidate_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (tenant_id, principal_id),
    CONSTRAINT xiangwan_consumer_profiles_published_candidate_key
        UNIQUE (tenant_id, published_candidate_id),
    CONSTRAINT xiangwan_consumer_profiles_owner_candidate_fkey
        FOREIGN KEY (tenant_id, principal_id, published_candidate_id)
        REFERENCES xiangwan_consumer_profile_candidates (
            tenant_id, principal_id, id
        ),
    CONSTRAINT xiangwan_consumer_profiles_version_check
        CHECK (version >= 1),
    CONSTRAINT xiangwan_consumer_profiles_occupation_check
        CHECK (
            occupation = btrim(occupation)
            AND char_length(occupation) <= 80
            AND occupation !~ '[[:cntrl:]]'
        ),
    CONSTRAINT xiangwan_consumer_profiles_introduction_check
        CHECK (
            introduction = btrim(introduction)
            AND char_length(introduction) <= 500
            AND regexp_replace(introduction, chr(10), '', 'g')
                !~ '[[:cntrl:]]'
        ),
    CONSTRAINT xiangwan_consumer_profiles_tags_check
        CHECK (xiangwan_valid_consumer_profile_tags(tags)),
    CONSTRAINT xiangwan_consumer_profiles_policy_check
        CHECK (
            privacy_policy_version
                ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$'
        ),
    CONSTRAINT xiangwan_consumer_profiles_time_check
        CHECK (updated_at >= created_at)
);

CREATE OR REPLACE FUNCTION xiangwan_guard_consumer_profile_update()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.tenant_id IS DISTINCT FROM OLD.tenant_id
        OR NEW.principal_id IS DISTINCT FROM OLD.principal_id
        OR NEW.created_at IS DISTINCT FROM OLD.created_at
        OR NEW.version <> OLD.version + 1
        OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'invalid xiangwan ConsumerProfile version transition'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_consumer_profile_version_transition';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_update
    ON xiangwan_consumer_profiles;
CREATE TRIGGER trg_xiangwan_consumer_profile_update
BEFORE UPDATE ON xiangwan_consumer_profiles
FOR EACH ROW EXECUTE FUNCTION xiangwan_guard_consumer_profile_update();

CREATE OR REPLACE FUNCTION xiangwan_assert_consumer_profile_publication()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
          FROM xiangwan_consumer_profile_candidates AS candidate
          JOIN xiangwan_consumer_profile_publications AS publication
            ON publication.tenant_id = candidate.tenant_id
           AND publication.principal_id = candidate.principal_id
           AND publication.candidate_id = candidate.id
         WHERE candidate.tenant_id = NEW.tenant_id
           AND candidate.principal_id = NEW.principal_id
           AND candidate.id = NEW.published_candidate_id
           AND candidate.base_profile_version = NEW.version - 1
           AND candidate.occupation = NEW.occupation
           AND candidate.introduction = NEW.introduction
           AND candidate.tags = NEW.tags
           AND candidate.occupation_public = NEW.occupation_public
           AND candidate.introduction_public = NEW.introduction_public
           AND candidate.tags_public = NEW.tags_public
           AND candidate.privacy_policy_version = NEW.privacy_policy_version
           AND publication.profile_version = NEW.version
           AND (
                SELECT decision.verdict
                  FROM xiangwan_consumer_profile_moderation_decisions AS decision
                 WHERE decision.tenant_id = candidate.tenant_id
                   AND decision.candidate_id = candidate.id
                 ORDER BY decision.decision_version DESC
                 LIMIT 1
           ) = 'approved'
    ) THEN
        RAISE EXCEPTION 'ConsumerProfile requires an approved publication fact'
            USING ERRCODE = '23514',
                  CONSTRAINT = 'xiangwan_consumer_profile_publication_required';
    END IF;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_publication_required
    ON xiangwan_consumer_profiles;
CREATE CONSTRAINT TRIGGER trg_xiangwan_consumer_profile_publication_required
AFTER INSERT OR UPDATE ON xiangwan_consumer_profiles
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION xiangwan_assert_consumer_profile_publication();

CREATE OR REPLACE FUNCTION xiangwan_reject_consumer_profile_fact_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan ConsumerProfile facts are append-only'
        USING ERRCODE = '23514',
              CONSTRAINT = 'xiangwan_consumer_profile_facts_append_only';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_candidates_append_only
    ON xiangwan_consumer_profile_candidates;
CREATE TRIGGER trg_xiangwan_consumer_profile_candidates_append_only
BEFORE UPDATE OR DELETE ON xiangwan_consumer_profile_candidates
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_consumer_profile_fact_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_moderation_append_only
    ON xiangwan_consumer_profile_moderation_decisions;
CREATE TRIGGER trg_xiangwan_consumer_profile_moderation_append_only
BEFORE UPDATE OR DELETE ON xiangwan_consumer_profile_moderation_decisions
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_consumer_profile_fact_mutation();

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profile_publications_append_only
    ON xiangwan_consumer_profile_publications;
CREATE TRIGGER trg_xiangwan_consumer_profile_publications_append_only
BEFORE UPDATE OR DELETE ON xiangwan_consumer_profile_publications
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_consumer_profile_fact_mutation();

CREATE OR REPLACE FUNCTION xiangwan_reject_consumer_profile_delete()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'xiangwan ConsumerProfile cannot be deleted'
        USING ERRCODE = '23514',
              CONSTRAINT = 'xiangwan_consumer_profiles_no_delete';
END;
$$;

DROP TRIGGER IF EXISTS trg_xiangwan_consumer_profiles_no_delete
    ON xiangwan_consumer_profiles;
CREATE TRIGGER trg_xiangwan_consumer_profiles_no_delete
BEFORE DELETE ON xiangwan_consumer_profiles
FOR EACH ROW EXECUTE FUNCTION xiangwan_reject_consumer_profile_delete();
