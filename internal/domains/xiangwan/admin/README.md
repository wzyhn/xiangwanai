# Xiangwan administrator boundary

This package owns the customer operator identity and authorization contracts
for the independently deployed Xiangwan Runtime. It is deliberately separate
from mini-program consumer JWTs and the platform governance API.

## Flow

1. `oidc.Client` performs Authorization Code + PKCE discovery, redirect and ID
   token verification. The provider access token is never persisted.
2. `postgres.SessionStore` consumes a five-minute encrypted login attempt,
   resolves an exact tenant/issuer/subject identity link, loads active Grants,
   and creates a hashed opaque session plus CSRF token in PostgreSQL.
3. HTTP middleware checks Secure session Cookie, trusted Origin/Referer and
   double-submit CSRF before protected routes.
4. Catalog and Checkin adapters recheck Principal, active identity link,
   tenant/Session Grant, active deployment generation and resource version at
   their PostgreSQL transaction boundary.

Capabilities are `super_admin`, `activity_operator`, `onsite_checkin`, and
tenant-scoped `finance` (migration 795).
Tenant activity operators can run both activity and Checkin operations; only
`onsite_checkin` can be Session scoped. Registration lists and the default
detail projection mask contact name/phone and never return Principal identity
or questionnaire answers. An activity operator may explicitly request a
purpose-bound contact reveal; the purpose and allow/deny result are audited.
An exact-Registration questionnaire answer read requires a separate explicit
purpose and commits a content-free audit event before returning values.
Onsite target reads contain only activity labels, schedule, venue and
aggregate counters visible within the operator's exact scope.
The audit-event list is restricted to a live tenant `super_admin` Grant,
omits event `details`, and audits each read before returning the page.
The Refund queue list requires a live tenant `finance` or `super_admin` Grant. It
returns only case routing, status, reason, and amount metadata from the
tenant/status-bound manual queue and commits a content-free read audit before
responding. An exact-case detail view adds only status/amount event metadata,
with a separate read audit; it omits provider references, evidence, notes,
actor identities, and idempotency keys. Neither read performs a financial
transition. `POST /refund-cases/:case_id/actions` records manual transitions
through the Refund processor: finance can start processing or record a channel
failure, while a tenant super administrator can reject with a reason or confirm
completed external repayment with exact cents, official refund ID and reference.
Completion requires a distinct prior processing actor. The live generation,
exact active identity and action-specific Grant are locked in the same
serializable transaction as the immutable Refund event. No provider refund
call is made by this endpoint; see
[`refund-sop.md`](../../../../apps/xiangwan-admin-web/docs/refund-sop.md).
The Order list accepts a live tenant `activity_operator`, `finance`, or
`super_admin` Grant. It uses an indexed tenant-wide keyset and a separate
read audit. It exposes immutable
price amounts and independent payment/Refund status without merchant or
provider identifiers, contact data, or consumer Principal IDs.
An exact-Order detail endpoint rechecks the same grant and audits its read
separately; both surfaces keep unknown payment distinct from confirmed paid.

Single-session cancellation uses `POST /sessions/:session_id/cancellation-previews`
with `{}` and a separate `POST /sessions/:session_id/cancellation` containing
the exact preview, Session version and required reason. Only a tenant activity
operator or super administrator can perform either command. Live generation,
Principal, exact identity link and Grant are rechecked inside the Serializable
fact transaction. Preview creation and final cancellation each persist their
operation receipt and audit atomically. Final confirmation checks the full
participation/payment/refund/coupon snapshot; siblings and the parent Instance
remain unchanged. A revoked Grant or stale generation also denies receipt
replay. Notifications require explicit manual follow-up; this does not send
messages or call an external refund provider.

