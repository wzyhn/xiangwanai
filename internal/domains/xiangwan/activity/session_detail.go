package activity

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type SessionDetailFacts struct {
	SeriesID                         uuid.UUID
	SeriesStatus                     SeriesStatus
	SeriesIsRecurring                bool
	CurrentPublicInstance            bool
	SuccessfulPublishedInstanceCount int
	HistoricalRegistrationCount      int64

	InstanceID            uuid.UUID
	InstanceTitle         string
	InstanceStatus        InstanceStatus
	InstanceActivityType  ActivityType
	InstanceQuickTagCodes []string
	InstanceCoverImageURL string
	InstanceDetailBlocks  []DetailBlock
	PublicationVersion    int64

	SessionID           uuid.UUID
	SessionTitle        string
	SessionStatus       SessionStatus
	RegistrationStartAt time.Time
	RegistrationEndAt   time.Time
	SessionStartAt      time.Time
	SessionEndAt        time.Time
	Capacity            int
	ConfirmedCount      int
	ActiveHoldCount     int
	GroupMinimum        int
	LowStockThreshold   *int
	PriceCents          int64

	DeliveryMode                 DeliveryMode
	Area                         AreaCode
	VenueName                    string
	Address                      string
	Longitude                    *float64
	Latitude                     *float64
	OnlineParticipationMode      string
	OnlineParticipationCompliant bool
}

type SessionDetailCTAAction string

const (
	SessionDetailCTAActionNone              SessionDetailCTAAction = "none"
	SessionDetailCTAActionStartRegistration SessionDetailCTAAction = "start_registration"
)

type SessionDetailCTALabel string

const (
	SessionDetailCTALabelActivityCancelled  SessionDetailCTALabel = "activity_cancelled"
	SessionDetailCTALabelComingSoon         SessionDetailCTALabel = "coming_soon"
	SessionDetailCTALabelActivityEnded      SessionDetailCTALabel = "activity_ended"
	SessionDetailCTALabelActivityInProgress SessionDetailCTALabel = "activity_in_progress"
	SessionDetailCTALabelNotOpen            SessionDetailCTALabel = "registration_not_open"
	SessionDetailCTALabelRegistrationClosed SessionDetailCTALabel = "registration_closed"
	SessionDetailCTALabelTryAgainLater      SessionDetailCTALabel = "try_again_later"
	SessionDetailCTALabelRegisterNow        SessionDetailCTALabel = "register_now"
	SessionDetailCTALabelBrandSuspended     SessionDetailCTALabel = "brand_suspended"
)

type SessionDetailCTA struct {
	Action  SessionDetailCTAAction
	Label   SessionDetailCTALabel
	Enabled bool
}

// SessionDetail contains exactly one Session identity. It intentionally has no
// Session collection or selector: routes with only Series/Instance context must
// resolve a unique public Session before invoking this projection.
type SessionDetail struct {
	SeriesID                         uuid.UUID
	InstanceID                       uuid.UUID
	SessionID                        uuid.UUID
	PublicationVersion               int64
	SuccessfulPublishedInstanceCount int
	HistoricalRegistrationCount      int64

	InstanceTitle       string
	SessionTitle        string
	ActivityType        ActivityType
	QuickTagCodes       []string
	CoverImageURL       string
	ContentBlocks       []DetailBlock
	RegistrationStartAt time.Time
	RegistrationEndAt   time.Time
	SessionStartAt      time.Time
	SessionEndAt        time.Time
	PriceCents          int64

	DeliveryMode            DeliveryMode
	Area                    AreaCode
	VenueName               string
	Address                 string
	Longitude               *float64
	Latitude                *float64
	OnlineParticipationMode string

	Display SessionDisplayDecision
	CTA     SessionDetailCTA
}

var (
	ErrInvalidSessionDetailFacts = errors.New("invalid xiangwan Session detail facts")
	ErrSessionDetailUnavailable  = errors.New("xiangwan Session detail unavailable")
)

type InvalidSessionDetailFactsError struct {
	Violations []FactViolation
}

func (err *InvalidSessionDetailFactsError) Error() string {
	if err == nil || len(err.Violations) == 0 {
		return ErrInvalidSessionDetailFacts.Error()
	}
	return ErrInvalidSessionDetailFacts.Error() + ": " +
		strings.Join(publicationViolationStrings(err.Violations), ", ")
}

func (*InvalidSessionDetailFactsError) Unwrap() error {
	return ErrInvalidSessionDetailFacts
}

