# Xiangwan booking read models

This package composes Activity, Registration, Order, Refund, and check-in facts
without collapsing their independent state axes.

`ProjectMyRegistration` produces the canonical user-facing reservation state.
Pending-payment and registered items sort by Session start time ascending;
refund-processing, refunded, cancelled, and ended items sort by their latest
business fact descending. Every item retains its exact Series, Instance,
Session, Registration, payment, Refund, and check-in identity.

`postgres.Repository.List` owns the PostgreSQL read path for “我的预约”. It
requires both tenant and principal identity, uses a mixed-direction stable
keyset cursor bound to that ownership and filter context, and freezes the
classification time across pages. SQL classification is checked against the
pure domain projection while scanning, so the adapter fails closed if the two
rules drift.

The adapter left-joins the exact tenant/Registration/Session/participant
Checkin. Missing rows remain explicit `not_recorded` facts; persisted
`checked_in` and `revoked` timestamps participate in the latest-business-time
projection without changing participation, payment, or Refund state.

`ProjectMyOrder` and `postgres.Repository.ListOrders` provide the matching
Order-centered view. Pending, provider-unknown, paid, closed, Refund-processing,
and refunded facts remain distinguishable; Coupon-only `settled_zero` Orders
remain in the paid view but expose a distinct outcome and no provider payment
fact. Only a live positive-payable pending hold exposes a continue-payment
action. The stable cursor is bound to tenant, principal, filter, and
classification time, and every item links back to the exact Registration and
Session.

`postgres.Repository.GetOrderDetail` applies the same tenant/principal fence to
one exact Order. Its outcome code distinguishes provider confirmation in
progress, trusted payment, unpaid closure, every manual Refund phase, rejection,
and successful refund without deriving one axis from another.

`postgres.Repository.GetRegistrationDetail` applies the equivalent ownership
fence to one historical Registration. It retains the original Session, selects
Instance cancellation over its child Session receipt when both exist, explains
access denial, and exposes check-in/private-access issuance only through the
same current confirmed-participation gate. Paid cancellation remains explicitly
`unavailable` until the server can supply a registration-specific final decision
and an accessible policy target; a separate reason flag prevents clients from
promoting eligibility from global capability data. Free and known-unpaid
cancellation capability is projected with the same configured, Session-specific
cutoff evaluator that the write command rechecks under PostgreSQL locks.
Persisted Coupon restore or
forfeit decisions are recovered from the owner-, Order-, Registration-, and
Refund-fenced ledger fact rather than from a transient command response.

PostgreSQL is the only persistence and coordination dependency; Redis is not
used.
