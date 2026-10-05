package xiangwanapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	identitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/identity/postgres"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"
	"github.com/wzyhn/xiangwanai/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type registrationContactApplication interface {
	Read(context.Context, uuid.UUID) (identitypostgres.RegistrationContact, error)
	Update(context.Context, uuid.UUID, identitypostgres.RegistrationContactUpdate) (identitypostgres.RegistrationContact, error)
}

func (handler *ConsumerIdentityHandler) SetRegistrationContactStore(store registrationContactApplication) {
	handler.registrationContact = store
}

// GetRegistrationContact godoc
// @Summary Read the authenticated consumer's private registration defaults
// @Description Private no-store read. Tenant is runtime-owned and Principal is session-derived. Phone is manually entered and never treated as verified identity; public profiles do not contain it.
// @Tags xiangwan
// @Produce json
// @Security BearerAuth
// @Success 200 {object} identitypostgres.RegistrationContact
// @Failure 401 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/me/registration-contact [get]
func (handler *ConsumerIdentityHandler) GetRegistrationContact(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if handler == nil || handler.registrationContact == nil || handler.principal == nil {
		writeConsumerIdentityError(c, ErrNicknameModerationUnavailable)
		return
	}
	if !emptyConsumerProfileReadRequest(c) {
		writeError(c, errx.NewBadRequest("invalid contact request"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	value, err := handler.registrationContact.Read(c.Request.Context(), principalID)
	if err != nil {
		writeRegistrationContactError(c, err)
		return
	}
	response.OK(c, value)
}

type RegistrationContactHTTPRequest struct {
	Nickname             *string `json:"nickname"`
	PhoneE164            *string `json:"phone_e164"`
	PrincipalProfileETag *string `json:"principal_profile_etag"`
	ExpectedVersion      *int64  `json:"expected_version"`
	PrivacyPolicyVersion *string `json:"privacy_policy_version"`
	ContactPolicyVersion *string `json:"contact_policy_version"`
}

// UpdateRegistrationContact godoc
// @Summary Save the authenticated consumer's nickname and private phone
// @Description BUSINESS-STATE command. Content-moderated nickname and unverified contact phone commit atomically behind Principal profile ETag, contact version, current published policy versions and active runtime generation. Unknown results reconcile through the exact private read; existing registration snapshots remain unchanged.
// @Tags xiangwan
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body RegistrationContactHTTPRequest true "Required nickname, phone, versions and policy acknowledgement"
// @Success 200 {object} identitypostgres.RegistrationContact
// @Failure 400 {object} response.Body
// @Failure 401 {object} response.Body
// @Failure 409 {object} response.Body
// @Failure 503 {object} response.Body
// @Router /xiangwan/me/registration-contact [patch]
func (handler *ConsumerIdentityHandler) UpdateRegistrationContact(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if handler == nil || handler.registrationContact == nil || handler.principal == nil {
		writeConsumerIdentityError(c, ErrNicknameModerationUnavailable)
		return
	}
	if len(c.Request.URL.Query()) != 0 || c.ContentType() != "application/json" || len(c.Request.Header.Values("Idempotency-Key")) != 0 {
		writeError(c, errx.NewBadRequest("invalid contact request"))
		return
	}
	body, readErr := io.ReadAll(io.LimitReader(c.Request.Body, 4097))
	if readErr != nil || len(body) > 4096 {
		writeError(c, errx.NewBadRequest("invalid contact request"))
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var input RegistrationContactHTTPRequest
	if err := decoder.Decode(&input); err != nil {
		writeError(c, errx.NewBadRequest("invalid contact request"))
		return
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || input.Nickname == nil || input.PhoneE164 == nil ||
		input.PrincipalProfileETag == nil || input.ExpectedVersion == nil || input.PrivacyPolicyVersion == nil || input.ContactPolicyVersion == nil {
		writeError(c, errx.NewBadRequest("incomplete contact request"))
		return
	}
	nickname, valid := normalizeConsumerNickname(*input.Nickname)
	if !valid {
		writeError(c, errx.NewBadRequest("invalid nickname"))
		return
	}
	principalID, err := handler.principal(c)
	if err != nil {
		writeError(c, err)
		return
	}
	if handler.nicknameModerator == nil {
		writeConsumerIdentityError(c, ErrNicknameModerationUnavailable)
		return
	}
	if err := handler.nicknameModerator.CheckNickname(c.Request.Context(), principalID, nickname); err != nil {
		writeConsumerIdentityError(c, err)
		return
	}
	value, err := handler.registrationContact.Update(c.Request.Context(), principalID, identitypostgres.RegistrationContactUpdate{
		Nickname: nickname, PhoneE164: *input.PhoneE164, PrincipalProfileETag: *input.PrincipalProfileETag,
		ExpectedVersion: *input.ExpectedVersion, PrivacyPolicyVersion: *input.PrivacyPolicyVersion, ContactPolicyVersion: *input.ContactPolicyVersion,
	})
	if err != nil {
		writeRegistrationContactError(c, err)
		return
	}
	response.OK(c, value)
}

func writeRegistrationContactError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, identitypostgres.ErrInvalidRegistrationContact):
		writeError(c, errx.NewBadRequest("invalid contact information"))
	case errors.Is(err, identitypostgres.ErrRegistrationContactConflict), errors.Is(err, identitypostgres.ErrRegistrationContactPolicyConflict):
		writeError(c, errx.NewConflict("contact information or policy changed; refresh before saving"))
	case errors.Is(err, identitypostgres.ErrPrincipalUnavailable):
		writeConsumerIdentityError(c, err)
	default:
		writeError(c, errx.NewInternal("registration contact operation failed"))
	}
}
