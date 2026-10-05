package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

var questionnairePolicyVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$`)

// GetInstanceQuestionnaire returns the latest immutable questionnaire
// assignment for an Instance. A nil result means that the activity uses the
// platform's built-in name and phone registration fields only.
func (catalog *Catalog) GetInstanceQuestionnaire(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	instanceID uuid.UUID,
) (*xiangwanadmin.InstanceQuestionnaire, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || instanceID == uuid.Nil {
		return nil, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var exists bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM xiangwan_activity_instances
    WHERE tenant_id = $1 AND id = $2
)
`, catalog.tenantID, instanceID).Scan(&exists); err != nil {
		return nil, fmt.Errorf("check admin questionnaire Instance: %w", err)
	}
	if !exists {
		return nil, xiangwanadmin.ErrTargetNotFound
	}
	value, err := readInstanceQuestionnaire(ctx, tx, catalog.tenantID, instanceID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit admin questionnaire read: %w", err)
	}
	return value, nil
}

// PublishInstanceQuestionnaire creates and assigns a new immutable
// questionnaire version. Existing registrations keep their snapshot while
// future registrations read the new assignment.
func (catalog *Catalog) PublishInstanceQuestionnaire(
	ctx context.Context,
	command xiangwanadmin.PublishInstanceQuestionnaireCommand,
) (result xiangwanadmin.InstanceQuestionnaire, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"instance.questionnaire.publish", "instance", command.InstanceID,
			command.RequestID, resultErr,
		)
	}()
	command.PrivacyPurpose = strings.TrimSpace(command.PrivacyPurpose)
	command.PrivacyPolicyVersion = strings.TrimSpace(command.PrivacyPolicyVersion)
	command.Fields = normalizeQuestionnaireFields(command.Fields)
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.InstanceID == uuid.Nil ||
		!questionnairePolicyVersionPattern.MatchString(command.PrivacyPolicyVersion) ||
		len([]rune(command.PrivacyPurpose)) < 1 ||
		len([]rune(command.PrivacyPurpose)) > 500 ||
		len(command.Fields) < 1 || len(command.Fields) > activity.MaxQuestionnaireFields {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		InstanceID           uuid.UUID                     `json:"instance_id"`
		PrivacyPurpose       string                        `json:"privacy_purpose"`
		PrivacyPolicyVersion string                        `json:"privacy_policy_version"`
		Fields               []activity.QuestionnaireField `json:"fields"`
	}{
		InstanceID: command.InstanceID, PrivacyPurpose: command.PrivacyPurpose,
		PrivacyPolicyVersion: command.PrivacyPolicyVersion, Fields: command.Fields,
	})
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.InstanceQuestionnaire]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"instance.questionnaire.publish", digest,
	); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	} else if replay {
		if receipt.Value.QuestionnaireVersionID == uuid.Nil ||
			receipt.Value.InstanceID != command.InstanceID || receipt.Value.Version < 1 {
			return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
				"commit admin questionnaire replay: %w", err,
			)
		}
		return receipt.Value, nil
	}

	var status activity.InstanceStatus
	err = tx.QueryRowContext(ctx, `
SELECT status
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"lock admin Instance for questionnaire: %w", err,
		)
	}
	switch status {
	case activity.InstanceStatusDraft,
		activity.InstanceStatusPendingPublish,
		activity.InstanceStatusPublished:
	default:
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrVersionConflict
	}

	var version int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(version), 0) + 1
FROM xiangwan_questionnaire_versions
WHERE tenant_id = $1 AND instance_id = $2
`, catalog.tenantID, command.InstanceID).Scan(&version); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"next admin questionnaire version: %w", err,
		)
	}
	now := catalog.now().UTC()
	questionnaireVersionID := uuid.New()
	fields := make([]activity.QuestionnaireField, len(command.Fields))
	copy(fields, command.Fields)
	for index := range fields {
		fields[index].FieldID = uuid.New()
		fields[index].SortOrder = index
	}
	candidate := activity.SessionQuestionnaire{
		QuestionnaireVersionID: questionnaireVersionID,
		InstanceID:             command.InstanceID,
		SessionID:              uuid.New(), // admin validation only; assignment is Instance-scoped
		Version:                version,
		PrivacyPurpose:         command.PrivacyPurpose,
		PrivacyPolicyVersion:   command.PrivacyPolicyVersion,
		PublishedAt:            now,
		Fields:                 fields,
	}
	if err := activity.ValidateSessionQuestionnaire(candidate); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"validate admin questionnaire: %w", err,
		)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_questionnaire_versions (
    questionnaire_version_id, tenant_id, instance_id, version, status,
    privacy_purpose, privacy_policy_version, created_by, published_by,
    published_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, 'published', $5, $6, $7, $7, $8, $8, $8)
