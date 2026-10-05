---
layer: L3
capability: xiangwan-public-api
status: implemented
owner: "@platform"
same_layer_dependencies:
  - xiangwan-activity
  - xiangwan-booking
  - xiangwan-coupon
  - xiangwan-consumer-profile
  - xiangwan-data-rights
  - xiangwan-consumer-identity
  - xiangwan-checkin
  - xiangwan-people
  - xiangwan-resource
routes:
  - "POST /api/v1/auth/wechat/login"
  - "GET /api/v1/xiangwan/home-sessions"
  - "GET /api/v1/xiangwan/series/:series_id/sessions"
  - "GET /api/v1/xiangwan/instances/:instance_id/sessions"
  - "GET /api/v1/xiangwan/sessions/:session_id"
  - "GET /api/v1/xiangwan/past-activities"
  - "GET /api/v1/xiangwan/instances/:instance_id/review"
  - "GET|HEAD /api/v1/xiangwan/media/:relation_id/:block_id/:file_id"
  - "GET /api/v1/xiangwan/people"
  - "GET /api/v1/xiangwan/people/:people_id"
  - "GET /api/v1/xiangwan/public-policies"
  - "GET|HEAD /api/v1/xiangwan/covers/:filename"
  - "GET|HEAD /api/v1/xiangwan/avatars/:filename"
  - "GET|HEAD /api/v1/xiangwan/avatars/legacy/:principal_id/:file_id"
  - "GET|HEAD /api/v1/xiangwan/brand-hero/:filename"
  - "GET /api/v1/xiangwan/sessions/:session_id/questionnaire"
  - "GET /api/v1/xiangwan/me/questionnaire-prefill/:session_id"
  - "POST /api/v1/xiangwan/sessions/:session_id/registrations"
  - "GET /api/v1/xiangwan/me/registrations"
  - "GET /api/v1/xiangwan/me/registrations/:registration_id"
  - "GET /api/v1/xiangwan/me/orders"
  - "GET /api/v1/xiangwan/me/orders/:order_id"
  - "GET /api/v1/xiangwan/me/coupons"
  - "GET /api/v1/xiangwan/me/favorites"
  - "GET /api/v1/xiangwan/me/data-rights-requests"
  - "POST /api/v1/xiangwan/me/data-rights-requests"
  - "GET /api/v1/xiangwan/me/profile"
  - "PATCH /api/v1/xiangwan/me/profile"
  - "PATCH /api/v1/xiangwan/me/profile/nickname"
  - "POST /api/v1/xiangwan/me/avatar"
  - "GET /api/v1/xiangwan/me/benefits"
  - "GET /api/v1/xiangwan/me/host-applications"
  - "POST /api/v1/xiangwan/me/host-applications"
  - "GET /api/v1/xiangwan/me/identity-history"
  - "PUT /api/v1/xiangwan/series/:series_id/favorite"
  - "DELETE /api/v1/xiangwan/series/:series_id/favorite"
  - "POST /api/v1/xiangwan/orders/:order_id/wechat-prepay-attempts"
  - "POST /api/v1/xiangwan/orders/:order_id/payment-queries"
  - "POST /api/v1/xiangwan/registrations/:registration_id/cancellations"
  - "POST /api/v1/xiangwan/registrations/:registration_id/checkin-credentials"
  - "GET /api/v1/xiangwan/admin/auth/status"
  - "GET /api/v1/xiangwan/admin/auth/login"
  - "GET /api/v1/xiangwan/admin/auth/callback"
  - "POST /api/v1/xiangwan/admin/auth/logout"
  - "GET /api/v1/xiangwan/admin/brand-profile"
  - "PATCH /api/v1/xiangwan/admin/brand-profile"
  - "POST /api/v1/xiangwan/admin/brand-hero-images"
  - "GET /api/v1/xiangwan/admin/series"
  - "POST /api/v1/xiangwan/admin/series"
  - "PATCH /api/v1/xiangwan/admin/series/:series_id"
  - "GET /api/v1/xiangwan/admin/instances"
  - "POST /api/v1/xiangwan/admin/instances"
  - "GET /api/v1/xiangwan/admin/instances/:instance_id"
  - "GET /api/v1/xiangwan/admin/instances/:instance_id/review-status"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/review-resources"
  - "GET /api/v1/xiangwan/admin/instances/:instance_id/questionnaire"
  - "PATCH /api/v1/xiangwan/admin/instances/:instance_id"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/sessions"
  - "PATCH /api/v1/xiangwan/admin/instances/:instance_id/sessions/:session_id"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/questionnaire"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/publications"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/completion"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/archives"
  - "POST /api/v1/xiangwan/admin/sessions/:session_id/archives"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/cancellation-previews"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/cancellation"
  - "GET /api/v1/xiangwan/admin/registrations"
  - "GET /api/v1/xiangwan/admin/registrations/answer-summaries"
  - "GET /api/v1/xiangwan/admin/registrations/:registration_id/answers"
  - "GET /api/v1/xiangwan/admin/registrations/:registration_id"
  - "GET /api/v1/xiangwan/admin/audit-events"
  - "GET /api/v1/xiangwan/admin/refund-cases"
  - "GET /api/v1/xiangwan/admin/refund-cases/:case_id"
  - "GET /api/v1/xiangwan/admin/orders"
  - "GET /api/v1/xiangwan/admin/orders/:order_id"
  - "GET /api/v1/xiangwan/admin/checkin-targets"
  - "GET /api/v1/xiangwan/admin/people"
  - "POST /api/v1/xiangwan/admin/people"
  - "PATCH /api/v1/xiangwan/admin/people/:people_id"
  - "POST /api/v1/xiangwan/admin/people/:people_id/reviews"
  - "GET /api/v1/xiangwan/admin/instances/:instance_id/roles"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/roles"
  - "POST /api/v1/xiangwan/admin/instances/:instance_id/roles/:role_binding_id/revocations"
  - "POST /api/v1/xiangwan/admin/checkin-verifications"
  - "POST /api/v1/xiangwan/admin/registrations/:registration_id/checkins"
  - "POST /api/v1/xiangwan/admin/cover-images"
  - "POST /api/v1/xiangwan/integrations/wechat-pay/notifications"