func BuildSessionDetail(facts SessionDetailFacts, now time.Time) (SessionDetail, error) {
	identityViolations := make([]FactViolation, 0, 3)
	if facts.SeriesID == uuid.Nil {
		identityViolations = append(identityViolations, FactViolation{Field: "series_id", Code: ViolationRequired})
	}
	if facts.InstanceID == uuid.Nil {
		identityViolations = append(identityViolations, FactViolation{Field: "instance_id", Code: ViolationRequired})
	}
	if facts.SessionID == uuid.Nil {
		identityViolations = append(identityViolations, FactViolation{Field: "session_id", Code: ViolationRequired})
	}
	if len(identityViolations) > 0 {
		return SessionDetail{}, &InvalidSessionDetailFactsError{Violations: identityViolations}
	}
	if !sessionDetailFactsArePublic(facts) {
		return SessionDetail{}, ErrSessionDetailUnavailable
	}

	violations := validateSessionDetailFacts(facts, now)
	if len(violations) > 0 {
		return SessionDetail{}, &InvalidSessionDetailFactsError{Violations: violations}
	}
	display, err := DecideSessionDisplay(sessionDetailDisplayFacts(facts, now))
	if err != nil {
		return SessionDetail{}, fmt.Errorf("%w: display decision: %v", ErrInvalidSessionDetailFacts, err)
	}

	return SessionDetail{
		SeriesID:                         facts.SeriesID,
		InstanceID:                       facts.InstanceID,
		SessionID:                        facts.SessionID,
		PublicationVersion:               facts.PublicationVersion,
		SuccessfulPublishedInstanceCount: facts.SuccessfulPublishedInstanceCount,
		HistoricalRegistrationCount:      facts.HistoricalRegistrationCount,
		InstanceTitle:                    facts.InstanceTitle,
		SessionTitle:                     facts.SessionTitle,
		ActivityType:                     facts.InstanceActivityType,
		QuickTagCodes:                    append([]string(nil), facts.InstanceQuickTagCodes...),
		CoverImageURL:                    facts.InstanceCoverImageURL,
		ContentBlocks:                    append([]DetailBlock(nil), facts.InstanceDetailBlocks...),
		RegistrationStartAt:              facts.RegistrationStartAt.UTC(),
		RegistrationEndAt:                facts.RegistrationEndAt.UTC(),
		SessionStartAt:                   facts.SessionStartAt.UTC(),
		SessionEndAt:                     facts.SessionEndAt.UTC(),
		PriceCents:                       facts.PriceCents,
		DeliveryMode:                     facts.DeliveryMode,
		Area:                             facts.Area,
		VenueName:                        facts.VenueName,
		Address:                          facts.Address,
		Longitude:                        cloneFloat64(facts.Longitude),
		Latitude:                         cloneFloat64(facts.Latitude),
		OnlineParticipationMode:          facts.OnlineParticipationMode,
		Display:                          display,
		CTA:                              sessionDetailCTAForDisplay(display.State),
	}, nil
}

func sessionDetailFactsArePublic(facts SessionDetailFacts) bool {
	if facts.SeriesStatus != SeriesStatusActive {
		return false
	}
	switch facts.InstanceStatus {
	case InstanceStatusPublished, InstanceStatusCompleted, InstanceStatusCancelled:
	default:
		return false
	}
	switch facts.SessionStatus {
	case SessionStatusPublished, SessionStatusEnded, SessionStatusCancelled:
		return true
	default:
		return false
	}
}

