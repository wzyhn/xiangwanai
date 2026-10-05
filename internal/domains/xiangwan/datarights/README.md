---
layer: L3
capability: xiangwan-data-rights
status: implemented
owner: "@platform"
same_layer_dependencies: []
tables:
  - "xiangwan_data_rights_cases"
  - "xiangwan_data_rights_case_events"
events:
  emitted: []
  consumed: []
config:
  - "XIANGWAN_PRIVACY_POLICY_VERSION"
auth:
  - "consumer submissions and reads derive Tenant and Principal from the independent runtime"
  - "PostgreSQL rechecks the active Principal before creating or reading a case"
tests:
  unit: "go test ./internal/domains/xiangwan/datarights/..."
---

# Xiangwan data rights

This package owns the Redis-free personal-data rights intake and consumer
history model. A request records its owner, exact access/correction/export/
deletion scope, server-owned privacy-policy version, and immutable operation
fingerprint. It creates a case for review; it never promises synchronous or
unqualified physical deletion.

The `profile` scope also covers the private account phone defaults owned by
`identity` (`xiangwan_registration_contact_defaults`). A reviewed access,
correction or deletion request must include this private table. Logical account
deletion erases the defaults through migration 824; registration snapshots have
their independent retention and case decisions. Intake alone is not erasure.

Lifecycle, policy-basis, and delivery facts are append-only events. The public
consumer projection omits operation keys, request fingerprints, actor identity,
and evidence digests. Future data-officer commands must update the case and add
the matching versioned event in one PostgreSQL transaction.