tables: []
events:
  emitted: []
  consumed: []
config:
  - "XIANGWAN_APP_ID"
  - "XIANGWAN_APP_SECRET"
  - "JWT_SIGNING_KEY"
  - "CONFIG-EXTERNAL-DOMAINS"
  - "XIANGWAN_ALLOW_MANUAL_CONTACT"
  - "XIANGWAN_MANUAL_CONTACT_POLICY_VERSION"
  - "XIANGWAN_MANUAL_CONTACT_POLICY_TEXT"
  - "XIANGWAN_PAID_REGISTRATION_ENABLED"
  - "XIANGWAN_PAYMENT_MERCHANT_ID"
  - "XIANGWAN_PAYMENT_MERCHANT_CONFIG_GENERATION_ID"
  - "XIANGWAN_CANCEL_POLICY_VERSION"
  - "XIANGWAN_PRIVACY_POLICY_VERSION"
  - "XIANGWAN_USER_AGREEMENT_VERSION"
  - "XIANGWAN_CUSTOMER_SERVICE_POLICY_VERSION"
  - "XIANGWAN_EXTERNAL_DOMAINS_POLICY_VERSION"
  - "XIANGWAN_CANCEL_FULL_REFUND_CUTOFF_HOURS"
  - "XIANGWAN_COUPON_REFUND_POLICY_VERSION"
  - "XIANGWAN_COUPON_REFUND_DISPOSITION"
  - "XIANGWAN_CHECKIN_CREDENTIAL_HMAC_KEY"
  - "XIANGWAN_CHECKIN_CREDENTIAL_TTL_SECONDS"
  - "XIANGWAN_AVATAR_MODERATION_DISABLED"
  - "XIANGWAN_WECHAT_PREPAY_ENABLED"
  - "XIANGWAN_WECHAT_PAY_API_V3_KEY"
auth:
  - "WeChat login accepts one single-use code and fixes tenant, AppID, product, role, and audience on the server"
  - "published Instance reviews are anonymous-readable"
  - "Public Policies is anonymous-readable and returns configured version identifiers, fail-closed switches, and the published manual-contact policy text required before PII entry"
  - "public media requires the exact current publication, Block, and File chain"
  - "public People reads expose only approved published profiles and never inspect trusted Principal bindings"
  - "My Registrations derives tenant from runtime config and principal from strict customer JWT authentication"
  - "Session questionnaire reads require the same live authenticated Principal and an actively submittable Session"
  - "Registration creation derives Tenant and Principal from the trusted runtime and requires a canonical UUIDv4 operation key"
  - "Payment queries derive every provider identity and amount from the authenticated owner's PostgreSQL Order"
  - "Payment notifications require the exact WeChat Pay signature, encrypted resource, configured AppID, and merchant identity; they do not use consumer authentication"
  - "Registration cancellation derives tenant and actor from the runtime and authenticated Principal; paid paths require local versioned policy decisions"
  - "My Benefits derives its owner from strict customer authentication and rechecks the active Principal inside the read-only PostgreSQL snapshot"
  - "My Host Applications reuses that owner-bound snapshot and exposes no submitted contact, narrative, or reviewer identity"
  - "Host Application submission derives Tenant and Principal from trusted runtime state and never accepts a client policy version or operation key"
  - "Identity History is rebuilt only from explicit trusted bindings, Instance roles, and the append-only contribution ledger"
  - "Series favorite target-state commands derive Tenant and Principal from runtime authentication and never accept client counter or relation facts"
  - "My Favorites derives Tenant and Principal from runtime authentication and exposes only safe Series display facts through an owner-bound cursor"
  - "Data Rights intake and history derive Tenant and Principal from runtime authentication and recheck the active Principal in PostgreSQL"
  - "Consumer Profile reads and writes derive Tenant and Principal from runtime authentication; nickname and avatar remain Auth-owned"
  - "Checkin credential issue derives its tenant and owner from the runtime and token; plaintext is returned once while PostgreSQL stores only scoped digests"
  - "On-site Checkin adapters require an independently authenticated administrator Principal and recheck the Session-scoped operator grant inside both PostgreSQL transactions"
  - "administrator auth, catalog, Registration, and Checkin routes require a customer OIDC-backed opaque session plus same-origin CSRF protection for writes"
  - "administrator questionnaire answer summaries require a tenant activity_operator grant, an Instance/Session/Registration filter, pagination, and emit only completion metadata with a PostgreSQL audit event; raw answer_values and contacts never cross the list/export boundary"
  - "single-Registration questionnaire answer detail requires an exact ID, explicit allowlisted read purpose and a live tenant activity_operator grant; the content-free audit transaction commits before raw answer values are returned, with private no-store response headers; customer visibility and retention policy still require launch sign-off"
  - "administrator audit-event list requires a live tenant super_admin grant; it returns event metadata without details JSONB and commits an audit of the read itself; later pages use the first response's as_of boundary"
  - "administrator Refund queue reads require a live tenant activity_operator grant; they return bounded status-specific routing and amount metadata, use tenant/status-bound cursors, and commit a content-free read audit before responding"
  - "administrator Refund case detail reads require the same live grant and exact case ID; they return a status/amount timeline without provider references, evidence, notes, actor IDs, or idempotency keys, and commit a content-free read audit before responding"
  - "administrator Order list reads require a live tenant activity_operator grant; they use a tenant/status-bound descending cursor and immutable amount snapshots, keep payment and Refund states separate, omit merchant/provider/Principal/contact identities, and commit a content-free read audit before responding"
  - "administrator exact-Order detail reads use the same live grant and safe projection with a separate content-free read audit; an unknown payment state never becomes a paid fact"
