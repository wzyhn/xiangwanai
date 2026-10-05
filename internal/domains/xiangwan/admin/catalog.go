package xiangwanadmin

import (
	"context"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/payment"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/refund"
	"github.com/google/uuid"
)

const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

type SeriesPage struct {
	Items    []activity.Series
	Page     int
	PageSize int
	Total    int64
}

type InstanceListItem struct {
	Instance     activity.Instance
	SeriesTitle  string
	SessionCount int
}

type InstancePage struct {
	Items    []InstanceListItem
	Page     int
	PageSize int
	Total    int64
}

type InstanceDetail struct {
	Series        activity.Series
	Instance      activity.Instance
	Sessions      []activity.Session
	Questionnaire *InstanceQuestionnaire
	CopyLineage   *InstanceCopyLineage
}

type InstanceCopyLineage struct {
	SourceInstanceID             uuid.UUID
	SourceInstanceVersion        int64
	SourcePresentationRevision   int64
	SourceQuestionnaireVersionID uuid.UUID
}

// InstanceReviewStatus is a read-only projection of the public review
// publication chain for one exact Instance. It deliberately exposes only
// counts and publication timestamps; review body blocks, storage keys and
// external URLs remain behind the public review/media readers.
type InstanceReviewStatus struct {
	InstanceID                  uuid.UUID
	InstanceStatus              activity.InstanceStatus
	PublicReviewEligible        bool
	PublicReviewAvailable       bool
	InstanceReviewDocumentCount int
	PublicSessionResourceCount  int
	LatestPublishedAt           *time.Time
	Sessions                    []SessionReviewStatus
}

type SessionReviewStatus struct {
	SessionID             uuid.UUID
	Status                activity.SessionStatus
	PublicResourceCount   int
	PublicReviewAvailable bool
	LatestPublishedAt     *time.Time
}

// InstanceQuestionnaire is the immutable questionnaire contract currently
// assigned to one activity Instance. Contact name and phone remain part of
// the platform registration contract; Fields contains the activity-specific
// additions configured by an operator.
type InstanceQuestionnaire struct {
	QuestionnaireVersionID uuid.UUID
	InstanceID             uuid.UUID
	Version                int64
	PrivacyPurpose         string
	PrivacyPolicyVersion   string
	PublishedAt            time.Time
	Fields                 []activity.QuestionnaireField
}

type PublishInstanceQuestionnaireCommand struct {
	ActorID              uuid.UUID
	IdentityLinkID       uuid.UUID
	OperationID          uuid.UUID
	RequestID            string
	InstanceID           uuid.UUID
	PrivacyPurpose       string
	PrivacyPolicyVersion string
	Fields               []activity.QuestionnaireField
}

// QuestionnaireTemplate is a reusable, versioned questionnaire definition.
// A template is only a creation aid: assigning it to an Instance always
// creates a new immutable Instance questionnaire version, so later template
// edits cannot change registrations or published activity history.
type QuestionnaireTemplate struct {
	ID                   uuid.UUID
	VersionID            uuid.UUID
	Name                 string
	Description          string
	Version              int64
	PrivacyPurpose       string
	PrivacyPolicyVersion string
	Status               string
	Fields               []activity.QuestionnaireField
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

type QuestionnaireTemplatePage struct {
	Items    []QuestionnaireTemplate
	Page     int
	PageSize int
	Total    int64
}

type CreateQuestionnaireTemplateCommand struct {
	ActorID              uuid.UUID
	IdentityLinkID       uuid.UUID
	OperationID          uuid.UUID
	RequestID            string
	Name                 string
	Description          string
	PrivacyPurpose       string
	PrivacyPolicyVersion string
	Fields               []activity.QuestionnaireField
}

type UpdateQuestionnaireTemplateCommand struct {
	ActorID              uuid.UUID
	IdentityLinkID       uuid.UUID
	OperationID          uuid.UUID
	RequestID            string
	TemplateID           uuid.UUID
	ExpectedVersion      int64
	Name                 string
	Description          string
	PrivacyPurpose       string
	PrivacyPolicyVersion string
	Fields               []activity.QuestionnaireField
}

type ArchiveQuestionnaireTemplateCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	TemplateID      uuid.UUID
	ExpectedVersion int64
}

type ApplyQuestionnaireTemplateCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	TemplateID      uuid.UUID
	InstanceID      uuid.UUID
	ExpectedVersion int64
}

// QuestionnaireTemplateCatalog is optional on the legacy Catalog interface
// so existing route fakes remain source-compatible. The production PostgreSQL
// catalog implements it and the HTTP handler advertises the routes whenever
// the optional surface is available.
type QuestionnaireTemplateCatalog interface {
	ListQuestionnaireTemplates(context.Context, Principal, int, int) (QuestionnaireTemplatePage, error)
	GetQuestionnaireTemplate(context.Context, Principal, uuid.UUID) (QuestionnaireTemplate, error)
	CreateQuestionnaireTemplate(context.Context, CreateQuestionnaireTemplateCommand) (QuestionnaireTemplate, error)
	UpdateQuestionnaireTemplate(context.Context, UpdateQuestionnaireTemplateCommand) (QuestionnaireTemplate, error)
	ArchiveQuestionnaireTemplate(context.Context, ArchiveQuestionnaireTemplateCommand) (QuestionnaireTemplate, error)
	ApplyQuestionnaireTemplate(context.Context, ApplyQuestionnaireTemplateCommand) (InstanceQuestionnaire, error)
}

type BrandProfile struct {
	Configured         bool
	LifecycleStatus    activity.BrandLifecycleStatus
	PublicationVersion int64
	Version            int64
	CommunityName      string
	BrandIntro         string
	HeroMode           activity.HomeHeroMode
	HeroEyebrow        string
	HeroSubtitle       string
	HeroImageURL       string
	HeroImageAlt       string
	QuickTags          []activity.HomeQuickTag
	PublishedAt        *time.Time
	UpdatedAt          *time.Time
}

type PublishBrandProfileCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	ExpectedVersion int64
	CommunityName   string
	BrandIntro      string
	HeroMode        activity.HomeHeroMode
	HeroEyebrow     string
	HeroSubtitle    string
	HeroImageURL    string
	HeroImageAlt    string
	QuickTags       []activity.HomeQuickTag
}

type CreateSeriesCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	OperationID    uuid.UUID
	RequestID      string
	Title          string
	HomeVisible    bool
}

type CreateInstanceCommand struct {
	ActorID               uuid.UUID
	IdentityLinkID        uuid.UUID
	OperationID           uuid.UUID
	RequestID             string
	SeriesID              uuid.UUID
	ExpectedSeriesVersion int64
	// IssueNo is optional at the API boundary. Zero asks the server to allocate
	// the next Series-scoped number under the Series row lock.
	IssueNo       int
	Title         string
	ActivityType  activity.ActivityType
	QuickTagCodes []string
	CoverImageURL string
	DetailBlocks  []activity.DetailBlock
	// Non-zero only for POST /instances/:source_id/copies. The source is
	// checked under the same transaction as the independent target creation.
	SourceInstanceID                     uuid.UUID
	ExpectedSourceVersion                int64
	ExpectedSourcePresentationRevision   int64
	ExpectedSourceQuestionnaireVersionID uuid.UUID
}

// UpdateInstanceCommand patches the presentational fields of one Instance.
// A nil pointer leaves the field untouched; a non-nil DetailBlocks replaces
// the whole list (an empty slice clears it). The fence is the dedicated
// presentation revision, not the operational version public review resources
// pin: the revision advances only when cover_image_url or detail_blocks
// actually change, so a cosmetic edit on a published/completed Instance never
// invalidates its published review documents.
type UpdateInstanceCommand struct {
	ActorID                      uuid.UUID
	IdentityLinkID               uuid.UUID
	OperationID                  uuid.UUID
	RequestID                    string
	InstanceID                   uuid.UUID
	ExpectedPresentationRevision int64
	CoverImageURL                *string
	DetailBlocks                 *[]activity.DetailBlock
}

type CreateSessionCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	ExpectedInstanceVersion int64
	Title                   string
	RegistrationStartAt     time.Time
	RegistrationEndAt       time.Time
	SessionStartAt          time.Time
	SessionEndAt            time.Time
	Capacity                int
	GroupMinimum            int
	LowStockThreshold       int
	PriceCents              int64
	DeliveryMode            activity.DeliveryMode
	Area                    activity.AreaCode
	VenueName               string
	Address                 string
	Longitude               *float64
	Latitude                *float64
	OnlineParticipationMode string
	OnlineCompliant         bool
	SortOrder               int
}

type PublishInstanceCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	ExpectedInstanceVersion int64
}

// SchedulePublicationCommand records a future publication intent. It never
// publishes inline; a fenced worker later reuses PublishInstance with these
// exact actor, identity and instance-version facts.
type SchedulePublicationCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	ExpectedInstanceVersion int64
	ScheduledAt             time.Time
}

type ScheduledPublication struct {
	ID                      uuid.UUID
	InstanceID              uuid.UUID
	ExpectedInstanceVersion int64
	ScheduledAt             time.Time
	Status                  string
	AttemptCount            int
	LeaseExpiresAt          *time.Time
	CompletedAt             *time.Time
	LastError               string
	Version                 int64
}

// ScheduledPublicationCatalog is optional so legacy handler fakes remain
// source compatible while production Catalogs expose durable scheduling.
type ScheduledPublicationCatalog interface {
	ScheduleInstancePublication(context.Context, SchedulePublicationCommand) (ScheduledPublication, error)
}

type ScheduledPublicationQueryCatalog interface {
	ListInstancePublicationSchedules(context.Context, Principal, uuid.UUID) ([]ScheduledPublication, error)
}

type CancelScheduledPublicationCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	OperationID    uuid.UUID
	RequestID      string
	InstanceID     uuid.UUID
	ScheduleID     uuid.UUID
}

type ScheduledPublicationCancellationCatalog interface {
	CancelScheduledPublication(context.Context, CancelScheduledPublicationCommand) (ScheduledPublication, error)
}

type CompleteInstanceCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	ExpectedInstanceVersion int64
}

type ArchiveInstanceCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	ExpectedInstanceVersion int64
}

type ArchiveSessionCommand struct {
	ActorID                uuid.UUID
	IdentityLinkID         uuid.UUID
	OperationID            uuid.UUID
	RequestID              string
	SessionID              uuid.UUID
	ExpectedSessionVersion int64
}

// PreviewInstanceCancellationCommand creates the short-lived, immutable
// impact snapshot shown to an operator before an activity is taken offline.
// The activity PostgreSQL transaction rechecks every registration, hold and
// refund fact when the final command is submitted.
type PreviewInstanceCancellationCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	OperationID    uuid.UUID
	RequestID      string
	InstanceID     uuid.UUID
	Reason         string
}

type CancelInstanceCommand struct {
	ActorID                 uuid.UUID
	IdentityLinkID          uuid.UUID
	OperationID             uuid.UUID
	RequestID               string
	InstanceID              uuid.UUID
	PreviewID               uuid.UUID
	ExpectedInstanceVersion int64
	Reason                  string
}

type InstanceCancellationResult struct {
	Instance        activity.Instance
	Receipt         activity.InstanceCancellationReceipt
	SessionReceipts []activity.SessionCancellationReceipt
}

// InstanceCancellationService is kept separate from Catalog so existing
// catalog fakes and read-only consumers do not accidentally gain a destructive
// operation. The concrete PostgreSQL Catalog implements this optional surface.
type InstanceCancellationService interface {
	PreviewInstanceCancellation(context.Context, PreviewInstanceCancellationCommand) (activity.InstanceCancellationPreview, error)
	CancelInstance(context.Context, CancelInstanceCommand) (InstanceCancellationResult, error)
}

type PreviewSessionCancellationCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	OperationID    uuid.UUID
	RequestID      string
	SessionID      uuid.UUID
}

type CancelSessionCommand struct {
	ActorID                uuid.UUID
	IdentityLinkID         uuid.UUID
	OperationID            uuid.UUID
	RequestID              string
	SessionID              uuid.UUID
	PreviewID              uuid.UUID
	ExpectedSessionVersion int64
	Reason                 string
}

type SessionCancellationResult struct {
	Session activity.Session
	Receipt activity.SessionCancellationReceipt
}

type SessionCancellationService interface {
	PreviewSessionCancellation(context.Context, PreviewSessionCancellationCommand) (activity.SessionCancellationPreview, error)
	CancelSession(context.Context, CancelSessionCommand) (SessionCancellationResult, error)
}

