# Content standalone PostgreSQL seam

`standalonepg` is the Content provider's narrow seam for an independently
deployed Xiangwan runtime. It has no dependency on Content HTTP handlers,
Gin, middleware, cache, eventbus, or Redis.

`Writer` is bound to a caller-owned `*sql.Tx`; it never begins, commits, or
rolls back a transaction. `CreateReview` and `UpdateReview` own the only
Content/Block SQL for this seam and always stamp the fixed `wq-xiangwan`
product code, `type=review`, `visibility=private`, `owner_type=user`, and
`space_id=NULL`. The caller cannot provide a product code or make the draft
generic-public. Only `active` and `reviewing` statuses are accepted. Block
types are the existing registered review types (`text`, `image`, `link`,
`file`, `video`, `audio`) and block data must be a JSON object.

Updates lock the tenant- and principal-scoped Content row and require an exact
`contents.updated_at` revision plus a strictly later timestamp. They replace
the complete ordered block set in the same transaction. Migration 765's
database guard rejects mutation after an Xiangwan resource relation binds the
revision, so the provider snapshot remains immutable even if a caller skips
its optimistic check.

`Reader` accepts either `*sql.DB` or `*sql.Tx`. It rechecks the fixed product,
tenant, owner, private visibility, status, and deletion predicates in SQL, and
can require the exact revision pinned by a product relation. It returns only
typed Content/Block facts and no product relation, storage key, URL, or event.

The caller owns request authentication, `writeChain`/content-safety policy,
Xiangwan relation/publication state, and any transaction-local outbox. The
seam does not create a `content_governance` row: those rows are reserved for
the existing governance policy and would make a published Xiangwan resource
ineligible for the resource reader. Private visibility and the fixed product
predicate keep these drafts out of generic public reads.