tests:
  unit: "go test ./internal/domains/xiangwan/api"
---

# Xiangwan Public API

`POST /api/v1/auth/wechat/login` is the only anonymous consumer-session
issuer in this composition root. It exchanges a bounded one-time code using
the customer-owned AppID and AppSecret. The shared client sends `app_id`; the
handler requires it to equal the server-owned fixed AppID instead of allowing
tenant/product selection. It converges the provider identity to one active
Principal in PostgreSQL and returns both the shared client's canonical `token`
field and the explicit `access_token` alias, restricted to the Xiangwan product
and consumer audience. Provider subjects, session keys, and signing material
are never returned or persisted by the handler.

This package is the Redis-free HTTP/application boundary for Xiangwan public
reads. Its tenant is fixed by the independently deployed single-customer
composition root, never selected by request data.

Paid WeChat endpoints bind to the configured merchant configuration
generation UUID (`XIANGWAN_PAYMENT_MERCHANT_CONFIG_GENERATION_ID`) and check
the tenant, provider, AppID, merchant ID, and allowed metadata status inside
the PostgreSQL admission transaction. This UUID is a merchant credential
generation; it is separate from the deployment/runtime generation
`XIANGWAN_GENERATION_ID`. Missing metadata, legacy NULL Order snapshots, or
any identity/status mismatch fail closed with a generic service-unavailable
response. Prepay admits only `active`; provider query and notification
convergence admit `active` or `draining`. This admission does not provide a
post-commit secret rotation or live revoke fence.

`GET /api/v1/xiangwan/public-policies` returns customer-published version
identifiers and the matching availability switches for privacy, manual
Registration contact capture, user agreement, paid self-service cancellation,
customer service, and external links. It also returns the approved
manual-contact policy text and exact version that a client must show before PII
entry and echo in a Registration snapshot. The optional `cancellation_policy`
contains versioned cancellation/refund text generated from the same configured
cutoff used by the transactional cancellation command. Registration detail grants
paid cash cancellation only after both cutoff and full-refund decisions agree;
a global public switch or a cutoff-only policy cannot grant this capability.
Coupon-funded orders remain unavailable without their own supported policy facts.
Privacy text remains accessible via
WeChat's native privacy contract. Missing configuration is represented by an
absent version and `false`; submitted contacts, customer-service details,
external-domain allowlists, Tenant, and private runtime configuration never
enter the response.

`POST /api/v1/xiangwan/me/data-rights-requests` accepts one canonical UUIDv4
operation key and an access, correction, export, or deletion scope. The runtime
binds the authenticated owner and current privacy-policy version, then commits
the case and submission event atomically in PostgreSQL. Missing policy
configuration fails closed. `GET` on the same path returns at most 100 of that
owner's cases with safe lifecycle and delivery summaries. Neither route exposes
operation keys, fingerprints, staff identities, evidence, or destinations, and
a deletion request explicitly carries no physical-deletion promise.