// LifecycleArchiveCatalog owns the explicit, append-audit archive commands.
// It is optional on Catalog so read-only and legacy route fakes stay source
// compatible. Archiving changes only the lifecycle status: publication,
// completion, cancellation, registration, order, and refund facts remain
// immutable.
type LifecycleArchiveCatalog interface {
	ArchiveInstance(context.Context, ArchiveInstanceCommand) (activity.Instance, error)
	ArchiveSession(context.Context, ArchiveSessionCommand) (activity.Session, error)
}

// PeopleRoleCatalog is the narrow administrator surface for maintaining the
// People profiles that may be shown as activity leaders. It is optional on
// Catalog so read-only/test implementations remain source compatible. Role
// assignment accepts a PeopleProfile ID, never an arbitrary Principal ID;
// the PostgreSQL writer resolves the active trusted People binding and
// requires an approved profile inside the same transaction.
type PeopleRoleCatalog interface {
	ListPeople(context.Context, Principal, int, int) (PeoplePage, error)
	ListInstanceRoles(context.Context, Principal, uuid.UUID) ([]InstanceRole, error)
	AssignInstanceRole(context.Context, AssignInstanceRoleCommand) (InstanceRole, error)
	RevokeInstanceRole(context.Context, RevokeInstanceRoleCommand) (InstanceRole, error)
}

// PeopleProfileCatalog is the optional administrator write surface for the
// moderated PeopleProfile record itself.  It deliberately remains separate
// from PeopleRoleCatalog: a profile may be public before it has a trusted
// binding, and role assignment must never become an identity writer.
type PeopleProfileCatalog interface {
	CreatePeopleProfile(context.Context, CreatePeopleProfileCommand) (Person, error)
	UpdatePeopleProfile(context.Context, UpdatePeopleProfileCommand) (Person, error)
	ReviewPeopleProfile(context.Context, ReviewPeopleProfileCommand) (Person, error)
}

type CreatePeopleProfileCommand struct {
	ActorID        uuid.UUID
	IdentityLinkID uuid.UUID
	OperationID    uuid.UUID
	RequestID      string
	DisplayName    string
	Headline       string
	Introduction   string
}

type UpdatePeopleProfileCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	PeopleProfileID uuid.UUID
	ExpectedVersion int64
	DisplayName     string
	Headline        string
	Introduction    string
}

type ReviewPeopleProfileCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	PeopleProfileID uuid.UUID
	ExpectedVersion int64
	Decision        string
}

type PeoplePage struct {
	Items    []Person
	Page     int
	PageSize int
	Total    int64
}

// Person is an administrator-safe profile projection. Principal IDs and
// contact data are intentionally absent; role assignment resolves the
// trusted binding by profile ID on the server.
type Person struct {
	ID               uuid.UUID
	DisplayName      string
	Headline         *string
	Introduction     string
	ProfileStatus    string
	ModerationStatus string
	Version          int64
	UpdatedAt        time.Time
	ActiveBindingID  *uuid.UUID
}

type InstanceRole struct {
	ID              uuid.UUID
	InstanceID      uuid.UUID
	PeopleProfileID uuid.UUID
	DisplayName     string
	Headline        *string
	RoleCode        string
	RoleStatus      string
	GrantReason     string
	Version         int64
	GrantedAt       time.Time
	RevokedAt       *time.Time
}

type AssignInstanceRoleCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	InstanceID      uuid.UUID
	PeopleProfileID uuid.UUID
	RoleCode        string
	GrantReason     string
}

type RevokeInstanceRoleCommand struct {
	ActorID         uuid.UUID
	IdentityLinkID  uuid.UUID
	OperationID     uuid.UUID
	RequestID       string
	InstanceID      uuid.UUID
	RoleBindingID   uuid.UUID
	ExpectedVersion int64
	Reason          string
}

type RegistrationFilter struct {
	SeriesID           *uuid.UUID
	InstanceID         *uuid.UUID
	SessionID          *uuid.UUID
	ParticipationState string
	Page               int
	PageSize           int
}

