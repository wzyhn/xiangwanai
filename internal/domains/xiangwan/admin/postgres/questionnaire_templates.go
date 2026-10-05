package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// questionnaireTemplateFieldRecord is deliberately separate from the
// activity model. Template JSON is a persisted public contract and must not
// change shape if Go field names or tags are refactored later.
type questionnaireTemplateFieldRecord struct {
	FieldID       uuid.UUID                       `json:"field_id"`
	Code          string                          `json:"code"`
	Type          activity.QuestionnaireFieldType `json:"type"`
	Label         string                          `json:"label"`
	HelpText      string                          `json:"help_text"`
	Required      bool                            `json:"required"`
	SortOrder     int                             `json:"sort_order"`
	MinLength     *int                            `json:"min_length,omitempty"`
	MaxLength     *int                            `json:"max_length,omitempty"`
	MaxSelections *int                            `json:"max_selections,omitempty"`
	Options       []activity.QuestionnaireOption  `json:"options"`
}

func templateFieldRecords(fields []activity.QuestionnaireField) []questionnaireTemplateFieldRecord {
	result := make([]questionnaireTemplateFieldRecord, len(fields))
	for index, field := range fields {
		result[index] = questionnaireTemplateFieldRecord{
			FieldID: field.FieldID, Code: field.Code, Type: field.Type,
			Label: field.Label, HelpText: field.HelpText, Required: field.Required,
			SortOrder: field.SortOrder, MinLength: cloneTemplateInt(field.MinLength),
			MaxLength:     cloneTemplateInt(field.MaxLength),
			MaxSelections: cloneTemplateInt(field.MaxSelections),
			Options:       append([]activity.QuestionnaireOption(nil), field.Options...),
		}
	}
	return result
}

func templateFieldsFromRecords(records []questionnaireTemplateFieldRecord) []activity.QuestionnaireField {
	result := make([]activity.QuestionnaireField, len(records))
	for index, field := range records {
		result[index] = activity.QuestionnaireField{
			FieldID: field.FieldID, Code: field.Code, Type: field.Type,
			Label: field.Label, HelpText: field.HelpText, Required: field.Required,
			SortOrder: field.SortOrder, MinLength: cloneTemplateInt(field.MinLength),
			MaxLength:     cloneTemplateInt(field.MaxLength),
			MaxSelections: cloneTemplateInt(field.MaxSelections),
			Options:       append([]activity.QuestionnaireOption(nil), field.Options...),
		}
	}
	return result
}

func cloneTemplateInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func prepareTemplateFields(
	fields []activity.QuestionnaireField,
	purpose string,
	policyVersion string,
) ([]activity.QuestionnaireField, error) {
	if !questionnairePolicyVersionPattern.MatchString(policyVersion) {
		return nil, xiangwanadmin.ErrInvalidCatalogRequest
	}
	fields = normalizeQuestionnaireFields(fields)
	for index := range fields {
		if fields[index].FieldID == uuid.Nil {
			fields[index].FieldID = uuid.New()
		}
		fields[index].SortOrder = index
	}
	candidate := activity.SessionQuestionnaire{
		QuestionnaireVersionID: uuid.New(), InstanceID: uuid.New(), SessionID: uuid.New(),
		Version: 1, PrivacyPurpose: purpose, PrivacyPolicyVersion: policyVersion,
		PublishedAt: time.Unix(1, 0).UTC(), Fields: fields,
	}
	if err := activity.ValidateSessionQuestionnaire(candidate); err != nil {
		return nil, fmt.Errorf("validate questionnaire template: %w", err)
	}
	return fields, nil
}