`GET /api/v1/xiangwan/me/profile` composes Auth-owned nickname and approved
avatar with the caller's Xiangwan-owned published extension and current pending
candidate without mirroring authorities. `PATCH` on the same path is a complete
replacement command for occupation, introduction, tags, and all visibility
choices. It requires a canonical UUIDv4 operation key and expected version;
server-owned policy and provider identity feed strict WeChat moderation.
Only a pass carrying validated provider trace/time/policy evidence advances the
PostgreSQL publication. Review, unknown, disabled, missing-identity, and
unavailable outcomes persist a retryable pending candidate; risky evidence is
persisted as a rejection and never publishes. The profile moderation worker can
append a later provider decision and atomically publish or reject that exact
candidate.
Provider subjects, file identities, decision evidence, and request fingerprints
do not cross either response.

`PATCH /api/v1/xiangwan/me/profile/nickname` is the narrow consumer command for
the Auth-owned nickname: the server derives the Principal from the session,
trims surrounding whitespace, enforces 1..64 runes without control characters,
requires the caller's `principal_profile_etag` for compare-and-swap concurrency,
and runs WeChat `msgSecCheck` before writing the principals row directly. Risky
content is rejected and an unavailable checker fails closed while preserving the
previous approved nickname. A successful response returns the refreshed ETag;
a stale ETag returns a conflict so the caller can refresh before retrying.
`POST /api/v1/xiangwan/me/avatar` stores one magic-byte-sniffed JPEG/PNG/WebP
(max 1 MiB, the WeChat img_sec_check upstream media cap) under the runtime's local storage root, repoints the Principal's
`avatar_url` to the immutable public object (clearing the central-files
backpointer), and returns its relative URL. Before anything is stored the
image passes a server-side WeChat `img_sec_check` content-security review —
a direct HTTP client can bypass the chooseAvatar component, so the gate lives
on the server. A risky verdict is refused with 「头像未通过内容安全审核，请更换图片」
and, fail-closed, an unavailable moderation service refuses with 「头像审核服务暂不可用，请稍后重试」;
nothing is stored on either outcome. The review defaults ON and is wired from
the WeChat login AppID/AppSecret; `XIANGWAN_AVATAR_MODERATION_DISABLED=true`
is the explicit local-development kill switch. The replaced avatar object is
deliberately kept: published objects answer immutable-cache reads, so stale
files are only collected by a delayed GC (upgrade-backlog).
`GET|HEAD /api/v1/xiangwan/avatars/{filename}` is the anonymous immutable read
of one server-named avatar. The login response additionally carries the
current `nickname` and `avatar_url` so the client skips one profile read; the
central executable remains the Swagger authority for the shared login path.
For legacy Auth-owned avatar URLs, the consumer profile and homepage only
project the exact principal/file binding to
`GET|HEAD /api/v1/xiangwan/avatars/legacy/{principal_id}/{file_id}`. The route
rechecks the active Principal, the exact `avatar_file_id`, file ownership,
confirmed image MIME, and the 2 MiB object limit before serving; arbitrary or
mismatched storage URLs remain untrusted and are not rewritten.

`GET /api/v1/xiangwan/me/registrations` is the authenticated PostgreSQL-only
consumer view. It derives the owner from runtime authentication, exposes the
exact Registration/Session/payment/Refund/check-in axes, and preserves the
mixed-direction owner-bound stable cursor from `booking`.

`GET /api/v1/xiangwan/me/registrations/{registration_id}` applies the same
tenant and Principal fence to one exact Registration. Its response keeps the
independent payment, Refund, and check-in facts and explains current access and
cancellation decisions without exposing an internal Principal, Tenant, or
cancellation receipt identifier.

`GET /api/v1/xiangwan/me/orders` is the matching owner-bound payment view. It
keeps pending, confirmation-unknown, paid, Coupon-settled-zero, closed, and
every Refund phase distinct, exposes payment continuation only while the PG
hold remains valid, and links every item to its exact Registration and Session.

`GET /api/v1/xiangwan/me/orders/{order_id}` applies that same Tenant/Principal
fence to one Order and returns its explainable payment, hold, and Refund outcome
without exposing provider credentials or another consumer's existence.

`GET /api/v1/xiangwan/me/coupons` is the authenticated owner-bound Coupon
wallet. It preserves the PostgreSQL reader's frozen as-of cursor and explicit
available/held/correction-required/expired/redeemed/invalidated states. The
response exposes value, validity, typed Series/activity applicability, and the
consumer's active Order/Registration links while omitting ledger entry IDs,
operator identities, source bindings, internal ranking, and policy evidence.

`GET /api/v1/xiangwan/me/favorites` is the authenticated PostgreSQL-only
Series collection. Its stable keyset cursor is scoped to the fixed Tenant and
token Principal without embedding either identity, and the response returns
only safe Series display state, current aggregate count, favorite time, a
bounded `favorite_avatars` preview of up to three active users who have a
published public ConsumerProfile, and a Series Sessions route when a public
current Instance exists. Favorite relation IDs and current Instance IDs never
cross the HTTP boundary; the preview never exposes Principal IDs or contact
data.

`GET /api/v1/xiangwan/me/benefits` is the authenticated identity and host
benefits view. One repeatable-read PostgreSQL snapshot returns only the
caller's trusted PeopleProfile binding, safe role and HostApplication
summaries, and rebuilt host-contribution history. It omits application contact
and narrative fields, binding/check-in evidence IDs, and operator identities.
Until `CONFIG-HOST-RULES` has a signed versioned source, the endpoint reports
`host_rules.state=pending`, exposes no invented benefits, and disables applying.

