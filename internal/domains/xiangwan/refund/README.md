# Xiangwan refund

Refund is an independent state axis. A paid Order remains paid after
participation is cancelled; a single durable Refund case records how the funds
will be handled.

- V1 starts in `pending_manual` and never calls automatic refund APIs.
- Requested and successful values use integer cents and cannot exceed the
  trusted Order payment fact.
- `refunded` and `rejected` are terminal, immutable outcomes.
- A failed manual attempt can be started again without restoring Registration
  participation or Session capacity.
- PostgreSQL is the only coordination and persistence dependency.

The `postgres` repository creates, reads, locks, and advances Refund cases with
tenant-scoped queries and optimistic versions. It accepts both database and
transaction handles so a case can be committed atomically with the payment or
cancellation fact that requires it.

Migration 706 and `postgres.Processor` add the finance-operation boundary. A
state-changing processing, completion, failure, or rejection command updates
the current case and appends one immutable, tenant-scoped event in the same
serializable transaction. Reusing an operation key returns the original event;
changing its payload is a conflict. Completion locks the paid Order first and
requires the exact requested amount plus an external refund identifier or
evidence reference. The service records manual evidence only and never calls a
refund provider while holding database locks.

The administrator action endpoint wraps that processor with a live, exact
identity/Grant and active-generation check inside the same serializable write
transaction. It requires a current case version, allows finance to start or
record failure only after processing, and requires a separate super administrator
to confirm a completed external refund. Completion requires both the official
refund ID and a reference to the channel record; identical operation keys replay
the recorded event without changing its timestamp. The case is located inside
the authorized transaction before the paid Order and case are locked, so even
a one-connection database pool cannot deadlock on a second checkout.

Migration 720 composes full cash-refund completion with a redeemed Coupon. The
processor obtains `CONFIG-COUPON-REFUND-POLICY` only through an injected local
or PostgreSQL-backed evaluator, then appends exactly one `restored` or
`forfeited` Coupon entry carrying the signed policy version in the same
transaction as the Refund completion event. Missing or malformed policy fails
closed, released Coupons need no second adjustment, and exact replay reads the
persisted decision instead of re-evaluating current configuration.

Official behavior was rechecked on 2026-09-12 against WeChat Pay's
[refund query documentation](https://pay.wechatpay.cn/doc/v3/merchant/4012791904):
provider `PROCESSING` is not treated as success, and a completed observation
retains the provider refund identity, amount, success time, and evidence.

`Repository.ListQueue` exposes bounded, status-specific operator tabs for
`pending_manual`, `processing`, and `failed` cases. Every query is tenant scoped,
uses the existing PostgreSQL manual-queue index, joins the exact Series /
Instance / Session labels, and advances with an opaque cursor bound to both
tenant and status. `Repository.GetCaseDetail` returns the current case and its
event timeline capped at that case version, so a concurrent transition cannot
produce a timeline newer than the returned projection.

Cancelling a Session creates or reuses every required paid-participation Refund
case inside the same PostgreSQL transaction that revokes participation and
capacity. Each case remains on the normal manual queue and is identified by its
paid Order; ambiguous payment outcomes are resolved later by the trusted payment
confirmation path and never restore the cancelled Session.

Whole-Instance cancellation uses the same manual queue with the distinct
`instance_cancelled` reason, including payments confirmed after cancellation.
