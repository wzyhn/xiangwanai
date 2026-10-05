package activity

import (
	"time"

	"github.com/google/uuid"
)

type SeriesStatus string

const (
	SeriesStatusDraft    SeriesStatus = "draft"
	SeriesStatusActive   SeriesStatus = "active"
	SeriesStatusArchived SeriesStatus = "archived"
)

type InstanceStatus string

const (
	InstanceStatusDraft          InstanceStatus = "draft"
	InstanceStatusPendingPublish InstanceStatus = "pending_publish"
	InstanceStatusPublished      InstanceStatus = "published"
	InstanceStatusCompleted      InstanceStatus = "completed"
	InstanceStatusCancelled      InstanceStatus = "cancelled"
	InstanceStatusArchived       InstanceStatus = "archived"
)

type SessionStatus string

const (
	SessionStatusDraft     SessionStatus = "draft"
	SessionStatusPublished SessionStatus = "published"
	SessionStatusCancelled SessionStatus = "cancelled"
	SessionStatusEnded     SessionStatus = "ended"
	SessionStatusArchived  SessionStatus = "archived"
)

// Series is the stable Activity identity across separately published Instances.
type Series struct {
	ID                               uuid.UUID
	TenantID                         uuid.UUID
	Title                            string
	Status                           SeriesStatus
	IsRecurring                      bool
	HomeVisible                      bool
	SuccessfulPublishedInstanceCount int
	FavoriteCount                    int64
	HistoricalRegistrationCount      int64
	CurrentPublicInstanceID          *uuid.UUID
	Version                          int64
	CreatedAt                        time.Time
	UpdatedAt                        time.Time
}

// Instance is one independently publishable occurrence of a Series.
type Instance struct {
	ID       uuid.UUID
	TenantID uuid.UUID
	SeriesID uuid.UUID
	// IssueNo is the immutable, Series-scoped period number. The backend
	// allocates it while holding the Series row lock; clients must never infer
	// it from publication counts because draft/cancelled periods still consume
	// a number.
	IssueNo              int
	Title                string
	Status               InstanceStatus
	ActivityType         *ActivityType
	QuickTagCodes        []string
	CoverImageURL        string
	DetailBlocks         []DetailBlock
	PublicationVersion   int64
	PresentationRevision int64
	ScheduledAt          *time.Time
	PublishedAt          *time.Time
	CompletedAt          *time.Time
	Version              int64
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// Session is the only level users can register for, pay for, check in to, or
// share. Nullable planning fields allow incomplete drafts; the database and
// publication decision require them before a public lifecycle state.
type Session struct {
	ID                           uuid.UUID
	TenantID                     uuid.UUID
	InstanceID                   uuid.UUID
	Title                        string
	Status                       SessionStatus
	RegistrationStartAt          *time.Time
	RegistrationEndAt            *time.Time
	SessionStartAt               *time.Time
	SessionEndAt                 *time.Time
	Capacity                     *int
	GroupMinimum                 *int
	LowStockThreshold            *int
	PriceCents                   *int64
	DeliveryMode                 *DeliveryMode
	Area                         *AreaCode
	VenueName                    *string
	Address                      *string
	Longitude                    *float64
	Latitude                     *float64
	OnlineParticipationMode      *string
	OnlineParticipationCompliant *bool
	ConfirmedRegistrationCount   int
	ActiveHoldCount              int
	SortOrder                    int
	PublishedAt                  *time.Time
	Version                      int64
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
}

// PublicationEvent is the immutable receipt for one successful Instance
// publication version. CandidateDigest binds the exact validated projection.
type PublicationEvent struct {
	ID                 uuid.UUID
	TenantID           uuid.UUID
	SeriesID           uuid.UUID
	InstanceID         uuid.UUID
	PublicationVersion int64
	SessionCount       int
	CandidateDigest    string
	PublishedBy        uuid.UUID
	PublishedAt        time.Time
	CreatedAt          time.Time
}
