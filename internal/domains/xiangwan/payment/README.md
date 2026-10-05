# Xiangwan payment

This package owns immutable Order price/payment snapshots and ten-minute
PostgreSQL capacity holds for paid registrations.

- Payment state is never inferred from participation, refund, or check-in state.
- Provider confirmation is accepted only as a trusted server-side fact and must
  exactly match the stored payable amount.
- A hold is active on the half-open interval `[created_at, expires_at)` and has
  exactly one terminal outcome.
- Order, Registration, hold, and Session counters must be changed in one
  serializable PostgreSQL transaction. Redis is not a coordination dependency.
- A payment confirmed after an unpaid close remains a recorded paid fact; the
  application must initiate a separate refund workflow.

The `postgres` adapter persists both fact streams, resolves commands by
tenant-scoped idempotency or merchant identity, exposes explicit row locks for
transaction coordinators, and rejects stale writes with optimistic versions.

`postgres.PaidRegistrar` starts a paid Registration under stable
Series → Instance → Session locks. It writes the pending Registration, Order,
hold, and Session hold counter in one serializable transaction; an idempotent
replay returns the original deadline and never reserves capacity twice.
The public Registration path also applies the active Brand/current Instance
fence and stores the confirmed publication/price, contact, questionnaire,
answers, and request fingerprint in that transaction. Paid runtime configuration
is disabled by default; AppID, merchant identity, amount, and merchant order
number are server-owned and never accepted from the client.
When a Coupon is selected, the registrar locks its ledger and derives the
discount from the server-owned face value. A full-price Coupon settles the
Order as `settled_zero`, confirms participation, converts capacity, and redeems
the Coupon locally without creating a provider payment fact.

`PrepayService` and `postgres.PrepayAttemptStore` create one recoverable WeChat
mini-program prepay attempt per Order. A serializable PostgreSQL acquisition
locks and rechecks the active Runtime deployment generation, configured
merchant-configuration generation metadata, owner, published activity hierarchy,
pending Registration/Order, active hold, amount, fixed AppID/merchant identity,
and app-scoped OpenID. It commits a 30-second single-winner lease before any
network call. Completion uses a fresh transaction, records an immutable safe
provider observation, and converges only to `ready` or `unknown`; a timeout is
never interpreted as unpaid or paid. An ambiguous completion also advances the
Order to `unknown`, preventing a second payment path until reconciliation. The
official WeChat Pay API v3 adapter
returns signed `wx.requestPayment` parameters and stores neither raw response
bodies nor OpenID.

`PaymentQueryService` performs the matching merchant-order query. The
client supplies only its owned Order ID: PostgreSQL resolves AppID, merchant
ID, `out_trade_no`, and exact amount, admits only the configured merchant
generation in `active` or `draining` status, then grants a 15-second
single-winner lease without holding a transaction across the provider call.
Verified
responses become append-only transaction observations. `SUCCESS` is passed to
`postgres.PaymentConfirmer`, which writes that observation in the same
serializable transaction as the Order, Registration, Hold, counters, Coupon,
and any required RefundCase. If trusted convergence of a verified response
fails, the response is still recorded as an observation while the query lease
is released to `unknown` with an explicit failure class. `NOTPAY`,
`USERPAYING`, `REFUND`, and ambiguous responses remain retryable behind a
PostgreSQL cooldown. Authoritative
`CLOSED`, `REVOKED`, and `PAYERROR` responses instead close the unpaid Order,
cancel the pending Registration, and release its hold/capacity/Coupon in one
PostgreSQL transaction; owner-fenced completion then finalizes the query lease.
WeChat's verified `NOTPAY`/`CLOSED` query responses may omit optional trade type
and amount metadata. The mini-program query adapter uses the immutable Order's
expected JSAPI type, amount and currency for absent metadata, while checking
every supplied identity and value. It never synthesizes a paid transaction;
`SUCCESS`, `REFUND` and signed notifications retain the strict field checks.
A definitive first prepay rejection (such as `NO_AUTH`) closes the local
Order through the same atomic participation/hold/capacity/Coupon transaction.
The closure rechecks the original attempt's version, immutable rejection,
absence of prepay credentials and transaction observations, and merchant
snapshot. A takeover or ambiguous failure never qualifies. A later
`ORDER_NOT_EXIST` query can recover legacy Orders only with that same
proof; absence alone cannot close an unknown payment. No provider terminal
transaction is invented. New rejection responses return an actionable 409.
Client `requestPayment` success never writes paid. A later trusted `SUCCESS` is
still recorded and creates the unique refund case without restoring
participation.

