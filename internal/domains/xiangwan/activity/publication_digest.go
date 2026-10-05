package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// DigestInstancePublication returns a stable SHA-256 digest of the complete
// validated public projection. Session input order and time zones do not alter
// the digest; changing a public fact does.
func DigestInstancePublication(candidate InstancePublicationCandidate) (string, error) {
	if err := CheckInstancePublication(candidate); err != nil {
		return "", err
	}

	canonical := canonicalPublication{
		SeriesID:      candidate.SeriesID.String(),
		InstanceID:    candidate.InstanceID.String(),
		ActivityType:  candidate.ActivityType,
		QuickTagCodes: append([]string(nil), candidate.QuickTagCodes...),
		Sessions:      make([]canonicalPublicationSession, 0, len(candidate.Sessions)),
	}
	sort.Strings(canonical.QuickTagCodes)
	for _, session := range candidate.Sessions {
		canonical.Sessions = append(canonical.Sessions, canonicalizePublicationSession(session))
	}
	sort.Slice(canonical.Sessions, func(left, right int) bool {
		return canonical.Sessions[left].SessionID < canonical.Sessions[right].SessionID
	})

	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical instance publication: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type canonicalPublication struct {
	SeriesID      string                        `json:"series_id"`
	InstanceID    string                        `json:"instance_id"`
	ActivityType  ActivityType                  `json:"activity_type"`
	QuickTagCodes []string                      `json:"quick_tag_codes"`
	Sessions      []canonicalPublicationSession `json:"sessions"`
}

type canonicalPublicationSession struct {
	SessionID                   string                        `json:"session_id"`
	Title                       string                        `json:"title"`
	RegistrationStartAt         string                        `json:"registration_start_at"`
	RegistrationEndAt           string                        `json:"registration_end_at"`
	SessionStartAt              string                        `json:"session_start_at"`
	SessionEndAt                string                        `json:"session_end_at"`
	Capacity                    int                           `json:"capacity"`
	GroupMinimum                int                           `json:"group_minimum"`
	LowStockThreshold           *int                          `json:"low_stock_threshold"`
	PriceCents                  int64                         `json:"price_cents"`
	DeliveryMode                DeliveryMode                  `json:"delivery_mode"`
	Area                        AreaCode                      `json:"area"`
	OfflineLocation             *canonicalOfflineLocation     `json:"offline_location"`
	OnlineParticipation         *canonicalOnlineParticipation `json:"online_participation"`
	QuestionnaireReferenceReady bool                          `json:"questionnaire_reference_ready"`
	PeopleReferenceReady        bool                          `json:"people_reference_ready"`
	ContentReferenceReady       bool                          `json:"content_reference_ready"`
	ResourcesReferenceReady     bool                          `json:"resources_reference_ready"`
	QuickTagsReferenceReady     bool                          `json:"quick_tags_reference_ready"`
}

type canonicalOfflineLocation struct {
	VenueName string   `json:"venue_name"`
	Address   string   `json:"address"`
	Longitude *float64 `json:"longitude"`
	Latitude  *float64 `json:"latitude"`
}

type canonicalOnlineParticipation struct {
	Mode      string `json:"mode"`
	Compliant bool   `json:"compliant"`
}

func canonicalizePublicationSession(session SessionPublicationCandidate) canonicalPublicationSession {
	canonical := canonicalPublicationSession{
		SessionID:                   session.SessionID.String(),
		Title:                       session.Title,
		RegistrationStartAt:         canonicalPublicationTime(session.RegistrationStartAt),
		RegistrationEndAt:           canonicalPublicationTime(session.RegistrationEndAt),
		SessionStartAt:              canonicalPublicationTime(session.SessionStartAt),
		SessionEndAt:                canonicalPublicationTime(session.SessionEndAt),
		Capacity:                    session.Capacity,
		GroupMinimum:                session.GroupMinimum,
		LowStockThreshold:           session.LowStockThreshold,
		PriceCents:                  session.PriceCents,
		DeliveryMode:                session.DeliveryMode,
		Area:                        session.Area,
		QuestionnaireReferenceReady: session.References.QuestionnaireReady,
		PeopleReferenceReady:        session.References.PeopleReady,
		ContentReferenceReady:       session.References.ContentReady,
		ResourcesReferenceReady:     session.References.ResourcesReady,
		QuickTagsReferenceReady:     session.References.QuickTagsReady,
	}
	if session.OfflineLocation != nil {
		canonical.OfflineLocation = &canonicalOfflineLocation{
			VenueName: session.OfflineLocation.VenueName,
			Address:   session.OfflineLocation.Address,
			Longitude: session.OfflineLocation.Longitude,
			Latitude:  session.OfflineLocation.Latitude,
		}
	}
	if session.OnlineParticipation != nil {
		canonical.OnlineParticipation = &canonicalOnlineParticipation{
			Mode:      session.OnlineParticipation.Mode,
			Compliant: session.OnlineParticipation.Compliant,
		}
	}
	return canonical
}

func canonicalPublicationTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
