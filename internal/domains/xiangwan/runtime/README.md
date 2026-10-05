---
layer: L3
capability: xiangwan-runtime
status: implemented
owner: "@platform"
same_layer_dependencies:
  - xiangwan-public-api
  - xiangwan-booking
  - xiangwan-coupon
  - xiangwan-consumer-profile
  - xiangwan-data-rights
  - xiangwan-consumer-identity
  - xiangwan-people
tables:
  - "xiangwan_runtime_generations"
  - "xiangwan_runtime_generation_activations"
  - "xiangwan_security_throttles"
  - "xiangwan_payment_close_jobs"
  - "xiangwan_payment_merchant_config_generations"
  - "xiangwan_consumer_profile_moderation_jobs"
  - "xiangwan_admin_identity_links"
  - "xiangwan_admin_login_attempts"
  - "xiangwan_admin_sessions"
  - "xiangwan_admin_grants"
  - "xiangwan_admin_operations"
  - "xiangwan_admin_audit_events"
events:
  emitted: []
  consumed: []
config:
  - "XIANGWAN_API_DATABASE_DSN"
  - "XIANGWAN_TENANT_ID"
  - "XIANGWAN_GENERATION_ID"
  - "JWT_SIGNING_KEY"
  - "JWT_PREVIOUS_SECRETS"
  - "XIANGWAN_APP_ID"
  - "XIANGWAN_APP_SECRET"
  - "XIANGWAN_LISTEN_ADDRESS"
  - "SERVER_TRUSTED_PROXIES"
  - "XIANGWAN_EXTERNAL_DOMAINS"
  - "XIANGWAN_EXTERNAL_DOMAINS_POLICY_VERSION"
  - "XIANGWAN_PRIVACY_POLICY_VERSION"
  - "XIANGWAN_USER_AGREEMENT_VERSION"
  - "XIANGWAN_CUSTOMER_SERVICE_POLICY_VERSION"
  - "STORAGE_LOCAL_DIR"
  - "XIANGWAN_MEDIA_CLEANUP_DATABASE_DSN"
  - "XIANGWAN_COUPON_RECONCILIATION_DATABASE_DSN"
  - "XIANGWAN_ALLOW_MANUAL_CONTACT"
  - "XIANGWAN_MANUAL_CONTACT_POLICY_VERSION"
  - "XIANGWAN_MANUAL_CONTACT_POLICY_TEXT"
  - "XIANGWAN_PAID_REGISTRATION_ENABLED"
  - "XIANGWAN_PAYMENT_MERCHANT_ID"
  - "XIANGWAN_PAYMENT_MERCHANT_CONFIG_GENERATION_ID"
  - "XIANGWAN_CANCEL_POLICY_VERSION"
  - "XIANGWAN_CANCEL_FULL_REFUND_CUTOFF_HOURS"
  - "XIANGWAN_COUPON_REFUND_POLICY_VERSION"
  - "XIANGWAN_COUPON_REFUND_DISPOSITION"
  - "XIANGWAN_COUPON_GRANT_POLICY_PUBLIC_KEY"
  - "XIANGWAN_CHECKIN_CREDENTIAL_HMAC_KEY"
  - "XIANGWAN_CHECKIN_CREDENTIAL_TTL_SECONDS"
  - "XIANGWAN_WECHAT_PREPAY_ENABLED"
  - "XIANGWAN_WECHAT_PAY_CERT_SERIAL"
  - "XIANGWAN_WECHAT_PAY_PRIVATE_KEY_FILE"
  - "XIANGWAN_WECHAT_PAY_PUBLIC_KEY_ID"
  - "XIANGWAN_WECHAT_PAY_PUBLIC_KEY_FILE"
  - "XIANGWAN_WECHAT_PAY_API_V3_KEY"
  - "XIANGWAN_WECHAT_PAY_NOTIFY_URL"
  - "XIANGWAN_WECHAT_PAY_DESCRIPTION"
  - "XIANGWAN_PAYMENT_CLOSE_MERCHANT_ID"
  - "XIANGWAN_PAYMENT_CLOSE_APP_ID"
  - "XIANGWAN_PAYMENT_CLOSE_MERCHANT_CONFIG_GENERATION_ID"
  - "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_CERT_SERIAL"
  - "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_PRIVATE_KEY_FILE"
  - "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_PUBLIC_KEY_ID"
  - "XIANGWAN_PAYMENT_CLOSE_WECHAT_PAY_PUBLIC_KEY_FILE"
  - "XIANGWAN_ADMIN_ORIGIN"
  - "XIANGWAN_ADMIN_OIDC_ISSUER"
  - "XIANGWAN_ADMIN_OIDC_CLIENT_ID"
  - "XIANGWAN_ADMIN_OIDC_CLIENT_SECRET"
  - "XIANGWAN_ADMIN_OIDC_REDIRECT_URL"
  - "XIANGWAN_ADMIN_OIDC_REQUIRED_ACR"
  - "XIANGWAN_ADMIN_OIDC_CA_FILE"
  - "XIANGWAN_ADMIN_SESSION_KEY"