`GET /api/v1/xiangwan/me/host-applications` projects the same authenticated
PostgreSQL snapshot down to application status history and the current apply
capability. It never returns submitted contact or narrative fields, reviewer
identities, trusted binding evidence, roles, or contribution evidence.

`POST /api/v1/xiangwan/me/host-applications` is a business-state command over
the existing serializable PostgreSQL writer. The client provides only the four
application fields; Tenant, Principal, current application cycle, and policy
version are server-owned. Repeats return the existing active application, and
the response never echoes submitted narratives or contact. While
`CONFIG-HOST-RULES` remains unsigned, the production runtime rejects submission
with an explicit conflict instead of inventing a cycle or promised benefits.

`GET /api/v1/xiangwan/me/identity-history` is the focused identity ledger view
over the same owner-authorized PostgreSQL snapshot. It returns current and
historical Instance roles plus host contributions and an explicitly trusted
PeopleProfile identity only; public profile similarity, application content,
binding evidence, and operator identities cannot enter the response.

`GET /api/v1/xiangwan/sessions/{session_id}/questionnaire` returns only the
latest append-only assignment for that Session's current public Instance. It
requires a live consumer and an active registration CTA, then exposes the
exact immutable version, V1 field constraints, option codes, and privacy
purpose without creating a Registration or reserving capacity.

`POST /api/v1/xiangwan/sessions/{session_id}/registrations` is the authenticated
PostgreSQL-only Registration command. It requires one canonical UUIDv4
`Idempotency-Key` plus the displayed Instance publication version and price,
then rechecks the exact current hierarchy, questionnaire, answers, capacity,
and configured contact-policy version. A free Session commits its confirmed
Registration directly. An explicitly enabled paid Session atomically commits a
pending Registration, immutable Order amount snapshot, and ten-minute capacity
Hold; the fixed AppID and merchant identity come only from runtime config.
Both branches persist immutable submission evidence and only replay when the
complete normalized request fingerprint matches. Provider prepay is a separate
recoverable command and this endpoint never reports a provider payment.

`POST /api/v1/xiangwan/orders/{order_id}/wechat-prepay-attempts` is that
owner-bound OP-key command. It confirms the displayed Order version and amount,
then lets one PostgreSQL lease winner call WeChat Pay without holding database
locks. Replays return the single stored attempt. A timeout or ambiguous result
returns `payment_confirmation_pending`; only a verified ready result exposes
the five signed `wx.requestPayment` fields. AppID, merchant ID, OpenID,
`out_trade_no`, provider request IDs, and raw provider payloads are never client
controlled or projected.

`POST /api/v1/xiangwan/orders/{order_id}/payment-queries` is the provider-keyed
reconciliation command. It rejects request bodies, query parameters, and
client `Idempotency-Key` values. A short PostgreSQL Order lease throttles the
official signed merchant query; only a verified exact AppID, merchant ID,
merchant order number, amount, currency, and `SUCCESS` transaction can invoke
atomic payment confirmation. The response exposes only local payment/query
state, retry timing, and participation/refund disposition—never provider IDs,
transaction IDs, trade state, OpenID, or raw payloads. The
`retry_payment_allowed` flag is true only when this request completed a new,
verified `NOTPAY` observation on a still-pending Order. Cached `NOTPAY`,
`USERPAYING`, provider errors and unknown states never authorize reopening the
payment sheet.

`POST /api/v1/xiangwan/registrations/{registration_id}/cancellations` is the
owner-bound BUSINESS-STATE command. It accepts no request body, query, or
client idempotency key. The existing serializable PostgreSQL transaction
revokes participation and capacity immediately, closes a known-unpaid Order,
and creates the unique manual Refund case for an allowed paid cancellation.
Paid/provider-unknown and Coupon-settled branches fail closed unless the runtime
has complete versioned local policy configuration; exact replay returns the
stored result without re-evaluating current policy.

`POST /api/v1/xiangwan/registrations/{registration_id}/checkin-credentials`
is the owner-bound ephemeral issue command. It accepts no body, query, or
client idempotency key. The serializable PostgreSQL transaction revalidates
the exact confirmed Registration and published Series/Instance/Session,
revokes the previous epoch, and stores only domain-separated HMAC digests,
scope, JTI, and expiry. The QR token and backup code cross the response once;
an uncertain response must be replaced by a newly issued credential.

`POST /api/v1/xiangwan/admin/checkin-verifications` accepts one QR token or
backup code together with an exact Series/Instance/Session and a canonical
UUIDv4 operation key. It fixes Tenant and operator from trusted server state,
stores only the existing domain-separated request fingerprint and minimal
decision receipt, and returns recordable fact IDs only for `valid` or
`already_checked_in`. Rejected scans never expose the participant, matched
Registration, credential identity, or presented secret.