Super administrators can read `GET /registrations/:registration_id/checkin`
and submit `POST /registrations/:registration_id/checkin-revocations` with
the exact Checkin, expected version and required reason. Immutable scope is
resolved server-side; the client cannot supply actor, tenant or hierarchy.
The serializable revocation transaction locks generation before identity and
Grant, including receipt replay. It retains the original attendance and
appends one revocation event, attendance decrement, correction outbox task,
operator receipt and audit. Migration 827 permits a cancelled Session's
attendance decrement only when immutable revocation/outbox evidence exists;
content changes, count increases and revival remain rejected. Contribution
and Coupon corrections are composed in the dedicated reconciliation worker,
whose customer deployment must still be verified;
the endpoint does not claim those downstream effects have completed.

Super administrators can create a 24-hour People binding invitation only for
an exact approved public profile version, with required reason and OP-KEY.
Reissuing revokes the prior unused invitation; explicit invitation withdrawal
also retains history. Codes are purpose-separated HMAC outputs, held only in
browser memory and returned through no-store responses. Receipts and audits
retain no plaintext; a key rotation prevents regenerating an old pending code.
The authenticated Mini consumer privately previews the code, then explicitly
confirms the same profile version. The serializable transaction rechecks live
inviter authority, owner, generation, profile, expiry and one-use state before
creating the evidence-backed binding. Neither side accepts a caller-selected
consumer Principal or derives identity from a phone number. Exact retries
return the retained binding status, including revoked; they never reactivate it.
Binding withdrawal is separately reasoned, versioned and audited, preserving
original roles and historical ledger facts.

`POST /instances/:source_id/copies` creates a separate draft under the source
Series. The source's Instance and presentation versions, the parent Series
version, the live activity Grant and the operation key are checked in the same
write transaction. Migration 796 records an immutable source link; the target
owns independent editable fields and receives no Session, questionnaire,
Registration, Order, Checkin, cancellation, publication or review facts.
Published review photo curation uses an exact relation and activity-operator
identity/Grant. The list and detail reads audit access to original photos,
including hidden ones; the append-only write requires an operation key and
current curation version. This changes only presentation, while Content,
moderation and publication facts stay immutable.
The Coupon correction list is a tenant-super-admin-only, audited read of
append-only `correction_required` markers after a revoked Checkin. It returns
Coupon, Checkin-event and related Order/Registration routing IDs without
reason text, consumer contact or private evidence. An `as_of` boundary keeps
later pages and handling status stable. Migration 821 and
`POST /coupon-corrections/:entry_id/actions` add append-only start/resolution
events. A distinct live super administrator must verify full-face-value external
financial evidence for redeemed Coupons, or an already-invalidated ledger for
never-redeemed Coupons. Exact identity, generation, version, ledger, receipt and
audit are checked in one serializable transaction. Neither read nor write changes
original Coupon or Order facts; see [the SOP](../../../../apps/xiangwan-admin-web/docs/coupon-correction-sop.md).
The separate optional signed-policy and manual five-Coupon
replenishment routes do not resolve an already held or redeemed correction.
With a customer Ed25519 public key configured, the optional
`POST /coupon-grant-policies` route records a future-effective signed canonical
policy version. It rechecks the exact live super-admin identity, Grant and
runtime generation in the same transaction as the immutable policy row and
audit. A policy receipt is not a Coupon issuance receipt; the automatic grant
worker and manual replenishment endpoint are separate optional write paths.

Review-media upload intents (migration 799) are activity-operator-only and
bind a UUIDv4 operation/File ID to an exact target, actor, kind, MIME, byte
count and SHA-256. The same-origin binary route selects immutable local bytes;
confirmation re-inspects them and changes only the private File lifecycle.
An optional private, owner- and target-scoped review download rechecks the
confirmed File lifecycle and complete object checksum, audits the access
attempt and successful open separately, and serves a no-store, sandboxed
attachment. A separate optional Resource writer requires that opened evidence
plus explicit manual review, then pins the exact File in the same transaction
as Content and publication. The persisted filename is derived only from the
File ID and allowlisted MIME. Customer byte-content review policy and the
production route composition remain open, so the production Runtime does not
mount these routes.

When the complete OIDC configuration is absent, the runtime does not mount
protected administrator routes. There is no password, bootstrap HTTP endpoint,
development login, localStorage token, Redis session or fail-open fallback.
