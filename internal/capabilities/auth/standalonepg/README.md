# Auth standalone PostgreSQL seam

`standalonepg.Writer` is the Auth-owned, caller-transaction-bound identity
writer for customer runtimes that cannot import the Redis-backed Auth HTTP
package. It owns Principal, WeChat `IdentityLink`, and
`PrincipalProductState` lifecycle SQL. Product code must acquire its runtime
generation fence first and then compose this writer in the same transaction.

This package must remain free of Gin, middleware, cache, event-bus, and Redis
imports.

`CreatePrincipal` and `CreateWeChatIdentity` are the shared initial writes used
by both the central Auth Repository and this transaction-bound Writer. The
central caller starts with a NULL tenant and activity observation; standalone
`Writer.CreatePrincipal` still requires an explicit tenant and login time.
WeChat keys must already be trimmed, with nonempty AppID and OpenID; an absent
UnionID is stored as SQL NULL. The helpers preserve uniqueness errors and do
not begin or commit transactions. An identity insert failure must roll back
the caller's principal creation. Dev login and product completion policy are
outside these WeChat creation helpers.

`BackfillWeChatUnionID` is shared by the central Auth Repository and
`Writer.SetWeChatUnionID`. It fills only SQL NULL, accepts a replay of the
same trimmed value, and rejects replacement, clearing, other providers, and
missing links. The single UPDATE runs on the supplied connection/transaction;
it never commits a caller's transaction. Callers must still validate UnionID
ownership while holding the union advisory lock. This helper does not repair
historical identity splits or replace product-specific login policy.

`RecordProductLogin` preserves the latest login timestamp and its matching
AppID as one observation. An older or equal-time login cannot replace that
source, including when the central Auth writer updates the same product row.