`POST /api/v1/xiangwan/admin/registrations/{registration_id}/checkins` consumes
the IDs from a valid verification receipt and rejects a client
`Idempotency-Key`: this is a BUSINESS-STATE command whose internal event key is
derived from the stable Registration fact. The PostgreSQL recorder again checks
the current Session-scoped operator authorization, Registration, credential,
receipt, and activity state before creating the Checkin, immutable event, and
attendance increment in one transaction. Duplicate scans by the same or a
different authorized operator converge to the current Checkin—including a
later revoked state—without a second count or event.

These administrator adapters are mounted only behind the independent Xiangwan
OIDC/opaque-session middleware. Their `PrincipalResolver` comes from that live
PostgreSQL administrator session, and both Checkin transactions recheck a
tenant activity-operator/super-admin Grant or the exact tenant/Session-scoped
`onsite_checkin` Grant. Using the consumer JWT resolver remains forbidden. With
no complete customer OIDC configuration, protected
administrator routes are absent and login reports unavailable.

The same boundary exposes Series/Instance/Session create, complete Instance
publication, masked Registration list/detail, and least-privilege Checkin
target reads. Each operator write uses a canonical UUIDv4 operation receipt
except the business-state Checkin record command, whose stable Registration
identity is its convergence key. Publication rechecks the active generation
and operator Grant in the same serializable transaction as the public snapshot
and immutable audit receipt.

`POST /api/v1/xiangwan/admin/instances` accepts the complete Instance
presentation, including an optional `issue_no` (the server allocates the next
Series-scoped number when omitted), `activity_type=custom`, quick tags, the optional cover,
and ordered text/image `detail_blocks`; the matching Session command supplies
the schedule, capacity, price, delivery and venue facts. A custom activity
uses the same Series → Instance → Session publication and registration chain as
the built-in types, so it remains visible to the same public catalog and past
activity readers. `GET /api/v1/xiangwan/admin/instances/{instance_id}/questionnaire`
reads the current version; `POST /api/v1/xiangwan/admin/instances/{instance_id}/questionnaire`
publishes an immutable per-Instance questionnaire version. The questionnaire adds
operator-defined fields to the platform-required nickname and phone inputs;
publishing a later version never mutates answers already captured by a
Registration snapshot.

`PATCH /api/v1/xiangwan/admin/instances/{instance_id}/sessions/{session_id}`
replaces the complete operational shape of one draft Session. It requires the
parent Instance version and Session version, clears fields belonging to the
other delivery mode, validates the publication shape before writing, advances
both versions in one serializable transaction, and stores an idempotent audit
receipt. Published, cancelled, ended, and archived Session facts are immutable
through this surface; changes that affect an already published Session need a
separate preview/change contract.

`POST /api/v1/xiangwan/admin/instances/{instance_id}/archives` is the explicit
archive command for a completed Instance. It changes only the lifecycle status,
requires the expected Instance version, and preserves all publication,
completion, registration, order, refund, and resource facts. The sibling
`POST /api/v1/xiangwan/admin/sessions/{session_id}/archives` command applies the
same rule to an ended Session. Cancelled Session facts remain immutable and are
not silently rewritten as archived.

`quick_tag_codes` is checked against the tenant's current published
BrandProfile inside both the create and publish transactions. An empty list is
allowed when no home vocabulary has been configured; a stale or unknown code
returns HTTP 409 with `CodeXiangwanAdminQuickTagUnavailable` (30611). The
publisher repeats this check in its final serializable transaction, so a home
configuration change cannot publish a dangling tag reference. Public home
reads retain an already-published unknown code as an opaque card value instead
of failing the entire page.

`POST /api/v1/xiangwan/admin/instances/{instance_id}/review-resources` is the
narrow operator command for adding the current video's review entry. It
accepts a title, optional description, and a deployment-allowlisted HTTPS
video-channel URL. The writer commits private Content, an exact Instance (or
Session) relation, a snapshot, a manual-review observation, and one immutable
publication in the same PostgreSQL transaction. A canonical operation key
replays the original receipt only for the identical target and body; changed
intent conflicts. The current command stores a link and text only; video-byte
upload remains a separate provider-owned Storage command.

`POST /api/v1/xiangwan/integrations/wechat-pay/notifications` is the matching
provider callback. It accepts no consumer credential, query parameter, or
client idempotency key. The bounded raw body is verified by the official
WeChat Pay API v3 SDK before AES-256-GCM decryption; only an exact
`TRANSACTION.SUCCESS` / `transaction` resource for the configured AppID and
merchant continues. The provider notification ID and raw-body digest form the
durable PostgreSQL replay key. An exact replay is acknowledged without
reapplying counters, while a changed payload under the same provider key is a
conflict. The immutable observation is inserted in the same serializable
transaction as Order, Registration, Hold, Coupon, capacity, and RefundCase
convergence. Invalid callbacks receive the provider `FAIL` envelope, and
transient business failures remain retryable.