func (catalog *Catalog) ListQuestionnaireTemplates(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	page int,
	pageSize int,
) (result xiangwanadmin.QuestionnaireTemplatePage, resultErr error) {
	page, pageSize, err := normalizePage(page, pageSize)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, err
	}
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil || principal.IdentityLinkID == uuid.Nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM xiangwan_questionnaire_templates
WHERE tenant_id = $1 AND status = 'active'
`, catalog.tenantID).Scan(&result.Total); err != nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, fmt.Errorf("count questionnaire templates: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
SELECT
    template.template_id, template.name, template.description, template.status,
    template.current_version, template.created_at, template.updated_at,
    version.template_version_id, version.privacy_purpose,
    version.privacy_policy_version, version.fields
FROM xiangwan_questionnaire_templates AS template
JOIN xiangwan_questionnaire_template_versions AS version
  ON version.tenant_id = template.tenant_id
 AND version.template_id = template.template_id
 AND version.version = template.current_version
WHERE template.tenant_id = $1 AND template.status = 'active'
ORDER BY template.updated_at DESC, template.template_id
LIMIT $2 OFFSET $3
`, catalog.tenantID, pageSize, (page-1)*pageSize)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, fmt.Errorf("query questionnaire templates: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		value, err := scanQuestionnaireTemplate(rows)
		if err != nil {
			return xiangwanadmin.QuestionnaireTemplatePage{}, err
		}
		result.Items = append(result.Items, value)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, fmt.Errorf("iterate questionnaire templates: %w", err)
	}
	result.Page, result.PageSize = page, pageSize
	if result.Items == nil {
		result.Items = []xiangwanadmin.QuestionnaireTemplate{}
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.QuestionnaireTemplatePage{}, fmt.Errorf("commit questionnaire template read: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) GetQuestionnaireTemplate(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	templateID uuid.UUID,
) (xiangwanadmin.QuestionnaireTemplate, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil || principal.IdentityLinkID == uuid.Nil || templateID == uuid.Nil {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, principal)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	value, err := readQuestionnaireTemplate(ctx, tx, catalog.tenantID, templateID)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("commit questionnaire template read: %w", err)
	}
	return value, nil
}

func (catalog *Catalog) CreateQuestionnaireTemplate(
	ctx context.Context,
	command xiangwanadmin.CreateQuestionnaireTemplateCommand,
) (result xiangwanadmin.QuestionnaireTemplate, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"questionnaire_template.create", "questionnaire_template", uuid.Nil,
			command.RequestID, resultErr,
		)
	}()
	command.Name = strings.TrimSpace(command.Name)
	command.Description = strings.TrimSpace(command.Description)
	command.PrivacyPurpose = strings.TrimSpace(command.PrivacyPurpose)
	command.PrivacyPolicyVersion = strings.TrimSpace(command.PrivacyPolicyVersion)
	fields, err := prepareTemplateFields(command.Fields, command.PrivacyPurpose, command.PrivacyPolicyVersion)
	if !catalog.valid(ctx) || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) ||
		command.Name == "" || len([]rune(command.Name)) > 200 || len([]rune(command.Description)) > 500 || err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		Name, Description, PrivacyPurpose, PrivacyPolicyVersion string
		Fields                                                  []activity.QuestionnaireField
	}{command.Name, command.Description, command.PrivacyPurpose, command.PrivacyPolicyVersion, fields})
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := readOperation[xiangwanadmin.QuestionnaireTemplate](ctx, tx, catalog.tenantID, command.ActorID, command.OperationID, "questionnaire_template.create", digest); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	} else if found {
		return replay, nil
	}
	now := catalog.now().UTC()
	templateID, versionID := uuid.New(), uuid.New()
	encodedFields, err := json.Marshal(templateFieldRecords(fields))
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("encode questionnaire template fields: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_questionnaire_templates (
    template_id, tenant_id, name, description, status, current_version,
    created_by, updated_by, created_at, updated_at
) VALUES ($1, $2, $3, $4, 'active', 1, $5, $5, $6, $6)
`, templateID, catalog.tenantID, command.Name, command.Description, command.ActorID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, classifyQuestionnaireTemplateWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_questionnaire_template_versions (
    template_version_id, tenant_id, template_id, version,
    privacy_purpose, privacy_policy_version, fields, created_by, created_at
) VALUES ($1, $2, $3, 1, $4, $5, $6::JSONB, $7, $8)
`, versionID, catalog.tenantID, templateID, command.PrivacyPurpose, command.PrivacyPolicyVersion, string(encodedFields), command.ActorID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, classifyQuestionnaireTemplateWriteError(err)
	}
	result, err = readQuestionnaireTemplate(ctx, tx, catalog.tenantID, templateID)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID, command.OperationID, "questionnaire_template.create", digest, operationResult[xiangwanadmin.QuestionnaireTemplate]{Value: result}, templateID, result.Version, command.RequestID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("commit questionnaire template create: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) UpdateQuestionnaireTemplate(
	ctx context.Context,
	command xiangwanadmin.UpdateQuestionnaireTemplateCommand,
) (result xiangwanadmin.QuestionnaireTemplate, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "questionnaire_template.update", "questionnaire_template", command.TemplateID, command.RequestID, resultErr)
	}()
	command.Name = strings.TrimSpace(command.Name)
	command.Description = strings.TrimSpace(command.Description)
	command.PrivacyPurpose = strings.TrimSpace(command.PrivacyPurpose)
	command.PrivacyPolicyVersion = strings.TrimSpace(command.PrivacyPolicyVersion)
	fields, err := prepareTemplateFields(command.Fields, command.PrivacyPurpose, command.PrivacyPolicyVersion)
	if !catalog.valid(ctx) || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) || command.TemplateID == uuid.Nil || command.ExpectedVersion < 1 || command.Name == "" || len([]rune(command.Name)) > 200 || len([]rune(command.Description)) > 500 || err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		TemplateID                                              uuid.UUID `json:"template_id"`
		ExpectedVersion                                         int64     `json:"expected_version"`
		Name, Description, PrivacyPurpose, PrivacyPolicyVersion string
		Fields                                                  []activity.QuestionnaireField
	}{command.TemplateID, command.ExpectedVersion, command.Name, command.Description, command.PrivacyPurpose, command.PrivacyPolicyVersion, fields})
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := readOperation[xiangwanadmin.QuestionnaireTemplate](ctx, tx, catalog.tenantID, command.ActorID, command.OperationID, "questionnaire_template.update", digest); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	} else if found {
		return replay, nil
	}
	var status string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT status, current_version
