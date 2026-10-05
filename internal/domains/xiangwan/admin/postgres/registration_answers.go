package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

// ListRegistrationAnswerSummaries returns only completion metadata for
// questionnaire answers. Raw answer_values are intentionally never scanned
// into Go or returned to the administrator boundary: free-text answers may
// contain personal or otherwise sensitive information.
func (catalog *Catalog) ListRegistrationAnswerSummaries(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.RegistrationAnswerSummaryFilter,
) (xiangwanadmin.RegistrationAnswerSummaryPage, error) {
	page, pageSize, err := normalizePage(filter.Page, filter.PageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || err != nil ||
		!validOptionalID(filter.InstanceID) || !validOptionalID(filter.SessionID) ||
		!validOptionalID(filter.RegistrationID) ||
		(filter.InstanceID == nil && filter.SessionID == nil && filter.RegistrationID == nil) ||
		(filter.Format != "" && filter.Format != "json" && filter.Format != "csv") {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	if filter.Format == "" {
		filter.Format = "json"
	}

	// beginAuthorizedRead locks the live tenant activity-operator grant and
	// then keeps the query in one repeatable-read snapshot. This endpoint is
	// intentionally unavailable to onsite-only grants because answer metadata
	// is an operator activity-management concern.
	tx, txErr := catalog.beginAuthorizedRead(ctx, principal)
	if txErr != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, txErr
	}
	defer func() { _ = tx.Rollback() }()
	args := []any{
		catalog.tenantID,
		nullableUUID(filter.InstanceID),
		nullableUUID(filter.SessionID),
		nullableUUID(filter.RegistrationID),
	}
	var total int64
	if err := tx.QueryRowContext(ctx, registrationAnswerSummaryCountSQL, args...).Scan(&total); err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"count administrator questionnaire answer summaries: %w", err,
		)
	}
	rows, err := tx.QueryContext(ctx, registrationAnswerSummaryListSQL,
		append(args, (page-1)*pageSize, pageSize)...)
	if err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"list administrator questionnaire answer summaries: %w", err,
		)
	}
	defer func() { _ = rows.Close() }()
	items := make([]xiangwanadmin.RegistrationAnswerSummary, 0)
	for rows.Next() {
		var item xiangwanadmin.RegistrationAnswerSummary
		var fieldType string
		if err := rows.Scan(
			&item.RegistrationID, &item.InstanceID, &item.SessionID,
			&item.QuestionnaireVersionID, &item.FieldID, &item.FieldCode,
			&fieldType, &item.FieldLabel, &item.Required, &item.SortOrder,
			&item.Answered, &item.ValueCount, &item.CreatedAt,
		); err != nil {
			return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
				"scan administrator questionnaire answer summary: %w", err,
			)
		}
		item.FieldType = activity.QuestionnaireFieldType(fieldType)
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"iterate administrator questionnaire answer summaries: %w", err,
		)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"close administrator questionnaire answer summaries: %w", err,
		)
	}

	// The audit event records the filter, page and result count only. It never
	// includes answer values, contact snapshots or free-text content.
	requestID := requestctx.RequestID(ctx)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	targetID, targetType := registrationAnswerAuditTarget(filter)
	details, err := json.Marshal(map[string]any{
		"identity_link_id":       principal.IdentityLinkID.String(),
		"instance_id":            optionalUUIDString(filter.InstanceID),
		"session_id":             optionalUUIDString(filter.SessionID),
		"registration_id":        optionalUUIDString(filter.RegistrationID),
		"page":                   page,
		"page_size":              pageSize,
		"total":                  total,
		"returned":               len(items),
		"format":                 filter.Format,
		"answer_values_returned": false,
	})
	if err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"encode questionnaire answer summary audit: %w", err,
		)
	}
	now := catalog.now().UTC()
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'registration.answer_summary_read', $4, $5,
    $6, $7::JSONB, $8, $8)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, targetType, targetID,
		requestID, string(details), now); err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"audit questionnaire answer summary read: %w", err,
		)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.RegistrationAnswerSummaryPage{}, fmt.Errorf(
			"commit administrator questionnaire answer summary read: %w", err,
		)
	}
	return xiangwanadmin.RegistrationAnswerSummaryPage{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	}, nil
}

const registrationAnswerSummaryFilterSQL = `
FROM xiangwan_registration_answers AS answer
JOIN xiangwan_registrations AS registration
  ON registration.tenant_id = answer.tenant_id
 AND registration.id = answer.registration_id
WHERE answer.tenant_id = $1
  AND ($2::UUID IS NULL OR answer.instance_id = $2)
  AND ($3::UUID IS NULL OR registration.session_id = $3)
  AND ($4::UUID IS NULL OR answer.registration_id = $4)
`

const registrationAnswerSummaryCountSQL = "SELECT COUNT(*) " + registrationAnswerSummaryFilterSQL

// Do not add answer.answer_values to this projection. The database computes
// only a boolean and cardinality so the application cannot accidentally log
// or serialize the submitted values.
const registrationAnswerSummaryListSQL = `SELECT
    answer.registration_id, answer.instance_id, registration.session_id,
    answer.questionnaire_version_id, answer.field_id, answer.field_code,
    answer.field_type, answer.field_label, answer.is_required, answer.sort_order,
    (jsonb_array_length(answer.answer_values) > 0),
    jsonb_array_length(answer.answer_values), answer.created_at
` + registrationAnswerSummaryFilterSQL + `
ORDER BY answer.created_at DESC, answer.registration_id DESC, answer.sort_order ASC
OFFSET $5 LIMIT $6
`

func registrationAnswerAuditTarget(
	filter xiangwanadmin.RegistrationAnswerSummaryFilter,
) (uuid.UUID, string) {
	if filter.RegistrationID != nil {
		return *filter.RegistrationID, "registration"
	}
	if filter.SessionID != nil {
		return *filter.SessionID, "session"
	}
	if filter.InstanceID != nil {
		return *filter.InstanceID, "instance"
	}
	return uuid.Nil, "registration_answers"
}

func optionalUUIDString(value *uuid.UUID) string {
	if value == nil {
		return ""
	}
	return value.String()
}
