package activity

import (
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

type DeliveryMode string

const (
	DeliveryModeOffline DeliveryMode = "offline"
	DeliveryModeOnline  DeliveryMode = "online"
)

type OfflineLocation struct {
	VenueName string
	Address   string
	Longitude *float64
	Latitude  *float64
}

type OnlineParticipation struct {
	Mode      string
	Compliant bool
}

// PublicationReferenceReadiness records results from the authoritative owners
// of references used by a Session. A false value is fail-closed; this package
// never guesses visibility or ownership from an ID.
type PublicationReferenceReadiness struct {
	QuestionnaireReady bool
	PeopleReady        bool
	ContentReady       bool
	ResourcesReady     bool
	QuickTagsReady     bool
}

type SessionPublicationCandidate struct {
	SessionID uuid.UUID
	Title     string

	RegistrationStartAt time.Time
	RegistrationEndAt   time.Time
	SessionStartAt      time.Time
	SessionEndAt        time.Time

	Capacity          int
	GroupMinimum      int
	LowStockThreshold *int
	PriceCents        int64

	DeliveryMode        DeliveryMode
	Area                AreaCode
	OfflineLocation     *OfflineLocation
	OnlineParticipation *OnlineParticipation
	References          PublicationReferenceReadiness
}

type InstancePublicationCandidate struct {
	SeriesID      uuid.UUID
	InstanceID    uuid.UUID
	ActivityType  ActivityType
	QuickTagCodes []string
	Sessions      []SessionPublicationCandidate
}

var ErrInvalidInstancePublication = errors.New("invalid instance publication")

type InvalidInstancePublicationError struct {
	Violations []FactViolation
}

func (err *InvalidInstancePublicationError) Error() string {
	if err == nil || len(err.Violations) == 0 {
		return ErrInvalidInstancePublication.Error()
	}
	return ErrInvalidInstancePublication.Error() + ": " +
		strings.Join(publicationViolationStrings(err.Violations), ", ")
}

func (*InvalidInstancePublicationError) Unwrap() error {
	return ErrInvalidInstancePublication
}

// CheckInstancePublication returns a typed error containing every stable field
// violation. It performs no writes and has no infrastructure dependency.
func CheckInstancePublication(candidate InstancePublicationCandidate) error {
	violations := ValidateInstancePublication(candidate)
	if len(violations) == 0 {
		return nil
	}
	return &InvalidInstancePublicationError{Violations: violations}
}

// ValidQuickTagCodes applies the canonical draft and publication constraints
// before a database write reaches the matching PostgreSQL CHECK constraint.
func ValidQuickTagCodes(codes []string) bool {
	if len(codes) > 20 {
		return false
	}
	seen := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		if !homeTagCodePattern.MatchString(code) {
			return false
		}
		if _, duplicate := seen[code]; duplicate {
			return false
		}
		seen[code] = struct{}{}
	}
	return true
}

// ValidateInstancePublication validates the complete Instance in deterministic
// Session order. At least one fully valid Session is required before a publish
// transaction may create a publication fact or switch the Series pointer.
func ValidateInstancePublication(candidate InstancePublicationCandidate) []FactViolation {
	violations := make([]FactViolation, 0)
	if candidate.SeriesID == uuid.Nil {
		violations = append(violations, FactViolation{Field: "series_id", Code: ViolationRequired})
	}
	if candidate.InstanceID == uuid.Nil {
		violations = append(violations, FactViolation{Field: "instance_id", Code: ViolationRequired})
	}
	if !knownActivityType(candidate.ActivityType, false) {
		violations = append(violations, FactViolation{Field: "activity_type", Code: ViolationInvalidChoice})
	}
	if len(candidate.QuickTagCodes) > 20 {
		violations = append(violations, FactViolation{Field: "quick_tag_codes", Code: ViolationOutOfRange})
	}
	seenQuickTagCodes := make(map[string]struct{}, len(candidate.QuickTagCodes))
	for index, code := range candidate.QuickTagCodes {
		field := "quick_tag_codes[" + strconv.Itoa(index) + "]"
		if !homeTagCodePattern.MatchString(code) {
			violations = append(violations, FactViolation{Field: field, Code: ViolationInvalidChoice})
		} else if _, duplicate := seenQuickTagCodes[code]; duplicate {
			violations = append(violations, FactViolation{Field: field, Code: ViolationDuplicate})
		} else {
			seenQuickTagCodes[code] = struct{}{}
		}
	}
	if len(candidate.Sessions) == 0 {
		return append(violations, FactViolation{Field: "sessions", Code: ViolationRequired})
	}

	seenSessionIDs := make(map[uuid.UUID]struct{}, len(candidate.Sessions))
	for index, session := range candidate.Sessions {
		prefix := "sessions[" + strconv.Itoa(index) + "]."
		if session.SessionID == uuid.Nil {
			violations = append(violations, FactViolation{Field: prefix + "session_id", Code: ViolationRequired})
		} else if _, duplicate := seenSessionIDs[session.SessionID]; duplicate {
			violations = append(violations, FactViolation{Field: prefix + "session_id", Code: ViolationDuplicate})
		} else {
			seenSessionIDs[session.SessionID] = struct{}{}
		}
		if strings.TrimSpace(session.Title) == "" {
			violations = append(violations, FactViolation{Field: prefix + "title", Code: ViolationMustNotBeBlank})
		}

		definitionViolations := validateSessionDefinition(
			session.RegistrationStartAt,
			session.RegistrationEndAt,
			session.SessionStartAt,
			session.SessionEndAt,
			session.Capacity,
			session.GroupMinimum,
			session.LowStockThreshold,
		)
		violations = append(violations, prefixPublicationViolations(prefix, definitionViolations)...)
		if session.PriceCents < 0 {
			violations = append(violations, FactViolation{Field: prefix + "price_cents", Code: ViolationMustBeNonNegative})
		}

		violations = append(violations, validateDelivery(prefix, session)...)
		violations = append(violations, validatePublicationReferences(prefix, session.References)...)
	}
	return violations
}