FROM xiangwan_questionnaire_templates
WHERE tenant_id = $1 AND template_id = $2
FOR UPDATE
`, catalog.tenantID, command.TemplateID).Scan(&status, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrTargetNotFound
	} else if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("lock questionnaire template: %w", err)
	}
	if status != "active" || currentVersion != command.ExpectedVersion {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	newVersion := currentVersion + 1
	versionID := uuid.New()
	encodedFields, err := json.Marshal(templateFieldRecords(fields))
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("encode questionnaire template fields: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_questionnaire_template_versions (
    template_version_id, tenant_id, template_id, version,
    privacy_purpose, privacy_policy_version, fields, created_by, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7::JSONB, $8, $9)
`, versionID, catalog.tenantID, command.TemplateID, newVersion, command.PrivacyPurpose, command.PrivacyPolicyVersion, string(encodedFields), command.ActorID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, classifyQuestionnaireTemplateWriteError(err)
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_questionnaire_templates
SET name = $3, description = $4, current_version = $5, updated_by = $6, updated_at = $7
WHERE tenant_id = $1 AND template_id = $2
`, catalog.tenantID, command.TemplateID, command.Name, command.Description, newVersion, command.ActorID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, classifyQuestionnaireTemplateWriteError(err)
	}
	result, err = readQuestionnaireTemplate(ctx, tx, catalog.tenantID, command.TemplateID)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID, command.OperationID, "questionnaire_template.update", digest, operationResult[xiangwanadmin.QuestionnaireTemplate]{Value: result}, command.TemplateID, result.Version, command.RequestID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("commit questionnaire template update: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) ArchiveQuestionnaireTemplate(
	ctx context.Context,
	command xiangwanadmin.ArchiveQuestionnaireTemplateCommand,
) (result xiangwanadmin.QuestionnaireTemplate, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "questionnaire_template.archive", "questionnaire_template", command.TemplateID, command.RequestID, resultErr)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) || command.TemplateID == uuid.Nil || command.ExpectedVersion < 1 {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(command)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := readOperation[xiangwanadmin.QuestionnaireTemplate](ctx, tx, catalog.tenantID, command.ActorID, command.OperationID, "questionnaire_template.archive", digest); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	} else if found {
		return replay, nil
	}
	var status string
	var currentVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT status, current_version
FROM xiangwan_questionnaire_templates
WHERE tenant_id = $1 AND template_id = $2
FOR UPDATE
`, catalog.tenantID, command.TemplateID).Scan(&status, &currentVersion); errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrTargetNotFound
	} else if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("lock questionnaire template archive: %w", err)
	}
	if status != "active" || currentVersion != command.ExpectedVersion {
		return xiangwanadmin.QuestionnaireTemplate{}, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE xiangwan_questionnaire_templates
SET status = 'archived', updated_by = $3, updated_at = $4
WHERE tenant_id = $1 AND template_id = $2
`, catalog.tenantID, command.TemplateID, command.ActorID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, classifyQuestionnaireTemplateWriteError(err)
	}
	result, err = readQuestionnaireTemplate(ctx, tx, catalog.tenantID, command.TemplateID)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID, command.OperationID, "questionnaire_template.archive", digest, operationResult[xiangwanadmin.QuestionnaireTemplate]{Value: result}, command.TemplateID, result.Version, command.RequestID, now); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("commit questionnaire template archive: %w", err)
	}
	return result, nil
}

func (catalog *Catalog) ApplyQuestionnaireTemplate(
	ctx context.Context,
	command xiangwanadmin.ApplyQuestionnaireTemplateCommand,
) (result xiangwanadmin.InstanceQuestionnaire, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, command.ActorID, command.IdentityLinkID, command.OperationID, "instance.questionnaire.apply_template", "instance", command.InstanceID, command.RequestID, resultErr)
	}()
	if !catalog.valid(ctx) || !validWriteIdentity(command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID) || command.TemplateID == uuid.Nil || command.InstanceID == uuid.Nil || command.ExpectedVersion < 1 {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(command)
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	tx, err := catalog.beginActivityWrite(ctx, command.ActorID, command.IdentityLinkID, command.OperationID)
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay, found, err := readOperation[xiangwanadmin.InstanceQuestionnaire](ctx, tx, catalog.tenantID, command.ActorID, command.OperationID, "instance.questionnaire.apply_template", digest); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	} else if found {
		return replay, nil
	}
	template, err := readQuestionnaireTemplate(ctx, tx, catalog.tenantID, command.TemplateID)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	if template.Status != "active" {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrVersionConflict
	}
	var status activity.InstanceStatus
	var instanceVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT status, version
FROM xiangwan_activity_instances
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.InstanceID).Scan(&status, &instanceVersion); errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrTargetNotFound
	} else if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf("lock Instance for questionnaire template: %w", err)
	}
	if instanceVersion != command.ExpectedVersion {
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrVersionConflict
	}
	switch status {
	case activity.InstanceStatusDraft, activity.InstanceStatusPendingPublish, activity.InstanceStatusPublished:
	default:
		return xiangwanadmin.InstanceQuestionnaire{}, xiangwanadmin.ErrVersionConflict
	}
	now := catalog.now().UTC()
	var version int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(version), 0) + 1
FROM xiangwan_questionnaire_versions
WHERE tenant_id = $1 AND instance_id = $2
`, catalog.tenantID, command.InstanceID).Scan(&version); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf("next Instance questionnaire version: %w", err)
	}
	questionnaireVersionID := uuid.New()
	fields := make([]activity.QuestionnaireField, len(template.Fields))
	copy(fields, template.Fields)
	for index := range fields {
		fields[index].FieldID = uuid.New()
		fields[index].SortOrder = index
	}
	candidate := activity.SessionQuestionnaire{
		QuestionnaireVersionID: questionnaireVersionID, InstanceID: command.InstanceID,
		SessionID: uuid.New(), Version: version,
		PrivacyPurpose: template.PrivacyPurpose, PrivacyPolicyVersion: template.PrivacyPolicyVersion,
		PublishedAt: now, Fields: fields,
	}
	if err := activity.ValidateSessionQuestionnaire(candidate); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf("validate applied questionnaire template: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_questionnaire_versions (
    questionnaire_version_id, tenant_id, instance_id, version, status,
    privacy_purpose, privacy_policy_version, created_by, published_by,
    published_at, created_at, updated_at
) VALUES ($1, $2, $3, $4, 'published', $5, $6, $7, $7, $8, $8, $8)
`, questionnaireVersionID, catalog.tenantID, command.InstanceID, version, template.PrivacyPurpose, template.PrivacyPolicyVersion, command.ActorID, now); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, classifyQuestionnaireTemplateWriteError(err)
	}
	for _, field := range fields {
		options, err := json.Marshal(field.Options)
		if err != nil {
			return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf("encode applied questionnaire options: %w", err)
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
`, field.FieldID, catalog.tenantID, command.InstanceID, questionnaireVersionID, field.Code, field.Type, field.Label, field.HelpText, field.Required, field.SortOrder, nullableQuestionnaireInt(field.MinLength), nullableQuestionnaireInt(field.MaxLength), nullableQuestionnaireInt(field.MaxSelections), string(options)); err != nil {
			return xiangwanadmin.InstanceQuestionnaire{}, classifyQuestionnaireTemplateWriteError(err)
		}
	}
	var assignmentVersion int64
	if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(MAX(assignment_version), 0) + 1
