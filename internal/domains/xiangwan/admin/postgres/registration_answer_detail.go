package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

const registrationAnswerDetailSQL = `SELECT
    answer.questionnaire_version_id, answer.field_id, answer.field_code,
    answer.field_type, answer.field_label, answer.is_required,
    answer.sort_order, answer.options, answer.answer_values, answer.created_at
FROM xiangwan_registration_answers AS answer
WHERE answer.tenant_id = $1 AND answer.registration_id = $2
ORDER BY answer.sort_order ASC
LIMIT 101`

// GetRegistrationAnswers reads raw answer values only for one exact
// Registration. The operator grant is locked through the same transaction as
// the row read and the content-free audit event commits before the response.
func (catalog *Catalog) GetRegistrationAnswers(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	registrationID uuid.UUID,
	purpose string,
) (xiangwanadmin.RegistrationAnswerDetailSet, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || registrationID == uuid.Nil ||
		(purpose != "activity_coordination" && purpose != "event_followup") {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, err
	}
	defer func() { _ = tx.Rollback() }()
	value := xiangwanadmin.RegistrationAnswerDetailSet{RegistrationID: registrationID}
	if err := tx.QueryRowContext(ctx, `
SELECT instance_id, session_id
FROM xiangwan_registrations
WHERE tenant_id = $1 AND id = $2`, catalog.tenantID, registrationID).Scan(
		&value.InstanceID, &value.SessionID,
	); errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, xiangwanadmin.ErrTargetNotFound
	} else if err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("locate questionnaire registration: %w", err)
	}
	rows, err := tx.QueryContext(ctx, registrationAnswerDetailSQL, catalog.tenantID, registrationID)
	if err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("read questionnaire answers: %w", err)
	}
	defer func() { _ = rows.Close() }()
	value.Items = make([]xiangwanadmin.RegistrationAnswerDetail, 0)
	for rows.Next() {
		var item xiangwanadmin.RegistrationAnswerDetail
		var fieldType string
		var optionsJSON, valuesJSON []byte
		if err := rows.Scan(
			&item.QuestionnaireVersionID, &item.FieldID, &item.FieldCode,
			&fieldType, &item.FieldLabel, &item.Required,
			&item.SortOrder, &optionsJSON, &valuesJSON, &item.CreatedAt,
		); err != nil {
			return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("scan questionnaire answer: %w", err)
		}
		item.FieldType = activity.QuestionnaireFieldType(fieldType)
		if err := json.Unmarshal(optionsJSON, &item.Options); err != nil {
			return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("decode questionnaire option snapshot: %w", err)
		}
		if err := json.Unmarshal(valuesJSON, &item.Values); err != nil {
			return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("decode questionnaire answer snapshot: %w", err)
		}
		value.Items = append(value.Items, item)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("iterate questionnaire answers: %w", err)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("close questionnaire answers: %w", err)
	}
	if len(value.Items) > activity.MaxQuestionnaireFields {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("questionnaire answer field count exceeds contract")
	}
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	now := catalog.now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'registration.answer_detail_read', 'registration', $4,
    $5, jsonb_build_object('identity_link_id', $6::TEXT, 'purpose', $7::TEXT,
    'field_count', $8::INTEGER), $9, $9)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, registrationID,
		requestID, principal.IdentityLinkID.String(), purpose, len(value.Items), now); err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("audit questionnaire answer read: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.RegistrationAnswerDetailSet{}, fmt.Errorf("commit questionnaire answer read: %w", err)
	}
	return value, nil
}