The public query result permits a client to replay its existing prepay attempt
only when that request freshly verifies `NOTPAY` on a still-pending Order.
Cached `NOTPAY` and generic `pending` leases cannot authorize a new payment
sheet: the latter may also mean `USERPAYING`.

Every Order that reaches `closed_unpaid` after a provider prepay attempt also
creates one durable `xiangwan_payment_close_jobs` record. `PaymentCloseService`
and `postgres.PaymentCloseJobStore` consume those records with PostgreSQL
single-winner leases and no transaction held across WeChat calls. Each attempt
queries the merchant Order first: `SUCCESS` converges through the existing
payment confirmer, authoritative unpaid terminal states finish locally, and
only `NOTPAY` permits a close-order call. `USERPAYING` and ambiguous failures
retry. If a verified `SUCCESS` or terminal response cannot be converged locally,
the observation and failure class are stored while the close job returns to
retry, instead of leaving its lease in progress. `REFUND` is retained for
manual review. This makes hold expiry and other
local unpaid closure recoverable without Redis, while a late trusted payment
still follows the unique Refund-case path.

The close worker binds its confirmer to the configured merchant-configuration
generation for Orders carrying the immutable 791 snapshot. It keeps a separate
legacy drain boundary for pre-791 NULL snapshots so queued close jobs can finish
without treating NULL as a new API admission path. New HTTP/API convergence
remains strict and rejects a NULL or mismatched snapshot.

The payment-notification path complements active queries. The official SDK
checks the current WeChat Pay public-key ID and RSA signature over the untouched
request bytes, enforces its timestamp window, and decrypts the resource with
the customer API v3 key. Only the normalized success transaction reaches the
domain. Its provider notification ID plus raw-body SHA-256 is recorded as a
`payment_notification` transaction observation. PostgreSQL accepts an exact
replay but rejects the same provider key with any changed tenant, Order,
Principal, transaction, amount, time, or digest. That observation and all
payment/participation/refund effects commit atomically; neither raw payloads nor
Redis are stored or used.

The provider contract was rechecked on 2026-09-14 against WeChat Pay's
[mini-program payment flow](https://pay.wechatpay.cn/doc/v3/merchant/4012791911),
[JSAPI/mini-program prepay API](https://pay.wechatpay.cn/doc/v3/merchant/4012791897),
[merchant-order query API](https://pay.wechatpay.cn/doc/v3/merchant/4012791900),
[merchant-order close API](https://pay.wechatpay.cn/doc/v3/merchant/4012791860),
[payment-success notification API](https://pay.wechatpay.cn/doc/v3/merchant/4012791902),
and the official
[Go SDK](https://github.com/wechatpay-apiv3/wechatpay-go). The local ten-minute
capacity hold remains stricter than the provider prepay token lifetime.

`postgres.PaymentConfirmer` consumes only a trusted provider fact. It records
the exact payment, converts a valid hold into confirmed participation and
updates Session/Series projections atomically. Late or unavailable
participation is retained as paid and returned as `refund_required`; that
result always carries the single durable, idempotent manual Refund case created
in the same PostgreSQL transaction. A successful confirmation redeems the held
Coupon in that transaction; a confirmation that cannot retain participation
releases a Coupon that has not already been redeemed.

`postgres.CapacityHoldExpirer` performs bounded PostgreSQL sweeps. Each due
hold is expired with its pending Registration, unpaid Order, and Session
counter in one transaction; an `unknown` provider outcome is preserved for
later reconciliation. The expirer rejects a nil/zero-value transaction starter,
clock, or context before opening a transaction. Expiry and explicit
pending-registration cancellation also release the selected Coupon atomically.
The runtime worker/cron wiring and unknown-to-reconcile queue remain separate
follow-up contracts.

Notification convergence uses the same tenant/provider/AppID/merchant ID and
merchant-configuration generation admission as query (`active` or `draining`).
Legacy NULL Order snapshots and revoked, missing, or mismatched metadata fail
closed. The admission lock is held only for the database transaction; the
provider call happens before the later convergence transaction, so this package
does not claim a live status, secret, or rotation TOCTOU fence. Exact replay
after a configuration is revoked remains an operational retry boundary for a
future replay-specific package.