auth:
  - "anonymous WeChat login uses only the fixed customer AppID and server-owned AppSecret"
  - "anonymous WeChat provider exchange is protected by a PostgreSQL cross-replica action window"
  - "consumer and public product requests require the exact active PostgreSQL generation"
  - "the provider-signed payment callback remains outside the Runtime deployment-generation gate, but still requires the configured merchant-configuration generation admission"
  - "consumer tokens require the customer signing key plus exact audience, product, and AppID"
  - "every protected request checks the live active Principal in PostgreSQL without a cache"
  - "administrator requests use customer OIDC Authorization Code + PKCE, an opaque PostgreSQL session, same-origin CSRF checks, and live PostgreSQL Grants"
tests:
  unit: "go test ./internal/domains/xiangwan/runtime"
  integration: "go test -tags=integration ./internal/test/integration -run XiangwanRuntime"
runbook:
  - "Run go run ./cmd/xiangwan serve with one fixed Business Tenant and active generation"
  - "Run go run ./cmd/xiangwan worker to drain PostgreSQL payment-close jobs"
  - "Run go run ./cmd/xiangwan profile-moderation-worker to resolve pending profile moderation jobs"
  - "Run go run ./cmd/xiangwan media-cleanup-worker to expire private review File objects"
  - "Run go run ./cmd/xiangwan coupon-reconciliation-worker to drain eligible Checkin grants and revoked-Checkin corrections"
  - "Activate with the expected write epoch and a separate deployment-role PostgreSQL credential"
  - "PostgreSQL is the only runtime state and coordination dependency"
---

The close worker uses its configured merchant-configuration generation when it
converges queued payment closes. A NULL Order snapshot is accepted only by the
explicit pre-791 drain path; strict API composition rejects NULL or a mismatched
generation. This is an admission boundary, not a live secret/status rotation
fence.

# Xiangwan Runtime

This package is the independent, Redis-free composition root for Xiangwan. It
opens only PostgreSQL, fixes one customer Tenant and deployment generation from
server-owned configuration, and wires the public home, Session routing/detail,
public People discovery, authenticated questionnaire and consumer views,
review, and media routes to customer-owned PostgreSQL and a local media volume
without importing the central Weconq router or middleware graph.

The raw HTTP server clears its ordinary 30-second response write deadline only
for the exact public media GET/HEAD route, so large authorized objects are not
truncated while every other API response remains bounded.

The root mounts only its narrow WeChat login handler at
`POST /api/v1/auth/wechat/login`. Code exchange uses the fixed customer AppID
and required AppSecret; a PG-backed per-AppID/action minute window limits
provider exchange across replicas and fails closed when its authority is
unavailable. Caller buckets use Gin's client address only after deployment has
configured an explicit `SERVER_TRUSTED_PROXIES` allowlist; without one,
forwarding headers are ignored. Identity convergence is serializable and locks
the exact active Runtime generation in the same transaction, so a stale process
cannot commit login state after cutover. Issued sessions carry the exact
consumer role, AppID, Xiangwan product, and single audience expected by
protected routes.
Request IDs cross the WeChat call, and provider failures are logged only as a
secret-safe category tied to that correlation ID.

Public Session routing supports both an exact Instance and a Series resolved
through its current-public-Instance pointer. Both paths preserve the same
zero/one/many decision and never select the first of multiple Sessions.

