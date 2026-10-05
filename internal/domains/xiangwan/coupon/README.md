---
layer: L3
capability: xiangwan-coupon
status: implemented
owner: "@platform"
same_layer_dependencies:
  - xiangwan-activity
  - xiangwan-checkin
  - xiangwan-people
tables:
  - "xiangwan_coupons"
  - "xiangwan_coupon_entries"
  - "xiangwan_coupon_grant_policy_versions"
events:
  emitted: []
  consumed:
    - "xiangwan_checkin_events"
config:
  - "CONFIG-COUPON-FACE"
  - "CONFIG-COUPON-EXPIRY"
  - "CONFIG-COUPON-SCOPE"
  - "CONFIG-COUPON-REFUND-POLICY"
auth:
  - "manual Coupon replenishment requires current super-admin authorization"
tests:
  unit: "go test ./internal/domains/xiangwan/coupon/..."
  integration: "go test -tags=integration ./internal/test/integration -run XiangwanPeople"
runbook:
  - "Reconcile pending invited-guest grants from PostgreSQL Checkin facts"
  - "Missing face, expiry, or scope configuration fails closed"
  - "Missing full-refund policy blocks a redeemed-Coupon refund completion"
---

# Xiangwan Coupon

This package owns immutable roundtable Coupon instruments and their append-only
grant ledger. Migration 757 and `postgres.Grantor` implement two exact
five-Coupon grant paths:

- the first valid invited-guest Checkin, derived from its trusted People binding
  and Instance-role history;
- an authorized manual replenishment after a historical grant only when the
  current available balance is zero.

Automatic commands accept only tenant and Checkin identity. Face value, expiry,
scope, and minimum-order facts come from a versioned policy provider inside the
same serializable PostgreSQL transaction. Manual grants additionally retain the
operator, reason, context, and idempotency business key.

Migration 819 adds append-only customer-evidenced grant-policy snapshots. An
optional activation writer now verifies an exact canonical JSON document with
the configured customer Ed25519 public key, checks tenant and policy shape,
requires a future effective time, and commits the new version and content-free
administrator audit after a live generation and super-admin identity/Grant
check. A later exact replay returns the original receipt. The PostgreSQL
provider reads the latest snapshot effective at the trusted source
time under the Grantor's transaction: first-guest grants use the Checkin event
time, and manual replenishment uses its actual grant time. It treats an explicit
disabled snapshot, missing row, or invalid
shape as unavailable. The migration seeds no policy, and customer-signed
face value, validity, scope and evidence are still absent. An optional,
separately credentialed reconciliation worker now wires this provider to
`Grantor.ReconcilePending`, and drains revoked-Checkin corrections. It only
lists first eligible Checkins whose source time had an enabled signed policy;
its grant and correction transactions recheck the active generation before
new ledger writes. The registration route cannot issue a Coupon by itself;
the worker must be separately deployed after customer signoff and database
validation. Direct ad hoc SQL is not an operations path.

`Grantor.ReconcilePending` derives unfinished automatic grants from durable Checkin
events and ledger absence. It uses neither Redis nor an in-memory queue. A
complete batch is replayed without consulting current configuration; incomplete
or conflicting persisted facts fail closed.

Migration 758 extends the same ledger with a per-Coupon sequence and immutable
`held`, `released`, `redeemed`, `invalidated`, and
`correction_required` entries. `LifecycleWriter` rechecks Coupon ownership,
scope, expiry, minimum Order value, exact discount, Order state, Registration
state, and the PostgreSQL capacity Hold before each transition. An Order can
hold only one Coupon, and exact retries replay their first ledger fact.

`CorrectionReconciler` consumes a revoked Checkin from PostgreSQL. Coupons that
were never held or redeemed become invalidated; a held or redeemed Coupon keeps
its financial history and receives an explicit manual-correction marker. If a
held Coupon is later released, reconciliation can safely append invalidation.
This follow-up projects history at the current recorded time while preserving
the original revocation source time, so a later release is visible.

Atomic integration into Registration creation, payment confirmation, Order
closure is added by migration 759 and the payment transaction coordinators.
The client supplies only a Coupon identity: the server locks its ledger,
derives the immutable discount, records the hold with the Order, and redeems or
releases it in the same PostgreSQL transaction as the corresponding payment,
expiry, or cancellation fact. Exact full-price coverage produces a local
`settled_zero` Order with no provider payment fact. Versioned Coupon restoration
or forfeiture is expressed by migration 760 as a policy-versioned ledger entry
linked to the exact redemption and, for cash refunds, the exact Refund case.
The Refund processor requires a valid server-side policy decision before it
can commit a full cash refund for a redeemed Coupon; replay returns the stored
decision without consulting current configuration. Restored Coupons can be
used again while still valid, whereas forfeiture remains terminal.

Migration 761 applies the same signed decision to cancellation of a
`settled_zero` Registration. Direct, Session, and whole-Instance cancellation
lock the redemption, cancel participation, and append exactly one restoration
or forfeiture entry in the same PostgreSQL transaction. No cash Refund is
fabricated. Preview and immutable receipt counts are fenced against the Coupon
ledger, and a missing or invalid policy rolls back the complete cancellation.

`MyCouponsReader` builds the consumer “我的优惠券” page from the immutable
instrument and complete PostgreSQL ledger. The tenant/principal-bound cursor
freezes one as-of instant, available Coupons are ordered by nearest expiry, and
SQL classification is checked against `ProjectMyCoupon` before any row is
returned. Each item exposes a typed Series or activity-type applicability
target, never guesses a Session and never relies on Redis or a cached balance.
The owner-bound `GET /api/v1/xiangwan/me/coupons` adapter carries that frozen
cursor and state model to the independent runtime while omitting ledger entry,
operator, grant-source, ranking, and policy-evidence fields.

Migration 798 adds a partial index for the administrator's historical
`correction_required` list. The tenant super administrator can inspect exact
Coupon, revoked-Checkin event, and related Order/Registration routing IDs in
a snapshot-paginated, audited read. A marker is a review lead, not proof that
a financial or entitlement correction is complete. The optional administrator
`POST /coupon-grants` route derives the beneficiary from an existing tenant
Coupon, checks the exact live super-admin identity and generation inside the
serializable grant transaction, requires historical issuance and zero current
available balance, reads the effective signed policy, and commits five
instruments with an audit row. Without the customer verification key the route
is absent; without an effective signed policy it cannot issue a Coupon.

Migration 821 and the Admin correction command record a separate two-person
manual handling history. A held Coupon must first leave its hold. A redeemed
Coupon requires full-face-value external adjustment evidence; a never-redeemed
Coupon requires an invalidated current ledger. These events do not alter this
package's Coupon ledger or provider financial facts. Native PostgreSQL tests
cover both paths, audit rollback and immutable history; operational rules are
in [the correction SOP](../../../../apps/xiangwan-admin-web/docs/coupon-correction-sop.md).
