# Xiangwan Check-in

This package owns the durable attendance fact for one exact confirmed
Registration. A Checkin records the tenant, Series, Instance, Session,
Registration, participant, and authorized operator that produced it; it never
uses a phone number, OpenID, payment identity, or database primary key as a
presented credential.

The lifecycle has two states: `checked_in` and terminal `revoked`. Creation and
revocation each produce one immutable, idempotent event. A duplicate scan must
return the existing Checkin rather than incrementing attendance or producing a
second event.

The PostgreSQL adapter is the only persistence and coordination boundary.
Migration 710 enforces one Checkin per Registration, a current confirmed
Registration at creation time, optimistic versioning, immutable identity,
terminal revocation, and an append-only event timeline. Redis is not used.

Migration 711 adds the credential boundary. `CredentialProtector` emits a
short-lived opaque QR token and a human-enterable backup code exactly once;
PostgreSQL receives only domain-separated HMAC-SHA-256 digests, a public JTI,
scope, epoch, and expiry. The public JTI is deliberately distinct from the
credential table primary key. Rotation revokes the previous row before a new
epoch becomes active.

`postgres.CredentialIssuer` is the consumer `EPHEMERAL-ISSUE` boundary. It
accepts only the owning principal's current confirmed Registration, locks and
revalidates the exact active Series and published Instance/Session, bounds the
short expiry by the Session end, terminally revokes the previous epoch, and
persists the next digest-only credential in one serializable transaction. QR
and backup plaintext are returned once and redact themselves from normal Go
formatting; an uncertain network result must request a fresh credential.

The authenticated
`POST /api/v1/xiangwan/registrations/{registration_id}/checkin-credentials`
adapter exposes that issue boundary. Tenant, Principal, TTL, and HMAC key are
server-owned; the request accepts no body, query, or idempotency key. Its safe
response omits credential row IDs, digests, ownership identifiers, and
revocation evidence.

Verification attempts store only the requested Series/Instance/Session,
operator, stable decision code, and matched fact identities when one exists.
They never store the QR token, backup code, phone, OpenID, or payment identity.

Migration 713 adds a keyed, domain-separated request fingerprint so an
idempotency key can replay only the exact credential presentation without
persisting a reusable secret. `postgres.Verifier` authorizes the on-site
operator and revalidates the current target, Registration, credential, and
existing Checkin in one serializable PostgreSQL transaction before appending
that immutable minimal receipt.

`postgres.Recorder` is the write boundary for a successful on-site scan. It
requires an injected current-operator authorizer, then revalidates the exact
active Series, published Instance/Session, confirmed Registration, immutable
verification receipt, and active unexpired credential inside one serializable
PostgreSQL transaction. The same transaction creates the Checkin and event and
increments the Session attendance counter. The API derives the creation-event
key from the stable Registration business fact instead of accepting a second
client operation identity. Repeated scans by any authorized operator return the
current fact, including a later terminal revocation, without another event or
counter change; conflicting targets fail closed.

`api.OnsiteCheckinHandler` and `api.OnsiteCheckinService` expose the two-step
operator contract without weakening those database guarantees:
`POST /api/v1/xiangwan/admin/checkin-verifications` creates the minimal decision
receipt, then
`POST /api/v1/xiangwan/admin/registrations/{registration_id}/checkins` consumes
that receipt and records attendance. The adapters never return the presented
secret or participant identity and must only be mounted behind the separate
administrator session/Grant resolver; the consumer JWT resolver is not an
operator authentication mechanism.

Migration 712 adds the durable correction outbox. `postgres.Revoker` requires
a current super-admin authorization, exact target identity, expected Checkin
version, non-empty reason, and business idempotency key. It permits convergence
even after the activity is cancelled or archived, but commits the terminal
Checkin, immutable event, attendance decrement, and one correction task only as
a single PostgreSQL transaction. Downstream contribution and coupon support
must consume that task by appending reversals rather than editing history.