The anonymous Public Policies route projects immutable version identifiers,
fail-closed capability switches, and the approved manual-registration contact
policy text from deployment configuration. Missing
privacy, manual-contact, agreement, customer-service, cancellation, or
external-domain policy versions remain unavailable; no submitted contact,
customer-service detail, or allowlist is exposed. The manual-contact text and
exact version let a client disclose its use before PII entry and satisfy the
Registration snapshot contract. Privacy text is opened through WeChat's native
privacy contract. External-domain configuration is accepted only with its
matching published policy version.

The Data Rights intake and history routes are wired directly to the customer
PostgreSQL database. Runtime authentication supplies the owner, the configured
privacy-policy version supplies the legal basis, and absent policy configuration
closes new submissions without disabling owner history reads. Operation replay,
case lifecycle, and delivery evidence use no Redis state.

The Consumer Profile routes compose the live Auth-owned nickname and approved
avatar with versioned Xiangwan extension fields directly from PostgreSQL. The
write path derives owner, AppID, provider OpenID, and current privacy-policy
version on the server, reuses the runtime's WeChat client, and requires durable
trace/time/policy evidence before a provider pass can publish. Review,
unavailable, and malformed results retain a PostgreSQL candidate and the last
approved publication. The least-privilege `profile-moderation-worker` uses
PostgreSQL `SKIP LOCKED` leases to recheck those candidates and append a later
verified decision; generation cutover fences both initial and worker writes.
Profile idempotency and leases use no Redis state. Auth login timestamps are not
part of the nickname/avatar ETag.

Authenticated Series favorite PUT/DELETE routes use the product-local
Principal and fixed Tenant, then commit the unique relation, aggregate counter,
and Series version together in PostgreSQL. Replays are target-state no-ops and
no Redis counter is introduced.

The matching My Favorites collection reads those owner-scoped relations
directly from PostgreSQL with stable keyset pagination. It returns safe Series
display facts and a Series Sessions entry point without exposing relation,
Tenant, Principal, or current Instance identities.

Public People list/detail reads use the same fixed runtime Tenant and only the
approved, published PeopleProfile set. They do not require or inspect a trusted
Principal binding, so an intentionally unbound public profile remains
displayable without turning presentation content into identity evidence.

When complete OIDC and a privacy policy are configured, People binding invites
use the existing session key with a separate HMAC purpose. Super-admin commands
create/revoke invites and withdraw bindings; strict consumer authentication
supplies the owner for private preview and explicit confirmation. The consumer
routes share a PostgreSQL per-owner ten-request/minute window. Codes never enter
URLs, logs, stored operator receipts or persistent client storage; confirmations
recheck generation and live inviter authority inside the fact transaction.

The consumer Coupon wallet is wired directly to the immutable PostgreSQL
ledger reader. Its tenant comes from this runtime and its owner from the strict
consumer token, so cursors cannot cross customers or principals and no Redis
balance projection is introduced.

An optional standard-Base64 Ed25519 customer public key enables only the
administrator's signed Coupon grant-policy registration route. The route is
absent without complete OIDC configuration and that key. It verifies canonical
customer-approved bytes, exact tenant, future effective time, live generation
and super-admin identity/Grant before storing an immutable policy version and
audit. It does not itself start issuance or supply face value defaults.

The My Benefits view is wired to the People repeatable-read snapshot. Runtime
authentication supplies the owner, the same transaction rechecks the active
Principal, and the response contains only safe role, application, and rebuilt
contribution summaries. Because `CONFIG-HOST-RULES` is not yet signed, the
runtime deliberately supplies a pending provider: it displays no fabricated
requirements or benefits and does not enable a host application action.
The dedicated Host Applications history route uses the same owner fence and
safe summary projection for the consumer application-status page.
The matching submission route is wired to the serializable PostgreSQL writer;
it is present but fails closed until the customer-owned `CONFIG-HOST-RULES`
source is signed and replaced at the composition root.
The Identity History route projects explicit binding, Instance-role, and
contribution-ledger facts from the same authenticated snapshot without a
second cache or inferred People binding.

Protected consumer routes use a product-local strict bearer boundary. Tokens
must be signed by this customer runtime, carry the exact Xiangwan audience,
product code, and configured AppID, and have a bounded lifetime. A valid token
still performs a live `principals.status='active' AND deleted_at IS NULL`
PostgreSQL read before the handler receives the Principal ID.