type RegistrationItem struct {
	ID                  uuid.UUID
	SeriesID            uuid.UUID
	InstanceID          uuid.UUID
	SessionID           uuid.UUID
	InstanceTitle       string
	SessionTitle        string
	ContactNameMasked   string
	ContactPhoneMasked  string
	ParticipationStatus string
	PaymentStatus       *string
	RefundStatus        *string
	CheckinStatus       string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RegistrationPage struct {
	Items    []RegistrationItem
	Page     int
	PageSize int
	Total    int64
}

type RegistrationDetail struct {
	RegistrationItem
	Contact                    *RegistrationContact
	PriceCents                 *int64
	InstancePublicationVersion int64
	SessionVersion             int64
	ConfirmedAt                *time.Time
	CancelledAt                *time.Time
	CancellationReason         string
	CheckinID                  *uuid.UUID
	CheckedInAt                *time.Time
	SuccessfulRefundCents      *int64
	PrivacyPolicyVersion       string
}

// RegistrationContact is returned only on an audited detail read by a live
// tenant activity operator. List and onsite-only projections stay masked.
type RegistrationContact struct {
	Name  string
	Phone string
}

// RegistrationAnswerSummary is the operator-facing, privacy-preserving
// projection of one submitted questionnaire field. It deliberately omits
// answer_values (which may contain free text or other personal information)
// and contact snapshots.
type RegistrationAnswerSummary struct {
	RegistrationID         uuid.UUID
	InstanceID             uuid.UUID
	SessionID              uuid.UUID
	QuestionnaireVersionID uuid.UUID
	FieldID                uuid.UUID
	FieldCode              string
	FieldType              activity.QuestionnaireFieldType
	FieldLabel             string
	Required               bool
	SortOrder              int
	Answered               bool
	ValueCount             int
	CreatedAt              time.Time
}

type RegistrationAnswerSummaryFilter struct {
	InstanceID     *uuid.UUID
	SessionID      *uuid.UUID
	RegistrationID *uuid.UUID
	Page           int
	PageSize       int
	Format         string
}

type RegistrationAnswerSummaryPage struct {
	Items    []RegistrationAnswerSummary
	Page     int
	PageSize int
	Total    int64
}

// RegistrationAnswerCatalog is optional on Catalog to keep existing route
// fakes source-compatible. The production PostgreSQL catalog implements this
// read-only surface and never returns raw answer_values or contact data.
type RegistrationAnswerCatalog interface {
	ListRegistrationAnswerSummaries(context.Context, Principal, RegistrationAnswerSummaryFilter) (RegistrationAnswerSummaryPage, error)
}

// RegistrationAnswers are exposed only for an exact, purpose-bound and
// audited operator read. List/export surfaces remain metadata-only.
type RegistrationAnswerDetail struct {
	FieldID                uuid.UUID
	QuestionnaireVersionID uuid.UUID
	FieldCode              string
	FieldType              activity.QuestionnaireFieldType
	FieldLabel             string
	Required               bool
	SortOrder              int
	Options                []activity.QuestionnaireOption
	Values                 []string
	CreatedAt              time.Time
}

type RegistrationAnswerDetailSet struct {
	RegistrationID uuid.UUID
	InstanceID     uuid.UUID
	SessionID      uuid.UUID
	Items          []RegistrationAnswerDetail
}

type RegistrationAnswerDetailCatalog interface {
	GetRegistrationAnswers(context.Context, Principal, uuid.UUID, string) (RegistrationAnswerDetailSet, error)
}

// AuditEvent is the minimum operator-facing event projection. Details can
// contain sensitive policy or command metadata and never leave PostgreSQL.
type AuditEvent struct {
	ID         uuid.UUID
	ActorID    uuid.UUID
	ActionCode string
	TargetType string
	TargetID   uuid.UUID
	OccurredAt time.Time
}

type AuditEventPage struct {
	Items    []AuditEvent
	Page     int
	PageSize int
	Total    int64
	AsOf     time.Time
}

type AuditEventFilter struct {
	Page     int
	PageSize int
	AsOf     *time.Time
}

type AuditEventCatalog interface {
	ListAuditEvents(context.Context, Principal, AuditEventFilter) (AuditEventPage, error)
}

type RefundQueueFilter struct {
	Status refund.Status
	Limit  int
	Cursor string
}

type RefundQueueItem struct {
	CaseID                uuid.UUID
	OrderID               uuid.UUID
	RegistrationID        uuid.UUID
	InstanceID            uuid.UUID
	SessionID             uuid.UUID
	SeriesTitle           string
	InstanceTitle         string
	SessionTitle          string
	Status                refund.Status
	ReasonCode            refund.ReasonCode
	RequestedRefundCents  int64
	SuccessfulRefundCents int64
	Version               int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

type RefundQueuePage struct {
	Items      []RefundQueueItem
	Status     refund.Status
	NextCursor string
}

type RefundQueueCatalog interface {
	ListRefundQueue(context.Context, Principal, RefundQueueFilter) (RefundQueuePage, error)
}

type RefundCaseEvent struct {
	Sequence               int64
	Type                   refund.EventType
	FromStatus             refund.Status
	ToStatus               refund.Status
	SuccessfulRefundCents  int64
	ResultingRefundVersion int64
	OccurredAt             time.Time
}

type RefundCaseDetail struct {
	Case   RefundQueueItem
	Events []RefundCaseEvent
}

type RefundCaseCatalog interface {
	GetRefundCase(context.Context, Principal, uuid.UUID) (RefundCaseDetail, error)
}

const (
	DefaultOrderListLimit = 50
	MaxOrderListLimit     = 100
)

type OrderListFilter struct {
	Status string
	Limit  int
	Cursor string
}

type OrderListItem struct {
	OrderID               uuid.UUID
	RegistrationID        uuid.UUID
	InstanceID            uuid.UUID
	SessionID             uuid.UUID
	SeriesTitle           string
	InstanceTitle         string
	SessionTitle          string
	PaymentStatus         payment.OrderStatus
	OriginalPriceCents    int64
	DiscountCents         int64
	PayableCents          int64
	ActualPaidCents       *int64
	RefundCaseID          *uuid.UUID
	RefundStatus          refund.Status
	RequestedRefundCents  int64
	SuccessfulRefundCents int64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	PaidAt                *time.Time
	ClosedAt              *time.Time
}

type OrderListPage struct {
	Items      []OrderListItem
	Status     string
	NextCursor string
}

type OrderListCatalog interface {
	ListOrders(context.Context, Principal, OrderListFilter) (OrderListPage, error)
}

type OrderDetailCatalog interface {
	GetOrder(context.Context, Principal, uuid.UUID) (OrderListItem, error)
}

type CheckinTarget struct {
	SeriesID                   uuid.UUID
	InstanceID                 uuid.UUID
	SessionID                  uuid.UUID
	SeriesTitle                string
	InstanceTitle              string
	SessionTitle               string
	SessionStartAt             time.Time
	VenueName                  string
	ConfirmedRegistrationCount int
	CheckedInRegistrationCount int
	Capacity                   int
}

type CheckinTargetPage struct {
	Items    []CheckinTarget
	Page     int
	PageSize int
	Total    int64
}

type Catalog interface {
	GetBrandProfile(context.Context, Principal) (BrandProfile, error)
	PublishBrandProfile(context.Context, PublishBrandProfileCommand) (BrandProfile, error)
	ListSeries(context.Context, Principal, int, int) (SeriesPage, error)
	CreateSeries(context.Context, CreateSeriesCommand) (activity.Series, error)
	ListInstances(context.Context, Principal, *uuid.UUID, int, int) (InstancePage, error)
	GetInstance(context.Context, Principal, uuid.UUID) (InstanceDetail, error)
	GetInstanceReviewStatus(context.Context, Principal, uuid.UUID) (InstanceReviewStatus, error)
	GetInstanceQuestionnaire(context.Context, Principal, uuid.UUID) (*InstanceQuestionnaire, error)
	CreateInstance(context.Context, CreateInstanceCommand) (activity.Instance, error)
	PublishInstanceQuestionnaire(context.Context, PublishInstanceQuestionnaireCommand) (InstanceQuestionnaire, error)
	UpdateInstance(context.Context, UpdateInstanceCommand) (activity.Instance, error)
	CreateSession(context.Context, CreateSessionCommand) (activity.Session, error)
	PublishInstance(context.Context, PublishInstanceCommand) (activity.PublicationEvent, error)
	CompleteInstance(context.Context, CompleteInstanceCommand) (activity.Instance, error)
	ListRegistrations(context.Context, Principal, RegistrationFilter) (RegistrationPage, error)
	GetRegistration(context.Context, Principal, uuid.UUID, string) (RegistrationDetail, error)
	ListCheckinTargets(context.Context, Principal, int, int) (CheckinTargetPage, error)
}