`, questionnaireVersionID, catalog.tenantID, command.InstanceID, version,
		command.PrivacyPurpose, command.PrivacyPolicyVersion, command.ActorID, now); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"insert admin questionnaire version: %w", err,
		)
	}
	for _, field := range fields {
		options, err := json.Marshal(field.Options)
		if err != nil {
			return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
				"encode admin questionnaire options: %w", err,
			)
		}
		if field.Options == nil {
			options = []byte("[]")
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_questionnaire_fields (
    field_id, tenant_id, instance_id, questionnaire_version_id,
    field_code, field_type, label, help_text, is_required, sort_order,
    min_length, max_length, max_selections, options
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14::JSONB)
`, field.FieldID, catalog.tenantID, command.InstanceID, questionnaireVersionID,
			field.Code, field.Type, field.Label, field.HelpText, field.Required,
			field.SortOrder, nullableQuestionnaireInt(field.MinLength),
			nullableQuestionnaireInt(field.MaxLength), nullableQuestionnaireInt(field.MaxSelections),
			string(options)); err != nil {
			return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
				"insert admin questionnaire field: %w", err,
			)
		}
	}
	var assignmentVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(assignment_version), 0) + 1
FROM xiangwan_instance_questionnaires
WHERE tenant_id = $1 AND instance_id = $2
`, catalog.tenantID, command.InstanceID).Scan(&assignmentVersion); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"next admin questionnaire assignment version: %w", err,
		)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_instance_questionnaires (
    tenant_id, instance_id, questionnaire_version_id,
    assignment_version, assigned_by, assigned_at
) VALUES ($1, $2, $3, $4, $5, $6)
`, catalog.tenantID, command.InstanceID, questionnaireVersionID,
		assignmentVersion, command.ActorID, now); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"assign admin questionnaire: %w", err,
		)
	}
	assigned, err := readInstanceQuestionnaire(ctx, tx, catalog.tenantID, command.InstanceID)
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	if assigned == nil {
		return xiangwanadmin.InstanceQuestionnaire{}, errors.New(
			"admin questionnaire disappeared after assignment",
		)
	}
	result = *assigned
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "instance.questionnaire.publish", digest,
		operationResult[xiangwanadmin.InstanceQuestionnaire]{Value: result},
		questionnaireVersionID, version, command.RequestID, now,
	); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf(
			"commit admin questionnaire publish: %w", err,
		)
	}
	return result, nil
}

func normalizeQuestionnaireFields(fields []activity.QuestionnaireField) []activity.QuestionnaireField {
	result := make([]activity.QuestionnaireField, len(fields))
	for index, field := range fields {
		result[index] = field
		result[index].Code = strings.TrimSpace(field.Code)
		result[index].Label = strings.TrimSpace(field.Label)
		result[index].HelpText = strings.TrimSpace(field.HelpText)
		result[index].SortOrder = index
		result[index].Options = append([]activity.QuestionnaireOption(nil), field.Options...)
		for optionIndex := range result[index].Options {
			result[index].Options[optionIndex].Code = strings.TrimSpace(
				result[index].Options[optionIndex].Code,
			)
			result[index].Options[optionIndex].Label = strings.TrimSpace(
				result[index].Options[optionIndex].Label,
			)
		}
	}
	return result
}

func nullableQuestionnaireInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func readInstanceQuestionnaire(
	ctx context.Context,
	db activitypostgres.DBTX,
	tenantID uuid.UUID,
	instanceID uuid.UUID,
) (*xiangwanadmin.InstanceQuestionnaire, error) {
	rows, err := db.QueryContext(ctx, `
SELECT
    version.questionnaire_version_id,
    version.instance_id,
    version.version,
    version.privacy_purpose,
    version.privacy_policy_version,
    version.published_at,
    field.field_id,
    field.field_code,
    field.field_type,
    field.label,
    field.help_text,
    field.is_required,
    field.sort_order,
    field.min_length,
    field.max_length,
    field.max_selections,
    field.options
FROM xiangwan_instance_questionnaires AS assignment
JOIN xiangwan_questionnaire_versions AS version
  ON version.tenant_id = assignment.tenant_id
 AND version.instance_id = assignment.instance_id
 AND version.questionnaire_version_id = assignment.questionnaire_version_id
JOIN xiangwan_questionnaire_fields AS field
  ON field.tenant_id = version.tenant_id
 AND field.instance_id = version.instance_id
 AND field.questionnaire_version_id = version.questionnaire_version_id
WHERE assignment.tenant_id = $1
  AND assignment.instance_id = $2
  AND assignment.assignment_version = (
      SELECT MAX(latest.assignment_version)
      FROM xiangwan_instance_questionnaires AS latest
      WHERE latest.tenant_id = assignment.tenant_id
        AND latest.instance_id = assignment.instance_id
  )
ORDER BY field.sort_order, field.field_id
`, tenantID, instanceID)
	if err != nil {
		return nil, fmt.Errorf("query admin questionnaire: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var result *xiangwanadmin.InstanceQuestionnaire
	for rows.Next() {
		var (
			questionnaireVersionID, questionnaireInstanceID, fieldID uuid.UUID
			version, sortOrder                                       int64
			privacyPurpose, privacyPolicyVersion, fieldCode          string
			fieldType, label, helpText                               string
			publishedAt                                              time.Time
			required                                                 bool
			minLength, maxLength, maxSelections                      sql.NullInt64
			optionsJSON                                              []byte
		)
		if err := rows.Scan(
			&questionnaireVersionID, &questionnaireInstanceID, &version,
			&privacyPurpose, &privacyPolicyVersion, &publishedAt, &fieldID,
			&fieldCode, &fieldType, &label, &helpText, &required, &sortOrder,
			&minLength, &maxLength, &maxSelections, &optionsJSON,
		); err != nil {
			return nil, fmt.Errorf("scan admin questionnaire: %w", err)
		}
		if result == nil {
			result = &xiangwanadmin.InstanceQuestionnaire{
				QuestionnaireVersionID: questionnaireVersionID,
				InstanceID:             questionnaireInstanceID,
				Version:                version,
				PrivacyPurpose:         privacyPurpose,
				PrivacyPolicyVersion:   privacyPolicyVersion,
				PublishedAt:            publishedAt,
				Fields:                 make([]activity.QuestionnaireField, 0),
			}
		} else if result.QuestionnaireVersionID != questionnaireVersionID ||
			result.InstanceID != questionnaireInstanceID || result.Version != version {
			return nil, errors.New("admin questionnaire identity changed while reading")
		}
		var options []activity.QuestionnaireOption
		if err := json.Unmarshal(optionsJSON, &options); err != nil {
			return nil, fmt.Errorf("decode admin questionnaire options: %w", err)
		}
		result.Fields = append(result.Fields, activity.QuestionnaireField{
			FieldID: fieldID, Code: fieldCode,
			Type:  activity.QuestionnaireFieldType(fieldType),
			Label: label, HelpText: helpText, Required: required,
			SortOrder:     sortOrderInt(sortOrder),
			MinLength:     questionnaireNullableInt(minLength),
			MaxLength:     questionnaireNullableInt(maxLength),
			MaxSelections: questionnaireNullableInt(maxSelections),
			Options:       options,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate admin questionnaire: %w", err)
	}
	return result, nil
}

func questionnaireNullableInt(value sql.NullInt64) *int {
	if !value.Valid || value.Int64 < 0 || value.Int64 > int64(^uint(0)>>1) {
		return nil
	}
	item := int(value.Int64)
	return &item
}

func sortOrderInt(value int64) int {
	if value < 0 || value > int64(^uint(0)>>1) {
		return 0
	}
	return int(value)
}