The authenticated Registration cancellation route is always available for free
and known-unpaid participation. Paid/provider-unknown cancellation is enabled
only by a complete versioned full-refund cutoff policy, and Coupon-settled
cancellation additionally requires an explicit versioned restore/forfeit
policy. The local evaluators run inside the existing serializable cancellation
transaction, persist their versions in the resulting facts, and fail closed
when configuration is absent or malformed.

Checkin credential issue uses a customer-owned HMAC key that is separate from
the JWT signing key. The runtime never writes that key or plaintext credentials
to PostgreSQL. Its configurable TTL defaults to ten minutes and cannot exceed
the domain's fifteen-minute ceiling; each successful issue atomically rotates
the prior credential epoch.

The same independently deployed runtime now owns a separate administrator
boundary under `/api/v1/xiangwan/admin/*`. Customer OIDC Authorization Code +
PKCE creates an opaque PostgreSQL session only after an exact issuer/subject
identity link and active Grant resolve. Unsafe requests require a same-origin
double-submit CSRF token, and every activity/check-in write rechecks the active
Runtime generation, Principal, identity link, Grant, version, and operation
receipt inside PostgreSQL. The on-site verification and recording routes are
mounted only when the complete OIDC configuration is present; otherwise only
the public auth status/login surface exists and fails closed. Consumer JWTs are
never accepted at this boundary.

The administrator catalog also mounts the exact-period questionnaire and
review-resource commands. Instance creation accepts the `custom` activity type
and ordered detail blocks; questionnaire publication is immutable per Instance
and adds fields alongside the required registration nickname/phone snapshot.
The review writer accepts an allowlisted HTTPS video-channel link and optional
text, then commits Content standalonepg, the exact Resource relation, snapshot,
manual-review observation, and publication in one transaction. An optional
private-media constructor also verifies the exact confirmed File intent and
bytes, requires a successful private-open audit after confirmation, and pins
the File in that same publication transaction. The production composition
does not enable this constructor or the upload routes while the customer media
review policy and end-to-end storage checks remain open. Taking an activity offline remains the existing
preview-then-final cancellation command, whose serializable transaction
rechecks the expected Instance version and all registration, payment, refund,
and Coupon policy facts before returning the receipt.

`/live` reports process liveness. `/ready` and every public/consumer product
request require the exact configured generation to be active for the single
Xiangwan Business Tenant in PostgreSQL. A missing, stale, cross-customer, or
incomplete generation fails closed with a generic service-unavailable response.
The cryptographically authenticated payment callback is the sole exception so
an in-flight provider fact can settle across a deployment transition.

Positive-price Registration and WeChat mini-program prepay are a coupled
fail-closed feature. Both are disabled by default; when paid Registration is
enabled, prepay must also be enabled. When enabled,
startup loads a read-only merchant private key and WeChat Pay public key from
configured PEM paths, constructs the official API v3 SDK adapter, and wires a
recoverable PostgreSQL attempt service plus owner-bound merchant-order queries.
The public-key verifier is also paired with the configured API v3 key for the
provider callback's AES-256-GCM resource. Query leases, notification replay
keys, cooldowns, immutable transaction observations, and financial convergence
all remain in PostgreSQL. The callback is deliberately outside the Runtime
deployment-generation gate, but its configured merchant-configuration metadata
must be `active` or `draining` and must match the immutable Order snapshot. A
previous merchant generation is not automatically routed by the single
callback decoder; drain old pending/unknown Orders or keep a controlled old
static API window before enabling the strict configured path. Fixed
tenant/AppID/merchant/Order identities and provider cryptography remain
mandatory. No key material, OpenID, provider transaction
identifier, or raw provider payload crosses the HTTP response boundary, and no
Redis lease is used.

The same Xiangwan binary also exposes a dedicated `worker` process for durable
provider close reconciliation. Its least-privilege configuration contains only
the customer PostgreSQL identity, exact Tenant/generation, merchant identity,
and read-only provider key paths; it does not load HTTP JWT, media, callback
decryption, notification, or Redis configuration. The worker drains due jobs,
queries WeChat before every close attempt, exits when its configured generation
is no longer active, and finishes an in-flight database completion during
graceful shutdown. A lost close-job lease waits one poll interval and retries;
database, schema, configuration, and merchant identity errors remain fail-fast
for operator intervention. `worker-health` first
parses the same local merchant and
provider PEM credentials as `worker`, then checks the exact active generation
and migration 774 schema without exposing a network listener or making a
provider API request. It also rejects any non-terminal queued close job whose
AppID or merchant ID differs from the worker's explicit payment identity, and
requires the configured merchant generation metadata row to match the tenant,
AppID, merchant ID, provider, and active/draining status. This startup check
does not provide a live revoke or secret rotation fence. Acquisition is scoped
to the worker's configured merchant generation and AppID/merchant identity;
legacy NULL snapshots remain eligible only for the matching identity. A
different generation's queued jobs do not make this worker unhealthy, so an
old-generation worker can drain alongside a new-generation worker during a
controlled handoff.
The current compose profile provides one worker and derives its AppID from the
API environment; cross-AppID or cross-runtime-generation handoff therefore
requires separate worker deployment configuration and is not a single-service
rotation guarantee.

