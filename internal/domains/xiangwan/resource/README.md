---
layer: L3
capability: xiangwan-resource
status: implemented
owner: "@platform"
same_layer_dependencies:
  - xiangwan-activity
tables:
  - "xiangwan_resource_relations"
  - "xiangwan_resource_moderation_observations"
  - "xiangwan_resource_publications"
  - "xiangwan_resource_content_snapshots"
  - "xiangwan_review_photo_curations"
  - "xiangwan_review_resource_replacements"
events:
  emitted: []
  consumed: []
config: []
auth:
  - "resource drafts are created only by an authorized Xiangwan operator"
  - "confirmed-registration resources require a current owner-scoped Registration"
tests:
  unit: "go test ./internal/domains/xiangwan/resource/..."
  integration: "go test -tags=integration ./internal/test/integration -run XiangwanResource"
---

# Xiangwan Resource

Migration 825 adds append-only replacements of published review documents.
The editor uses the existing standalonepg Content write seam, completing the
snapshot, moderation, publication, replacement and audit in one transaction.
The exact period, Session, ordering and original photo URLs are retained;
photo curation is checked by version and mapped to the new Block identities.
All public readers and file grants exclude replaced relations. Hidden typed
links remain administrator-readable but are omitted from public Blocks.
Native Channels identities are validated and moderated in the same snapshot;
they do not use a web origin or infer identities from sharing URLs.

This package owns the immutable binding between a shared platform Content
revision and one exact Xiangwan Instance or Session. It does not create a
second Content or File store.

An `instance_review` is public and cannot carry a Session identity. A
`session_resources` relation must carry the exact Session and may be public or
restricted to a confirmed Registration. The Content revision and target
version are pinned so later publication can fail closed when either side has
changed. Draft creation is idempotent by tenant, operator, and operation key.

Migration 762 deliberately creates only immutable draft candidates. A draft is
not public evidence. Migration 763 records an exact signed-callback,
provider-query, or audited manual-review observation without treating the
observation as publication. External signatures and provider responses must be
verified before this package receives the fact; only digests and the provider
reference are retained here.

Every observation is pinned to the relation's exact Content revision and is
append-only. Provider references are idempotent only when the complete verified
intent matches. `unknown`, `review`, and `rejected` fail closed, while even
`approved` remains private until a separate immutable publication transaction
validates all current facts. This package has no provider client, Redis path, or
public reader.

Migration 764 adds that publication transaction. It pins one approved
observation to the exact relation, Content revision, access policy, and target
version, while PostgreSQL share-locks and revalidates the current Content and
Activity rows before committing. Shared Content remains private; only this
product-owned fact can authorize a later Xiangwan reader. Publication is
append-only, one-time per relation, and exact operation retries return the
original receipt even after later state changes.

Migration 765 closes the mutable-Block gap in the shared Content primitive.
Creating a relation now computes a versioned canonical digest over its title
and ordered Blocks. Content and Block guards make that bound candidate
immutable, and every new moderation observation must carry the exact digest.
Only the digest receipt is duplicated; the Content and Blocks remain the single
body truth for a future Redis-free provider reader.

The standalone Xiangwan runtime wires this package for public reads and the
administrator's narrow review-resource command. The command is implemented by
`resource/postgres.ReviewResourceWriter`: it creates private `review` Content
through the registered `content/standalonepg` transaction seam, then passes
manual-review evidence through the immutable relation → snapshot → observation
→ publication sequence above. Direct writes to `contents`, `blocks`, or these
resource tables outside those owners remain unsupported. The current admin
form accepts a policy-allowlisted video-channel link, optional photo references,
and typed recording/material Feishu links plus optional text; every URL is
canonicalized against the configured HTTPS allowlist. It does not claim to
upload raw bytes or expose a storage key. The writer rechecks
the active runtime generation and exact administrator identity/Grant inside
its transaction, serializes retries by operation key, and locks the completed
Instance (or ended Session) target before creating the candidate.

An optional reviewed-File writer accepts only UUIDv4 File IDs, exact SHA-256
values and an explicit operator review acknowledgement. It requires an
append-only successful private-open audit for the same actor, identity link
and File after confirmation; a browser checkbox alone is insufficient. It verifies private
selected bytes before the transaction, then rechecks the exact
tenant/actor/identity/target upload intent, confirmed File metadata and
server-derived provider key. It pins each File in the same transaction as
Content, relation, digest snapshot, manual moderation observation and
publication; a later conflict rolls the pin back. The production Runtime
still uses the constructor without this media provider, so File submissions
fail closed until media-safety responsibility and deployment are approved.