FROM xiangwan_instance_questionnaires
WHERE tenant_id = $1 AND instance_id = $2
`, catalog.tenantID, command.InstanceID).Scan(&assignmentVersion); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf("next applied questionnaire assignment version: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_instance_questionnaires (
    tenant_id, instance_id, questionnaire_version_id,
    assignment_version, assigned_by, assigned_at
) VALUES ($1, $2, $3, $4, $5, $6)
`, catalog.tenantID, command.InstanceID, questionnaireVersionID, assignmentVersion, command.ActorID, now); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, classifyQuestionnaireTemplateWriteError(err)
	}
	assigned, err := readInstanceQuestionnaire(ctx, tx, catalog.tenantID, command.InstanceID)
	if err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	if assigned == nil {
		return xiangwanadmin.InstanceQuestionnaire{}, errors.New("applied questionnaire disappeared after assignment")
	}
	result = *assigned
	if err := writeOperationAndAudit(ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID, command.OperationID, "instance.questionnaire.apply_template", digest, operationResult[xiangwanadmin.InstanceQuestionnaire]{Value: result}, command.InstanceID, version, command.RequestID, now); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, err
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.InstanceQuestionnaire{}, fmt.Errorf("commit questionnaire template assignment: %w", err)
	}
	return result, nil
}