The separate `media-cleanup-worker` uses a dedicated PostgreSQL credential,
the active Tenant/generation and the same private `STORAGE_LOCAL_DIR` volume as
the API. Each expired review File is claimed through its exact tenant upload
intent and immutable object key; the File row lock conflicts with publication
`Pin`. The local object delete is idempotent, and an exact `deleting_at` lease
fences success or bounded failure completion. Stale claims are replayed after
ten minutes. Only abandoned `.review-upload-*` request temporaries use the
separate 72-hour filesystem-age prune. The worker has no WeChat, OIDC, JWT or
payment secrets. `media-cleanup-worker-health` checks the active generation and
required schema; it does not prove a real customer deployment is running.

The optional `coupon-reconciliation-worker` uses only a dedicated PostgreSQL
credential, Tenant and generation. It first drains Checkin revocation outbox
tasks, then reconciles host contributions, Coupon corrections, and finally
new invited-guest grants from durable facts. A task is acknowledged only after
both ledgers converge. Five-minute leases recover after process loss; failed
tasks back off up to one hour and the persisted 25-attempt limit leaves a
`dead_letter` record for operator investigation. Errors retain only a fixed
category, never submitted private content. First-guest
source listing excludes Checkins for which no enabled signed grant-policy
version was effective at the source time; an older unapproved Checkin cannot
block later eligible work or be retroactively priced. Both grant policy reads
and correction/Contribution writes lock the exact active generation inside their own
transactions, so a stale worker exits at cutover without creating a new
ledger fact. The worker has no HTTP, OIDC, WeChat, media or customer signing
key. `coupon-reconciliation-worker-health` checks generation and required
schema; customer policy values and real PostgreSQL reconciliation still need
deployment evidence.

With complete administrator OIDC and `XIANGWAN_COUPON_GRANT_POLICY_PUBLIC_KEY`,
the API also registers the signed policy activation and manual `coupon-grants`
routes. Manual issuance derives its owner from an existing tenant Coupon and
rechecks the exact super-admin identity, generation, zero available balance,
and live signed policy in the serializable ledger transaction. Missing
customer policy values leave issuance closed; the route is not a substitute
for correction of held or redeemed Coupon financial facts.

With complete administrator OIDC, the Catalog also mounts
`POST /coupon-corrections/:entry_id/actions`. It records two-person handling
through migration 821 without requiring a grant-policy signing key and without
performing financial provider calls. The adapter rechecks live super-admin
identity, generation, immutable Coupon history and exact version in its write
transaction; an identical operation returns its stored receipt.

Migration 726 stores the singleton generation authority. Its Tenant and
bootstrap completion are immutable, activation advances the epoch exactly once,
and delete or truncate operations are rejected. Initial insertion cannot make a
generation active; deployment activation must be a separate update.

Migration 727 and `GenerationActivator` implement that deployment operation.
The serializable transaction locks the singleton row, verifies the exact
Business Tenant and completed bootstrap, compares the caller's expected write
epoch, derives the operator identity from the PostgreSQL role, and records an
immutable receipt. A generation ID can never be reused; an exact retry returns
the first committed activation without incrementing the epoch again.

`Bootstrapper` reports an already-active matching tenant/generation and its
current epoch without repeating activation. A changed release reason or an
explicit second-generation cutover therefore permits later bootstrap replay
while preserving activation history. A mismatching tenant/generation still
fails closed; bootstrap never replaces an active pair. The PostgreSQL bootstrap
regression covers both restart cases and unchanged activation history.
Cutover and read-only HTTP verification are documented in
[`COOLIFY.md`](../../../../deploy/xiangwan/COOLIFY.md).
