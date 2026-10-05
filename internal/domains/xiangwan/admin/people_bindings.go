package xiangwanadmin

import (
	"context"
	"github.com/google/uuid"
	"time"
)

type PeopleBindingInvitation struct {
	ID              uuid.UUID `json:"id"`
	PeopleProfileID uuid.UUID `json:"people_profile_id"`
	ProfileVersion  int64     `json:"profile_version"`
	Status          string    `json:"status"`
	Version         int64     `json:"version"`
	ExpiresAt       time.Time `json:"expires_at"`
	Code            string    `json:"code,omitempty"`
}
type CreatePeopleBindingInvitationCommand struct {
	ActorID, IdentityLinkID, OperationID, PeopleProfileID uuid.UUID
	RequestID, Reason                                     string
	ExpectedVersion                                       int64
}
type RevokePeopleBindingCommand struct {
	ActorID, IdentityLinkID, OperationID, PeopleProfileID, BindingID uuid.UUID
	RequestID, Reason                                                string
	ExpectedVersion                                                  int64
}
type PeopleBindingDetail struct {
	ID              uuid.UUID `json:"id"`
	PeopleProfileID uuid.UUID `json:"people_profile_id"`
	Status          string    `json:"status"`
	Version         int64     `json:"version"`
}
type PeopleBindingAdministration interface {
	CreatePeopleBindingInvitation(context.Context, CreatePeopleBindingInvitationCommand) (PeopleBindingInvitation, error)
	GetPeopleBinding(context.Context, Principal, uuid.UUID, string) (PeopleBindingDetail, error)
	RevokePeopleBinding(context.Context, RevokePeopleBindingCommand) (PeopleBindingDetail, error)
	RevokePeopleBindingInvitation(context.Context, RevokePeopleBindingCommand) (PeopleBindingInvitation, error)
}