func validateSessionDetailFacts(facts SessionDetailFacts, now time.Time) []FactViolation {
	violations := make([]FactViolation, 0)
	if strings.TrimSpace(facts.InstanceTitle) == "" {
		violations = append(violations, FactViolation{Field: "instance_title", Code: ViolationMustNotBeBlank})
	}
	if strings.TrimSpace(facts.SessionTitle) == "" {
		violations = append(violations, FactViolation{Field: "session_title", Code: ViolationMustNotBeBlank})
	}
	if facts.PublicationVersion < 1 {
		violations = append(violations, FactViolation{Field: "publication_version", Code: ViolationMustBePositive})
	}
	if facts.SuccessfulPublishedInstanceCount < 0 {
		violations = append(violations, FactViolation{Field: "successful_published_instance_count", Code: ViolationMustBeNonNegative})
	}
	if facts.HistoricalRegistrationCount < 0 {
		violations = append(violations, FactViolation{Field: "historical_registration_count", Code: ViolationMustBeNonNegative})
	}
	if !knownActivityType(facts.InstanceActivityType, false) {
		violations = append(violations, FactViolation{Field: "activity_type", Code: ViolationInvalidChoice})
	}
	if len(facts.InstanceQuickTagCodes) > 20 {
		violations = append(violations, FactViolation{Field: "quick_tag_codes", Code: ViolationOutOfRange})
	}
	// The PostgreSQL read projects stored detail_blocks through
	// ProjectDetailBlocks before they reach facts, so an invalid element here
	// is a facts conflict, never something to silently drop again.
	if len(facts.InstanceDetailBlocks) > MaxInstanceDetailBlocks {
		violations = append(violations, FactViolation{Field: "detail_blocks", Code: ViolationOutOfRange})
	}
	for index, block := range facts.InstanceDetailBlocks {
		if !ValidDetailBlock(block) {
			violations = append(violations, FactViolation{
				Field: "detail_blocks[" + strconv.Itoa(index) + "]",
				Code:  ViolationInvalidChoice,
			})
		}
	}
	seenQuickTags := make(map[string]struct{}, len(facts.InstanceQuickTagCodes))
	for index, code := range facts.InstanceQuickTagCodes {
		field := "quick_tag_codes[" + strconv.Itoa(index) + "]"
		if !homeTagCodePattern.MatchString(code) {
			violations = append(violations, FactViolation{Field: field, Code: ViolationInvalidChoice})
		} else if _, duplicate := seenQuickTags[code]; duplicate {
			violations = append(violations, FactViolation{Field: field, Code: ViolationDuplicate})
		} else {
			seenQuickTags[code] = struct{}{}
		}
	}
	if facts.PriceCents < 0 {
		violations = append(violations, FactViolation{Field: "price_cents", Code: ViolationMustBeNonNegative})
	}

	deliveryCandidate := SessionPublicationCandidate{
		DeliveryMode: facts.DeliveryMode,
		Area:         facts.Area,
	}
	if facts.DeliveryMode == DeliveryModeOffline {
		deliveryCandidate.OfflineLocation = &OfflineLocation{
			VenueName: facts.VenueName,
			Address:   facts.Address,
			Longitude: facts.Longitude,
			Latitude:  facts.Latitude,
		}
	}
	if facts.DeliveryMode == DeliveryModeOnline {
		deliveryCandidate.OnlineParticipation = &OnlineParticipation{
			Mode:      facts.OnlineParticipationMode,
			Compliant: facts.OnlineParticipationCompliant,
		}
	}
	violations = append(violations, validateDelivery("", deliveryCandidate)...)
	violations = append(violations, ValidateSessionDisplayFacts(sessionDetailDisplayFacts(facts, now))...)
	return violations
}

func sessionDetailDisplayFacts(facts SessionDetailFacts, now time.Time) SessionDisplayFacts {
	return SessionDisplayFacts{
		InstanceCancelled:          facts.InstanceStatus == InstanceStatusCancelled,
		SessionCancelled:           facts.SessionStatus == SessionStatusCancelled,
		SeriesInRecurringGap:       facts.SeriesIsRecurring && facts.CurrentPublicInstance && facts.InstanceStatus == InstanceStatusCompleted,
		Now:                        now.UTC(),
		RegistrationStartAt:        facts.RegistrationStartAt.UTC(),
		RegistrationEndAt:          facts.RegistrationEndAt.UTC(),
		SessionStartAt:             facts.SessionStartAt.UTC(),
		SessionEndAt:               facts.SessionEndAt.UTC(),
		Capacity:                   facts.Capacity,
		ConfirmedRegistrationCount: facts.ConfirmedCount,
		ActiveHoldCount:            facts.ActiveHoldCount,
		GroupMinimum:               facts.GroupMinimum,
		LowStockThreshold:          facts.LowStockThreshold,
	}
}

func sessionDetailCTAForDisplay(state DisplayState) SessionDetailCTA {
	cta := SessionDetailCTA{Action: SessionDetailCTAActionNone}
	switch state {
	case DisplayStateCancelled:
		cta.Label = SessionDetailCTALabelActivityCancelled
	case DisplayStateRecurringGap:
		cta.Label = SessionDetailCTALabelComingSoon
	case DisplayStateEnded:
		cta.Label = SessionDetailCTALabelActivityEnded
	case DisplayStateInProgress:
		cta.Label = SessionDetailCTALabelActivityInProgress
	case DisplayStateNotOpen:
		cta.Label = SessionDetailCTALabelNotOpen
	case DisplayStateClosed, DisplayStateFull:
		cta.Label = SessionDetailCTALabelRegistrationClosed
	case DisplayStateTemporarilyLockedFull:
		cta.Label = SessionDetailCTALabelTryAgainLater
	case DisplayStateOpenLowStock, DisplayStateOpenNeedGroup, DisplayStateOpen:
		cta.Action = SessionDetailCTAActionStartRegistration
		cta.Label = SessionDetailCTALabelRegisterNow
		cta.Enabled = true
	}
	return cta
}

func cloneFloat64(value *float64) *float64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