func validateDelivery(prefix string, session SessionPublicationCandidate) []FactViolation {
	switch session.DeliveryMode {
	case DeliveryModeOffline:
		if !knownAreaCode(session.Area, false) || session.Area == AreaCodeOnline {
			return []FactViolation{{Field: prefix + "area", Code: ViolationInvalidChoice}}
		}
		if session.OfflineLocation == nil {
			return []FactViolation{{Field: prefix + "offline_location", Code: ViolationRequired}}
		}
		violations := make([]FactViolation, 0)
		if strings.TrimSpace(session.OfflineLocation.VenueName) == "" {
			violations = append(violations, FactViolation{Field: prefix + "offline_location.venue_name", Code: ViolationMustNotBeBlank})
		}
		if strings.TrimSpace(session.OfflineLocation.Address) == "" {
			violations = append(violations, FactViolation{Field: prefix + "offline_location.address", Code: ViolationMustNotBeBlank})
		}
		if session.OfflineLocation.Longitude == nil {
			violations = append(violations, FactViolation{Field: prefix + "offline_location.longitude", Code: ViolationRequired})
		} else if math.IsNaN(*session.OfflineLocation.Longitude) ||
			math.IsInf(*session.OfflineLocation.Longitude, 0) ||
			*session.OfflineLocation.Longitude < -180 ||
			*session.OfflineLocation.Longitude > 180 {
			violations = append(violations, FactViolation{Field: prefix + "offline_location.longitude", Code: ViolationOutOfRange})
		}
		if session.OfflineLocation.Latitude == nil {
			violations = append(violations, FactViolation{Field: prefix + "offline_location.latitude", Code: ViolationRequired})
		} else if math.IsNaN(*session.OfflineLocation.Latitude) ||
			math.IsInf(*session.OfflineLocation.Latitude, 0) ||
			*session.OfflineLocation.Latitude < -90 ||
			*session.OfflineLocation.Latitude > 90 {
			violations = append(violations, FactViolation{Field: prefix + "offline_location.latitude", Code: ViolationOutOfRange})
		}
		return violations
	case DeliveryModeOnline:
		if session.Area != AreaCodeOnline {
			return []FactViolation{{Field: prefix + "area", Code: ViolationInvalidChoice}}
		}
		if session.OnlineParticipation == nil {
			return []FactViolation{{Field: prefix + "online_participation", Code: ViolationRequired}}
		}
		violations := make([]FactViolation, 0)
		if strings.TrimSpace(session.OnlineParticipation.Mode) == "" {
			violations = append(violations, FactViolation{Field: prefix + "online_participation.mode", Code: ViolationMustNotBeBlank})
		}
		if !session.OnlineParticipation.Compliant {
			violations = append(violations, FactViolation{Field: prefix + "online_participation.compliant", Code: ViolationNotReady})
		}
		return violations
	default:
		return []FactViolation{{Field: prefix + "delivery_mode", Code: ViolationInvalidChoice}}
	}
}

func validatePublicationReferences(prefix string, readiness PublicationReferenceReadiness) []FactViolation {
	violations := make([]FactViolation, 0, 5)
	for _, reference := range []struct {
		field string
		ready bool
	}{
		{field: "references.questionnaire", ready: readiness.QuestionnaireReady},
		{field: "references.people", ready: readiness.PeopleReady},
		{field: "references.content", ready: readiness.ContentReady},
		{field: "references.resources", ready: readiness.ResourcesReady},
		{field: "references.quick_tags", ready: readiness.QuickTagsReady},
	} {
		if !reference.ready {
			violations = append(violations, FactViolation{Field: prefix + reference.field, Code: ViolationNotReady})
		}
	}
	return violations
}

func prefixPublicationViolations(prefix string, violations []FactViolation) []FactViolation {
	prefixed := make([]FactViolation, 0, len(violations))
	for _, violation := range violations {
		prefixed = append(prefixed, FactViolation{Field: prefix + violation.Field, Code: violation.Code})
	}
	return prefixed
}

func publicationViolationStrings(violations []FactViolation) []string {
	formatted := make([]string, 0, len(violations))
	for _, violation := range violations {
		formatted = append(formatted, violation.Field+":"+string(violation.Code))
	}
	return formatted
}