`GET /api/v1/xiangwan/home-sessions` reads the selected immutable BrandProfile
snapshot and current published Session facts from PostgreSQL. It applies the
canonical filters, six-group ordering, publication-bound cursor, and
Asia/Shanghai time windows without creating Registration, Hold, Order, or any
other business fact. Each card also carries the Instance `cover_image_url`
('' when unset) and a `participant_avatars` stack of at most three avatars
from confirmed Registrations whose owner has a published ConsumerProfile with
at least one public field; the stack comes from one batch query keyed by the
page's Session identities, never from per-card reads. Cards additionally
return `current_favorite_users` (the Series current Favorite count) and a
separate `favorite_avatars` stack of at most three public avatars from current
Series Favorites. The Favorite stack is batch-loaded by Series identity and
never substitutes for confirmed participant avatars; homepage `heat_count`
remains `current_favorite_users + historical_registration_count`.

`GET /api/v1/xiangwan/past-activities` groups completed and archived Instance
facts by their exact Series. Each returned period retains its own Instance ID
and title, so the Mini Program can open the matching review and the operator
can update one period's cover/detail blocks without refreshing sibling covers.

`GET /api/v1/xiangwan/instances/{instance_id}/review` also returns the
published Instance `detail_blocks` as `content_blocks`. The reader applies the
same completed/archived, published, and as-of lifecycle predicates as the past
activities catalog, then projects only valid text/image blocks; drafts,
unpublished or cancelled facts and malformed stored elements do not cross the
anonymous boundary. The Mini Program joins relative cover paths against its
configured API origin and drops non-local media paths before rendering.

`POST /api/v1/xiangwan/admin/cover-images` stores one JPEG/PNG/WebP cover
(max 5 MiB, magic-byte sniffed) under the runtime's local storage root and
returns the public relative URL; the administrator surface stores it as the
Instance `cover_image_url` either as-is (relative path — the form dev http
origins use) or composed with the site origin into an absolute https URL, and
both forms pass the migration 787 CHECK constraint. Uploads require an
activity-operator Grant. A successful upload also sweeps the cover directory
best-effort in two phases: objects older than 24h whose filename no Instance
references (`cover_image_url` or `detail_blocks`) are first marked with the
moment that reference was lost (`xiangwan_cover_gc_markers`, migration 788),
and only deleted once the mark is older than the 7-day retention window — a
just-replaced cover survives while clients may still hold its immutable
cached URL. A cover that becomes referenced again clears its mark, every
sweep failure is logged without failing the upload, and without the marker
store nothing is ever deleted on upload age alone. `GET|HEAD
/api/v1/xiangwan/covers/{filename}` is the anonymous immutable read of one
server-named cover object.

`PATCH /api/v1/xiangwan/admin/instances/{instance_id}` is the ADMIN OP-KEY
command that patches only the presentational Instance fields: `cover_image_url`
(`''` or the public relative `/api/v1/xiangwan/covers/<32hex>.<ext>` path or an
absolute https URL — both stored forms pass the migration 787 CHECK; image
blocks inside `detail_blocks` accept the same relative-or-https choice) and
`detail_blocks` (a full-list replace; each element must be a valid text or
image block or the whole command is rejected with 400). Every other field is
immutable here. The concurrency fence is the dedicated
`expected_presentation_revision` (returned on every admin Instance response as
`presentation_revision`), not the operational `version`: public instance_review
resources pin `expected_target_version = instance.version`, so the revision
advances only when `cover_image_url` or `detail_blocks` actually change and the
operational version never moves — a cosmetic edit on a published/completed
Instance leaves its published review documents valid. A byte-identical PATCH is
a successful no-op. The command runs in the same serializable operation/audit
pattern as publish and completion: an exact replay returns the original
response, a changed intent under the same key conflicts, and rejections are
audited.

`GET /api/v1/xiangwan/admin/instances/{instance_id}/review-status` is a
read-only, grant-bound projection of the existing public review/resource
reader. It rechecks the exact Instance and Session hierarchy in one authorized
PostgreSQL read transaction and runs the same public review projection before
reporting whether the review is eligible/available, reader-visible document
counts, and the latest publication time. It never returns Content blocks,
storage keys, raw external URLs, or restricted-registration resource facts; the
public review and media routes remain the only body/byte readers.

`POST /api/v1/xiangwan/admin/instances/{instance_id}/cancellation-previews`
creates a short-lived, immutable impact snapshot for taking a published
Instance offline. It requires an administrator OP-KEY and a reason, but makes
no activity, Session, Registration, Hold, Order, Refund, or Coupon change. The
preview records the expected Instance version and the exact Session/payment
facts that the final command must still match. Reusing the same key and
request returns the same preview; changing the actor, reason, or target under
that key is a conflict.