func readQuestionnaireTemplate(
	ctx context.Context,
	db activitypostgres.DBTX,
	tenantID uuid.UUID,
	templateID uuid.UUID,
) (xiangwanadmin.QuestionnaireTemplate, error) {
	var value xiangwanadmin.QuestionnaireTemplate
	var rawFields []byte
	err := db.QueryRowContext(ctx, `
SELECT
    template.template_id, template.name, template.description, template.status,
    template.current_version, template.created_at, template.updated_at,
    version.template_version_id, version.privacy_purpose,
    version.privacy_policy_version, version.fields
FROM xiangwan_questionnaire_templates AS template
JOIN xiangwan_questionnaire_template_versions AS version
  ON version.tenant_id = template.tenant_id
 AND version.template_id = template.template_id
 AND version.version = template.current_version
WHERE template.tenant_id = $1 AND template.template_id = $2
`, tenantID, templateID).Scan(&value.ID, &value.Name, &value.Description, &value.Status, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.VersionID, &value.PrivacyPurpose, &value.PrivacyPolicyVersion, &rawFields)
	if err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, err
	}
	var records []questionnaireTemplateFieldRecord
	if err := json.Unmarshal(rawFields, &records); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("decode questionnaire template fields: %w", err)
	}
	value.Fields = templateFieldsFromRecords(records)
	if value.Fields == nil {
		value.Fields = []activity.QuestionnaireField{}
	}
	return value, nil
}

func scanQuestionnaireTemplate(scanner interface{ Scan(...any) error }) (xiangwanadmin.QuestionnaireTemplate, error) {
	var value xiangwanadmin.QuestionnaireTemplate
	var rawFields []byte
	if err := scanner.Scan(&value.ID, &value.Name, &value.Description, &value.Status, &value.Version, &value.CreatedAt, &value.UpdatedAt, &value.VersionID, &value.PrivacyPurpose, &value.PrivacyPolicyVersion, &rawFields); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("scan questionnaire template: %w", err)
	}
	var records []questionnaireTemplateFieldRecord
	if err := json.Unmarshal(rawFields, &records); err != nil {
		return xiangwanadmin.QuestionnaireTemplate{}, fmt.Errorf("decode questionnaire template fields: %w", err)
	}
	value.Fields = templateFieldsFromRecords(records)
	if value.Fields == nil {
		value.Fields = []activity.QuestionnaireField{}
	}
	return value, nil
}

func classifyQuestionnaireTemplateWriteError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "23514" || pgErr.Code == "23505") {
		return xiangwanadmin.ErrInvalidCatalogRequest
	}
	return err
}
