# Xiangwan People and Instance roles

This package keeps three facts deliberately separate:

- `PeopleProfile` is moderated public content and may be published without a
  trusted user binding.
- `Binding` is the explicit, evidence-backed relationship between one
  PeopleProfile and one platform Principal. An unbound profile is never a
  source of identity, roles, contributions, coupons, or other user benefits.
- `InstanceRoleBinding` grants one of the four Canonical roles to a Principal
  for one exact Activity Instance: `host`, `invited_guest`,
  `course_instructor`, or `event_speaker`. Roles are never Session-scoped or
  inferred from profile text. The public Session detail leader stack
  (`people.ProjectSessionLeaders`) is capped at `MaxSessionLeaders` (8): the
  repository pre-pass reads one spare row and the projection drops invalid or
  duplicate bindings before applying the cap.
- `HostApplication` carries the applicant's introduction, relevant experience,
  availability, contact method, current application cycle, and signed policy
  version through pending, approved/rejected, or self-withdrawn review.

Migration 714 persists these facts in the Xiangwan PostgreSQL database. One
Principal can hold several different roles on the same Instance, while the same
active role is unique. Binding and role corrections revoke the current row and
retain it as immutable history; a later grant creates a new row. Raw binding
evidence is not stored here, only its SHA-256 digest.

Migration 828 records one-use 24-hour binding invitations and explicit consent.
The administrator creates the invitation for an exact approved profile version;
`postgres.BindingInvitationAcceptor` privately previews it and confirms the
authenticated consumer. The same serializable transaction locks generation,
live inviter authority, owner and current profile before creating one binding
and retaining consent evidence. Plaintext invitation codes do not enter the
database, logs or URLs. Expired/replaced/withdrawn invitations, changed profiles
and other owners cannot claim or replay the fact. Revoking a binding keeps the
original relationship and never rewrites historical role or Contribution facts.

The PostgreSQL repository exposes tenant-scoped administrative reads and
writes, approved-only public profile detail and stable keyset-list reads,
trusted active-binding lookups, and stable Instance-role listings.
`GET /api/v1/xiangwan/people` and
`GET /api/v1/xiangwan/people/{people_id}` project only public profile content;
they neither join nor infer a binding, Principal, moderation operator, or
content operator. Authorization and command orchestration are separate
application boundaries and must not be inferred by repository callers. Redis
is not used.

Migration 715 adds the HostApplication boundary. Self-service submission reads
the current host rules from an injected PostgreSQL policy adapter, serializes on
the Principal row, and returns the existing active application for a duplicate
submission in the same cycle. Missing rules fail closed. An approved
application or an active Instance `host` role suppresses later application
cycles. Review authorization is injected and evaluated through the same
serializable transaction; exact review retries return the first terminal fact.
Submitted contact and narrative fields cannot be rewritten or exposed through
the public profile query.

`people.BuildMyBenefits` and `postgres.BenefitsReader` provide the consumer
identity boundary. A read-only repeatable-read transaction loads only the
caller's explicit active binding, Instance roles, HostApplication history, and
current host-rules presentation. Current roles exclude revoked relationships
and terminal Instances; the separate history retains both. The returned role
summaries omit grant/revocation reasons and operator identities, while
HostApplication summaries omit submitted contact and narrative fields. An
unconfigured rules provider returns an explicit `pending` state and disables
application instead of inventing requirements or benefits. The same snapshot
loads the caller's append-only Contribution entries and returns only rebuilt
active counts and safe history, without ledger evidence identifiers.

`GET /api/v1/xiangwan/me/benefits` exposes that projection behind strict
consumer authentication. The production reader rechecks the active Principal
inside its repeatable-read transaction; it never searches PeopleProfile text
to guess a binding and never publishes submitted HostApplication contact or
narrative fields.
