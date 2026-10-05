# Xiangwan Contribution

This package owns the PostgreSQL-only, append-only contribution ledger. A host
contribution is earned only when one exact checked-in event is backed, at that
event time, by both an explicit trusted People binding and a host role on the
same Instance. The caller cannot supply role, profile, amount, or status facts.

`postgres.Reconciler` locks the authoritative Checkin lifecycle in a
serializable transaction, derives eligibility from retained People and role
history, and appends at most one `host_checkin` earning per
principal + Instance. Replaying the same Checkin or processing another Session
in that Instance cannot increment the contribution twice.

`ReconcilePending` derives unfinished work directly from immutable Checkin
events and absent ledger effects. A failed run remains queryable for retry, and
concurrent workers converge through PostgreSQL constraints without a Redis
queue or volatile cursor.

Production uses `NewReconcilerWithGeneration`: each ledger transaction locks
the exact customer runtime generation before reading the Checkin source. The
shared `coupon-reconciliation-worker` drains both earnings and reversal work;
its Checkin outbox processor acknowledges revocation only after Contribution
and Coupon ledgers converge.

When the source Checkin is revoked, reconciliation appends one `reversed`
entry linked to the original earning. It never edits or deletes the earning.
The database validates the exact Checkin event and historical identity facts,
and rejects update, delete, or truncate operations on the ledger.

`BuildHistory` reconstructs the active count and retained reversal history
from entries. Consumer projections omit People-binding, role-binding, Checkin,
operator, and audit identifiers.
