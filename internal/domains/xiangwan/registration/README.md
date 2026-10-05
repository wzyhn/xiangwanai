# Xiangwan Registration

This package owns Session-bound participation eligibility. A Registration always
stores tenant, Series, Instance, Session, and principal identities; callers may
not infer or default the Session.

`pending_payment`, `confirmed`, and `cancelled` describe participation only.
Order/payment, refund, questionnaire, and check-in facts remain separate axes.
Cancellation is terminal, while a later re-registration creates a new historical
record with a new idempotency key.

Migration 703 enforces hierarchy ownership, immutable identities, irreversible
cancellation, durable command idempotency, and at most one pending/confirmed
Registration per principal and Session. These guarantees are PostgreSQL-only and
do not use Redis locks.

`registration/postgres.Repository` persists and retrieves the complete
participation fact, supports replay by tenant-scoped idempotency key, finds the
single open principal/Session record, and updates lifecycle state with optimistic
version checks.

`registration/postgres.FreeRegistrar` serializes Series, Instance, and Session
locks, rechecks the canonical display state, confirms only zero-price Sessions,
and increments confirmed plus historical counts in the same PostgreSQL
transaction. A replayed idempotency key never increments either count again.

Migration 730 adds the immutable consumer-submission receipt used by the public
free and paid Registration command; migration 780 extends it with the exact
public privacy-policy version acknowledged by the consumer. The same
serializable transaction snapshots the consumer-confirmed and current Instance
publication, Session price/version, public privacy policy, explicitly enabled
manual contact policy, exact published questionnaire and questionnaire-privacy
version, every field contract, every normalized answer, and a stable request
fingerprint. Database constraints and triggers reject stale field snapshots and
any later mutation;
an idempotent replay must match the original fingerprint before it can return
the stored Registration.

`registration/postgres.RegistrationCanceller` follows the same aggregate lock
order for free, pending-payment, unknown-payment, and paid registrations. It
revokes participation and releases capacity immediately, closes a still-pending
Order, and creates or reuses the manual Refund case for a confirmed payment in
one serializable transaction. Every self-service request, including a free
Registration, rechecks the Session-specific cancellation cutoff after acquiring
the aggregate locks. The detail read and command use the same deterministic
policy evaluator; this prevents the UI decision from bypassing the configured
cutoff and closes the time-of-check/time-of-use window. Paid or
payment-ambiguous requests additionally fail closed unless that evaluator
returns an allowed, full-refund decision with a version that is snapshotted in
the cancellation fact. The operator source is a separate adapter boundary and
must be authorized before invoking the service.

The consumer HTTP adapter exposes that command at
`POST /api/v1/xiangwan/registrations/{registration_id}/cancellations`. It binds
Tenant and actor from the independent runtime, accepts no body or client
idempotency key, and returns only the stored participation, local Order,
manual-Refund, or Coupon-adjustment summary. A repeat cannot re-evaluate or
replace the policy version committed by the first cancellation.

Migration 721 adds the zero-settlement branch: a confirmed `settled_zero`
Registration never creates a cash Refund. Instead, cancellation requires the
versioned Coupon refund policy and appends one `restored` or `forfeited` entry
to the locked PostgreSQL ledger in the same transaction. Exact replay returns
that stored adjustment without evaluating current policy again.

`activity/postgres.SessionCanceller` extends the same invariants to one complete
Session. It cancels every open Registration, releases confirmed or held
capacity, closes only known-unpaid Orders, and creates required manual Refund
cases atomically. It also applies the same Coupon policy to every zero-settled
Registration and records the adjustment count in the preview and receipt.
Registration remains the authorization fact, so cancellation also invalidates
questionnaire and participation access without a Redis cache.

`activity/postgres.InstanceCanceller` applies the same convergence to every
published Session in one Instance under one serializable PostgreSQL transaction;
Coupon adjustments and aggregate receipt counts converge in that transaction,
so there is no state in which only part of the Instance remains participable.