`POST /api/v1/xiangwan/admin/instances/{instance_id}/cancellation` consumes
that preview with a second administrator OP-KEY and the returned
`expected_instance_version`. One serializable PostgreSQL transaction locks the
current administrator identity and activity Grant, then locks the activity
hierarchy, rechecks the preview snapshot and all open Registrations, closes
pending Orders and Holds, creates the required Refund cases, and applies
the configured versioned Coupon restore/forfeit policy for settled-zero Orders,
cancels every targeted Session and Registration, and records an aggregate
receipt. The Instance version advances exactly once. A replay with the same
key and intent returns the stored receipt; an expired/consumed preview, stale
version, changed payment fact, or changed intent returns a conflict without
partial writes. The runtime must inject
`XIANGWAN_COUPON_REFUND_POLICY_VERSION` plus
`XIANGWAN_COUPON_REFUND_DISPOSITION` before Coupon-backed cancellation is
allowed; an absent policy fails closed with a conflict response.

`GET /api/v1/xiangwan/people` and
`GET /api/v1/xiangwan/people/{people_id}` expose only moderated, published
PeopleProfile content. The list uses a tenant-bound frozen PostgreSQL keyset;
the detail remains valid when no trusted Principal binding exists. Neither
route reads or projects binding evidence, Principal IDs, moderation operators,
or content-operator identities.

`GET /api/v1/xiangwan/sessions/{session_id}` binds every time, price,
capacity, delivery, map, display, and CTA field to that exact public Session.
It never returns sibling Sessions or a selector, never exposes a private online
link, and disables new-Registration CTA semantics while the BrandProfile is
suspended. The response preserves the immutable `quick_tag_codes` and also
returns the published `quick_tags` vocabulary (`code` plus human label);
unknown codes remain visible to clients as stable codes. Three content fields
enrich the same page. `content_blocks` is the
Instance's administrator-written detail blocks (`[]` when unset): each element
is either `{type:"text",title,body}` or `{type:"image",url,caption}` (caption
possibly empty), where image `url` is the public `/api/v1/xiangwan/covers/...`
path or an absolute https URL; the stored JSONB is projected server-side and
invalid elements never leave the read boundary. `leaders` (`[]` when none)
projects each active Instance role binding through its trusted, published, and
approved PeopleProfile — `{display_name, role_label, headline, avatar_url}`
with `avatar_url` empty when the Principal has none, ordered host, invited
guest, course instructor, then event speaker; the Chinese `role_label` matches
the Mini Program my-benefits vocabulary exactly, and the stack is capped at
eight leaders (the SQL pre-pass reads one spare row so an invalid or duplicate
binding cannot starve the cap). `previous_review` is `null` unless a strictly earlier completed/archived
sibling Instance has a published review. A published review carries
`{instance_id, title, completed_at, image_urls}`; `image_urls` may be empty
when the review contains only a video-channel link or text. When images exist,
at most three product-local media paths are returned and the underlying image
scan is bounded to the first 32 ordered rows. All three come from one bounded
query each — never per-row reads.

`GET /api/v1/xiangwan/series/{series_id}/sessions` resolves only the Series'
current public Instance; `GET /api/v1/xiangwan/instances/{instance_id}/sessions`
resolves the exact requested Instance. Both return zero, one, or many public
Sessions in stable chronological order. One Session returns a direct detail
target; multiple Sessions return an explicit ordered choice and never invent a
default.

`PUT|DELETE /api/v1/xiangwan/series/{series_id}/favorite` applies the desired
owner state through one serializable PostgreSQL transaction. The client cannot
supply a relation ID, counter, version, Tenant, Principal, body, or operation
key. A repeated target state is successful without moving the Series counter;
new favorites require an active, home-visible Series with a current public
Instance.

`GET /api/v1/xiangwan/instances/{instance_id}/review` resolves the Series from
the public PostgreSQL hierarchy. An optional canonical `session_id` adds only
that exact ended Session's resources; no Session is guessed. The response
contains allowlisted review modules and the canonical next-Instance
zero/one/many route. It never exposes shared Content IDs, storage keys, legacy
CDN URLs, private relations, or unapproved Blocks. Available file-backed Blocks
carry a product-local `media_path` bound to the relation, Block, and File IDs.

`GET|HEAD /api/v1/xiangwan/media/{relation_id}/{block_id}/{file_id}` rechecks
that complete publication chain on every request before opening the confirmed
object through Storage's Redis-free provider seam. It streams bytes with range
support, serves generic files as attachments, and returns opaque public errors.

`POST /api/v1/xiangwan/admin/coupon-corrections/{entry_id}/actions` records
manual start/resolution with a UUIDv4 Idempotency-Key and exact handling version.
It derives actor and identity from the session and parses adjustment cents from
a decimal string. A distinct super administrator verifies external financial or
invalidated-ledger evidence; responses omit evidence and notes and are no-store.

## Owner questionnaire prefill

Authenticated `GET /me/questionnaire-prefill/:session_id` rechecks the current
Session registration eligibility and returns only the caller's reusable answers,
mapped to current field IDs. Same-Series immutable submissions must have exactly
matching field definitions, order, options, constraints, purpose and privacy
version. Independent Instance IDs do not prevent reuse. Source payment or
cancellation status does not change answer ownership; this read grants no old
participation/resource access. Inactive/deleted owners and non-rejected deletion
requests for participation/all data disable reuse. The no-store response has no
contacts or source registration IDs and accepts no client owner or tenant.
Current consent and a new independently validated Registration snapshot remain
required.
