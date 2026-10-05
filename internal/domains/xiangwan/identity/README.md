---
layer: L3
capability: xiangwan-consumer-identity
status: implemented
owner: "@platform"
same_layer_dependencies: []
tables:
  - "principals"
  - "identity_links"
  - "principal_product_states"
  - "xiangwan_registration_contact_defaults"
events:
  emitted: []
  consumed: []
config:
  - "XIANGWAN_APP_ID"
  - "XIANGWAN_APP_SECRET"
  - "JWT_SIGNING_KEY"
auth:
  - "WeChat login exchanges a single-use code against one server-owned AppID"
  - "PostgreSQL is the only provider-identity and Principal binding authority"
tests:
  unit: "go test ./internal/domains/xiangwan/identity/..."
---

# Xiangwan consumer identity

This package owns the independent Xiangwan consumer-login path. A transient
WeChat code is exchanged by the server and never persisted. The resulting
OpenID and optional UnionID are bound to one active Principal in PostgreSQL;
the WeChat session key and customer AppSecret never cross the application or
HTTP boundary.

The runtime exposes this boundary only as `POST /api/v1/auth/wechat/login`.
The request contains the one-time `code` and shared-client `app_id`. The latter
must exactly match the server-owned AppID, so it is an assertion rather than an
AppID/product/tenant/role/audience selector. Provider failures are reduced to a
small secret-safe category and correlated by request ID; raw provider errors,
request URLs, codes, and AppSecret values are not logged.

The PostgreSQL identity transaction takes a shared lock on the exact active
Runtime generation before touching the provider binding, Principal, or product
login facts. A deployment cutover therefore either waits for the old login to
commit or makes that stale process fail before any identity write.

The product-local access token is fixed to the Xiangwan AppID, product code,
and consumer audience. Every protected request still rechecks the Principal's
live PostgreSQL status, so disabling or deleting the account invalidates access
without a Redis session or revocation cache.

The private `/me/registration-contact` boundary stores manually entered phone
defaults for future registrations in customer PostgreSQL. Nickname stays in
the Auth-owned Principal; both changes commit atomically after content safety,
profile ETag, contact version, policy and active-generation checks. Phone is
returned only to its authenticated owner, never in public profile projections,
browser storage, logs or identity verification. Existing registrations retain
their independent snapshots. Logical/physical account deletion erases defaults;
personal-data requests include this table in the profile scope.
