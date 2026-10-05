---
layer: L3
capability: xiangwan-consumer-profile
status: implemented
owner: "@platform"
same_layer_dependencies: []
tables:
  - "xiangwan_consumer_profiles"
  - "xiangwan_consumer_profile_candidates"
  - "xiangwan_consumer_profile_moderation_decisions"
  - "xiangwan_consumer_profile_moderation_jobs"
  - "xiangwan_consumer_profile_publications"
events:
  emitted: []
  consumed: []
config:
  - "XIANGWAN_PRIVACY_POLICY_VERSION"
  - "XIANGWAN_APP_ID"
  - "XIANGWAN_APP_SECRET"
auth:
  - "Tenant and Principal come only from the independent runtime"
  - "nickname and avatar remain Auth-owned and are never mirrored"
tests:
  unit: "go test ./internal/domains/xiangwan/consumerprofile/..."
---

# Xiangwan consumer profile

This package owns only the Xiangwan extension fields: occupation, personal
introduction, tags, and their explicit visibility choices. The current public
value is advanced only by an approved candidate and immutable publication
fact. Review and provider-unavailable results preserve the last approved
version while retaining the replacement candidate in PostgreSQL.

Every update uses a UUIDv4 operation key, an expected profile version, the
server's current privacy-policy version, and strict content moderation. Every
accepted WeChat result is bound to the candidate with its trace ID, observation
time, label, suggestion, and fixed API-policy version before `pass` can publish.
Risky results are durably rejected. A leased PostgreSQL worker retries review,
missing-binding, unavailable, and malformed-result candidates; a later verified
pass publishes atomically and a later risky result closes the job without
publication. PostgreSQL is the only business-state, lease, and idempotency
authority; no Redis cache or session is introduced.
