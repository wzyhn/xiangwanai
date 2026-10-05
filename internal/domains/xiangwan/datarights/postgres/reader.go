package datarightspostgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/datarights"
	"github.com/google/uuid"
)

var (
	ErrInvalidMyCasesReader = errors.New(
		"invalid xiangwan My Data Rights Cases reader",
	)
	ErrMyCasesProjection = errors.New(
		"xiangwan My Data Rights Cases projection mismatch",
	)
)

type Reader struct {
	db caseQueryExecutor
}

func NewReader(db *sql.DB) *Reader {
	return &Reader{db: sqlCaseQueryExecutor{db: db}}
}

func (reader *Reader) ListMine(
	ctx context.Context,
	tenantID uuid.UUID,
	principalID uuid.UUID,
) ([]datarights.CaseHistory, error) {
	if reader == nil || reader.db == nil || ctx == nil || tenantID == uuid.Nil ||
		principalID == uuid.Nil {
		return nil, ErrInvalidMyCasesReader
	}
	rows, err := reader.db.queryContext(ctx, `
WITH owned_cases AS (
    SELECT
        data_case.id,
        data_case.tenant_id,
        data_case.principal_id,
        data_case.operation_key,
        data_case.request_fingerprint,
        data_case.request_type,
        data_case.request_scope,
        data_case.privacy_policy_version,
        data_case.status,
        data_case.version,
        data_case.submitted_at,
        data_case.updated_at,
        data_case.completed_at
    FROM xiangwan_data_rights_cases AS data_case
    WHERE data_case.tenant_id = $1
      AND data_case.principal_id = $2
      AND EXISTS (
          SELECT 1
          FROM principals
          WHERE id = $2
            AND status = 'active'
            AND deleted_at IS NULL
      )
    ORDER BY data_case.submitted_at DESC, data_case.id DESC
    LIMIT $3
)
SELECT
    data_case.id,
    data_case.tenant_id,
    data_case.principal_id,
    data_case.operation_key,
    data_case.request_fingerprint,
    data_case.request_type,
    data_case.request_scope,
    data_case.privacy_policy_version,
    data_case.status,
    data_case.version,
    data_case.submitted_at,
    data_case.updated_at,
    data_case.completed_at,
    data_event.id,
    data_event.tenant_id,
    data_event.case_id,
    data_event.case_version,
    data_event.event_type,
    data_event.resulting_status,
    data_event.actor_principal_id,
    data_event.policy_basis_version,
    data_event.delivery_kind,
    data_event.delivery_status,
    data_event.evidence_digest,
    data_event.occurred_at
FROM owned_cases AS data_case
LEFT JOIN xiangwan_data_rights_case_events AS data_event
  ON data_event.tenant_id = data_case.tenant_id
 AND data_event.case_id = data_case.id
ORDER BY
    data_case.submitted_at DESC,
    data_case.id DESC,
    data_event.case_version,
    data_event.id
`, tenantID, principalID, datarights.MaxMyCases)
	if err != nil {
		return nil, fmt.Errorf("list xiangwan My Data Rights Cases: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	histories := make([]datarights.CaseHistory, 0)
	for rows.Next() {
		dataCase, event, err := scanHistoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan xiangwan My Data Rights Case: %w", err)
		}
		if len(histories) == 0 || histories[len(histories)-1].Case.ID != dataCase.ID {
			histories = append(histories, datarights.CaseHistory{
				Case:   dataCase,
				Events: []datarights.CaseEvent{},
			})
		} else if !sameDataRightsCase(histories[len(histories)-1].Case, dataCase) {
			return nil, ErrMyCasesProjection
		}
		if event != nil {
			histories[len(histories)-1].Events = append(
				histories[len(histories)-1].Events,
				*event,
			)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate xiangwan My Data Rights Cases: %w", err)
	}
	for _, history := range histories {
		if err := validateHistoryProjection(history); err != nil {
			return nil, err
		}
	}
	return histories, nil
}

type caseQueryExecutor interface {
	queryContext(context.Context, string, ...any) (rowsScanner, error)
}

type rowsScanner interface {
	rowScanner
	Next() bool
	Err() error
	Close() error
}

type sqlCaseQueryExecutor struct {
	db *sql.DB
}

func (executor sqlCaseQueryExecutor) queryContext(
	ctx context.Context,
	query string,
	args ...any,
) (rowsScanner, error) {
	if executor.db == nil {
		return nil, ErrInvalidMyCasesReader
	}
	return executor.db.QueryContext(ctx, query, args...)
}

func scanHistoryRow(
	row rowScanner,
) (datarights.Case, *datarights.CaseEvent, error) {
	var dataCase datarights.Case
	var fingerprint []byte
	var completedAt sql.NullTime
	var eventID uuid.NullUUID
	var eventTenantID uuid.NullUUID
	var eventCaseID uuid.NullUUID
	var eventVersion sql.NullInt64
	var eventType sql.NullString
	var resultingStatus sql.NullString
	var actorID uuid.NullUUID
	var policyBasis sql.NullString
	var deliveryKind sql.NullString
	var deliveryStatus sql.NullString
	var evidenceDigest []byte
	var eventOccurredAt sql.NullTime
	err := row.Scan(
		&dataCase.ID,
		&dataCase.TenantID,
		&dataCase.PrincipalID,
		&dataCase.OperationKey,
		&fingerprint,
		&dataCase.RequestType,
		&dataCase.RequestScope,
		&dataCase.PrivacyPolicyVersion,
		&dataCase.Status,
		&dataCase.Version,
		&dataCase.SubmittedAt,
		&dataCase.UpdatedAt,
		&completedAt,
		&eventID,
		&eventTenantID,
		&eventCaseID,
		&eventVersion,
		&eventType,
		&resultingStatus,
		&actorID,
		&policyBasis,
		&deliveryKind,
		&deliveryStatus,
		&evidenceDigest,
		&eventOccurredAt,
	)
	if err != nil {
		return datarights.Case{}, nil, err
	}
	if len(fingerprint) != len(dataCase.RequestFingerprint) {
		return datarights.Case{}, nil, ErrMyCasesProjection
	}
	copy(dataCase.RequestFingerprint[:], fingerprint)
	dataCase.SubmittedAt = dataCase.SubmittedAt.UTC()
	dataCase.UpdatedAt = dataCase.UpdatedAt.UTC()
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		dataCase.CompletedAt = &value
	}
	if err := datarights.ValidateCase(dataCase); err != nil {
		return datarights.Case{}, nil, ErrMyCasesProjection
	}
	if !eventID.Valid {
		if eventTenantID.Valid || eventCaseID.Valid || eventVersion.Valid ||
			eventType.Valid || resultingStatus.Valid || actorID.Valid ||
			policyBasis.Valid || deliveryKind.Valid || deliveryStatus.Valid ||
			len(evidenceDigest) != 0 || eventOccurredAt.Valid {
			return datarights.Case{}, nil, ErrMyCasesProjection
		}
		return dataCase, nil, nil
	}
	if !eventTenantID.Valid || !eventCaseID.Valid || !eventVersion.Valid ||
		!eventType.Valid || !resultingStatus.Valid || !eventOccurredAt.Valid {
		return datarights.Case{}, nil, ErrMyCasesProjection
	}
	event := datarights.CaseEvent{
		ID:                 eventID.UUID,
		TenantID:           eventTenantID.UUID,
		CaseID:             eventCaseID.UUID,
		CaseVersion:        eventVersion.Int64,
		EventType:          datarights.EventType(eventType.String),
		ResultingStatus:    datarights.CaseStatus(resultingStatus.String),
		PolicyBasisVersion: policyBasis.String,
		DeliveryKind:       datarights.DeliveryKind(deliveryKind.String),
		DeliveryStatus:     datarights.DeliveryStatus(deliveryStatus.String),
		EvidenceDigest:     append([]byte(nil), evidenceDigest...),
		OccurredAt:         eventOccurredAt.Time.UTC(),
	}
	if actorID.Valid {
		value := actorID.UUID
		event.ActorPrincipalID = &value
	}
	if err := datarights.ValidateCaseEvent(event); err != nil {
		return datarights.Case{}, nil, ErrMyCasesProjection
	}
	return dataCase, &event, nil
}

func validateHistoryProjection(history datarights.CaseHistory) error {
	if len(history.Events) != int(history.Case.Version) ||
		len(history.Events) == 0 {
		return ErrMyCasesProjection
	}
	for index, event := range history.Events {
		if event.TenantID != history.Case.TenantID ||
			event.CaseID != history.Case.ID ||
			event.CaseVersion != int64(index+1) ||
			event.OccurredAt.Before(history.Case.SubmittedAt) ||
			(index > 0 && event.OccurredAt.Before(
				history.Events[index-1].OccurredAt,
			)) {
			return ErrMyCasesProjection
		}
	}
	first := history.Events[0]
	if first.EventType != datarights.EventTypeSubmitted ||
		first.ResultingStatus != datarights.CaseStatusSubmitted ||
		first.ActorPrincipalID == nil ||
		*first.ActorPrincipalID != history.Case.PrincipalID ||
		first.PolicyBasisVersion != history.Case.PrivacyPolicyVersion ||
		history.Events[len(history.Events)-1].ResultingStatus !=
			history.Case.Status {
		return ErrMyCasesProjection
	}
	return nil
}

func sameDataRightsCase(left datarights.Case, right datarights.Case) bool {
	if left.ID != right.ID || left.TenantID != right.TenantID ||
		left.PrincipalID != right.PrincipalID ||
		left.OperationKey != right.OperationKey ||
		left.RequestFingerprint != right.RequestFingerprint ||
		left.RequestType != right.RequestType ||
		left.RequestScope != right.RequestScope ||
		left.PrivacyPolicyVersion != right.PrivacyPolicyVersion ||
		left.Status != right.Status || left.Version != right.Version ||
		!left.SubmittedAt.Equal(right.SubmittedAt) ||
		!left.UpdatedAt.Equal(right.UpdatedAt) {
		return false
	}
	if left.CompletedAt == nil || right.CompletedAt == nil {
		return left.CompletedAt == nil && right.CompletedAt == nil
	}
	return left.CompletedAt.Equal(*right.CompletedAt)
}
