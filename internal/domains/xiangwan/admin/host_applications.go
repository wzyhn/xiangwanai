package xiangwanadmin

import (
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"context"
	"github.com/google/uuid"
	"time"
)

type HostRuleVersion struct {
	Version          int64     `json:"version"`
	ApplicationCycle string    `json:"application_cycle"`
	PolicyVersion    string    `json:"policy_version"`
	Requirements     string    `json:"requirements"`
	Benefits         string    `json:"benefits"`
	Enabled          bool      `json:"enabled"`
	ApprovedAt       time.Time `json:"approved_at"`
}
type PublishHostRulesCommand struct {
	ActorID, IdentityLinkID, OperationID uuid.UUID
	RequestID, Reason                    string
	ExpectedVersion                      int64
	Rules                                HostRuleVersion
}
type HostApplicationItem struct {
	ID               uuid.UUID                    `json:"id"`
	ApplicationCycle string                       `json:"application_cycle"`
	PolicyVersion    string                       `json:"policy_version"`
	Status           people.HostApplicationStatus `json:"status"`
	ReviewComment    string                       `json:"review_comment,omitempty"`
	Version          int64                        `json:"version"`
	SubmittedAt      time.Time                    `json:"submitted_at"`
	UpdatedAt        time.Time                    `json:"updated_at"`
}
type HostApplicationDetail struct {
	HostApplicationItem
	PersonalIntroduction string `json:"personal_introduction"`
	RelevantExperience   string `json:"relevant_experience"`
	Availability         string `json:"availability"`
	ContactMethod        string `json:"contact_method"`
}

func (HostApplicationDetail) String() string {
	return "HostApplicationDetail{private_fields:[REDACTED]}"
}
func (v HostApplicationDetail) GoString() string { return v.String() }

type HostApplicationPage struct {
	Items    []HostApplicationItem `json:"items"`
	Page     int                   `json:"page"`
	PageSize int                   `json:"page_size"`
	Total    int64                 `json:"total"`
}
type ReviewHostApplicationCommand struct {
	ActorID, IdentityLinkID, OperationID, ApplicationID uuid.UUID
	RequestID, Comment                                  string
	ExpectedVersion                                     int64
	Decision                                            people.HostApplicationStatus
}
type HostApplicationAdministration interface {
	GetHostRules(context.Context, Principal) (HostRuleVersion, error)
	PublishHostRules(context.Context, PublishHostRulesCommand) (HostRuleVersion, error)
	ListHostApplications(context.Context, Principal, string, int, int) (HostApplicationPage, error)
	GetHostApplication(context.Context, Principal, uuid.UUID, string, string) (HostApplicationDetail, error)
	ReviewHostApplication(context.Context, ReviewHostApplicationCommand) (HostApplicationItem, error)
}
