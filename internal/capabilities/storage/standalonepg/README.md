# Storage standalone PostgreSQL seam

`standalonepg.Writer` is the Storage-owned, caller-transaction-bound metadata
writer for an independently deployed runtime that cannot import the central
Storage HTTP service. The package has no Content, Gin, middleware, cache,
event-bus, or Redis dependency.

The lifecycle is explicit:

```text
CreatePending → provider-owned object staging/inspection → Confirm → Pin
```

`CreatePending` writes one UUIDv4 upload identity with an owner, canonical MIME,
expected size, opaque provider object key, and cleanup deadline. A repeated
identity is accepted only when its immutable facts and object key match.
`Confirm` takes provider-verified MIME, size, and object-key facts, locks the
`files` row, checks the owner and pending lifecycle, and makes the row
`confirmed`. A replay of the same facts is idempotent. `Pin` clears the
cleanup deadline while holding the same row lock; callers should use it in the
same transaction as the durable Content/resource relation and its audit facts.

The PostgreSQL writer does not stage bytes, issue presigned URLs, inspect a
bucket, or return an object key/path. `ReviewLocalStager` is a separate,
Storage-owned local-byte adapter for the isolated Xiangwan runtime; it accepts
only server-derived UUIDv4 keys, streams at most 200 MiB into a private
request-unique file, verifies exact size, SHA-256 and allowlisted MIME magic,
then atomically selects an immutable object. `Inspect` rechecks the selected
bytes before the caller may confirm metadata. `OpenSelected` exposes only a
verified private descriptor to an already authorized caller, never its path.
The target/owner-bound intent,
byte-stage and confirmation handlers exist, but the Xiangwan production Runtime
does not mount them while customer byte-review policy and production composition
remain incomplete.
A subsequent review publication must still record
moderation evidence, pin the File in its durable resource transaction, and
prove the public read grant. This seam alone does not approve or publish media.
`PruneStaleTemporaries` only removes abandoned local request temporary files.
The dedicated Xiangwan `media-cleanup-worker` uses `ReviewCleanupRepository`
to claim expired pending/confirmed File rows under a PostgreSQL row lock,
requires the exact tenant upload intent and server-derived object key, and
uses `DeleteSelected` plus the `deleting_at` lease for crash-safe completion.
It retries provider failures at most five times and never deletes selected UUID
objects solely by filesystem age. Native PostgreSQL integration exercises locked
File exclusion, retry backoff, crashed lease recovery, stale completion rejection,
idempotent physical deletion and preservation of pinned bytes. Deployment remains
a separate evidence requirement. `GetForStaging` checks an owned live pending or
confirmed File under the caller's transaction lock; Xiangwan checks it before and
after byte IO and removes bytes recreated by a request that crossed lifecycle
closure. An immutable upload intent alone cannot revive an expired File.
`GetConfirmed` returns
safe metadata only and requires the owner, a live `confirmed` row, and no
cleanup deadline.

`CanonicalVideoMIME` is the narrow review-media gate for Xiangwan adapters;
generic platform files can use `CanonicalMIME` and impose their own product
policy. HTML and SVG active-content MIME types are rejected at this seam.

The writer never begins or commits a transaction. A product must acquire its
generation fence and then compose File, operation, audit, outbox, and resource
facts in one caller-owned PostgreSQL transaction. This seam is metadata-only;
`storage/publicobject` remains the confirmed-object read seam for local bytes.