`postgres.Repository` exposes separate published Instance-review and
Session-resource readers. Both require the complete relation → snapshot →
approved observation → publication chain and current eligible Activity state.
Instance reviews are always public. Session resources marked `public` are
anonymous-readable; `confirmed_registration` rows are returned only when the
same PostgreSQL query finds a current confirmed Registration for the supplied
principal and exact Series/Instance/Session hierarchy. The projection returns
an exact Content ID, revision, schema, and digest—not a mutable body or private
link—so later hydration must use the Redis-free Content/File seams.

The same repository resolves the deterministic context for “往期精彩”. The
anchor must be a public Instance of a recurring Series, and history is limited
to completed or archived Instances in that exact Series with a complete public
publication chain. When the selected Instance has several Session-owned public
resources, the pure domain selector applies offline first, confirmed count
descending, start time ascending, then sort order and Session ID. Restricted
Registration resources never make a Session eligible, and the returned
Instance/Session identity is shared by preview hydration and “查看更多”.

`ReadPastHighlightPreview` resolves that context and projects at most three
unique image File references from the common Instance review plus the selected
Session only. Every image still requires the complete digest-matched public
chain, a confirmed permanent `image/*` File owned by the relation author, and
the current target version. It returns no storage key or legacy CDN URL;
`MoreTarget` repeats the exact Series/Instance/Session context so transport code
cannot send the preview and “查看更多” to different Sessions.

The public Session detail's previous-Instance review preview can also use
administrator-supplied image URLs. Its PostgreSQL scan applies the latest
photo curation before selecting images; the domain projector rechecks each URL
against the runtime's current external-domain policy. Unlisted or non-HTTPS
URLs yield no thumbnail, while a published text/link review can still link to
the full review page.

The independent Runtime now composes `ReadPastHighlightContext`, the exact
past-activity summary, and `ReadPublicReview` for this preview. The optional
`previous_review.session_id` is the selected eligible historical Session;
Mini navigation preserves it in the full review URL. Common period resources
and that Session's resources share the same approved reader and current
photo curation; sibling Session resources cannot enter the preview. A
non-recurring anchor simply has no previous-period module.

`ReadPublicReview` consumes that typed target and returns only six explicit
Block shapes: text, image, link, file, video, and audio. Arbitrary Block JSON,
object keys, private relations, and sibling-Session resources never cross the
boundary. File-backed blocks require a confirmed permanent author-owned File;
unsafe or missing files remain an explicit unavailable/blocked module. External
links require HTTPS and an exact or explicitly wildcarded
`CONFIG-EXTERNAL-DOMAINS` host; missing configuration fails closed.

`AuthorizePublicFile` is the server-side gate for fetching bytes behind one
public file-backed Block. It requires the tenant, relation, Block, and File IDs
to match the same current digest-approved publication chain and rechecks the
Activity version plus confirmed permanent File metadata in PostgreSQL. The
grant contains only typed identity and safe metadata, never an object key or
download URL; a transport may ask its server-side file reader for bytes only
after this gate succeeds. All mismatches are an opaque not-found result.

Migration 797 adds append-only photo curation over a published review relation.
Migration 826 adds an independent optional cover Block to the same immutable
revisions. The cover must belong to the visible image set, and does not change
the image order. An omitted/null `cover_block_id` preserves a still-visible
selection for old clients; an explicit empty string chooses the first available
image automatically. Exact operation replay includes whether the cover was
specified. Resource replacement maps the cover to the new approved Block ID.
Public image Blocks expose optional `is_cover`; clients ignore unavailable
images when choosing a cover. Recording/material cards retain an empty URL as
an unavailable card; hidden cards retain their contents for administrators.
The activity operator can list original image Blocks, including hidden ones,
and append a versioned ordered set of visible Block IDs. The operation key,
exact expected version, relation ownership, current approved publication and
live activity Grant are checked in the same PostgreSQL transaction; a separate
content-free admin audit records reads and writes. Original Content, moderation
and publication rows are never edited. Public review detail, past-highlight
preview and public file authorization all consult the latest curation: omitted
images are hidden, and selected images follow the new order. This command
cannot add new images or upload bytes. Hiding an external HTTPS image removes
it from Xiangwan projections but cannot revoke a copy served by that external
origin; takedown there remains a separate operator action.

The Xiangwan public media handler now consumes that grant together with
Storage's narrow confirmed-object reader. The HTTP path carries the exact
Relation, Block, and File identities; each request repeats authorization before
streaming, and storage keys remain internal to the provider seam.

Public review bodies, preview images, and file grants also exclude every
Content registered in `content_governance`. A past publication does not bypass
the shared Content protection boundary; each read checks it again, and a denied
file grant prevents the media handler from fetching bytes.
