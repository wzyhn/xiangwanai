package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/pkg/requestctx"
	"github.com/google/uuid"
)

func (catalog *Catalog) ListRegistrations(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	filter xiangwanadmin.RegistrationFilter,
) (xiangwanadmin.RegistrationPage, error) {
	page, pageSize, err := normalizePage(filter.Page, filter.PageSize)
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || err != nil ||
		!validOptionalID(filter.SeriesID) || !validOptionalID(filter.InstanceID) ||
		!validOptionalID(filter.SessionID) ||
		!validParticipationState(filter.ParticipationState) {
		return xiangwanadmin.RegistrationPage{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	// The authorization rows are locked inside the same repeatable-read
	// snapshot as the data query, so a revocation cannot commit between the
	// check and the read and leak masked contact data (codex review
	// 2026-09-19).
	tx, txErr := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if txErr != nil {
		return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
			"begin administrator Registration read: %w", txErr,
		)
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.lockRegistrationReadIdentity(
		ctx, tx, principal.PrincipalID, principal.IdentityLinkID,
		filter.SessionID,
	); err != nil {
		return xiangwanadmin.RegistrationPage{}, err
	}
	args := []any{
		catalog.tenantID, nullableUUID(filter.SeriesID),
		nullableUUID(filter.InstanceID), nullableUUID(filter.SessionID),
		filter.ParticipationState, principal.PrincipalID, principal.IdentityLinkID,
	}
	var total int64
	if err := tx.QueryRowContext(ctx, registrationCountSQL, args...).Scan(&total); err != nil {
		return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
			"count administrator Registrations: %w", err,
		)
	}
	rows, err := tx.QueryContext(
		ctx,
		registrationListSQL,
		append(args, (page-1)*pageSize, pageSize)...,
	)
	if err != nil {
		return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
			"list administrator Registrations: %w", err,
		)
	}
	defer func() { _ = rows.Close() }()
	items := make([]xiangwanadmin.RegistrationItem, 0)
	for rows.Next() {
		item, scanErr := scanRegistrationItem(rows)
		if scanErr != nil {
			return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
				"scan administrator Registration: %w", scanErr,
			)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
			"iterate administrator Registrations: %w", err,
		)
	}
	if err := rows.Close(); err != nil {
		return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
			"close administrator Registrations: %w", err,
		)
	}
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.RegistrationPage{}, fmt.Errorf(
			"commit administrator Registration read: %w", err,
		)
	}
	return xiangwanadmin.RegistrationPage{
		Items: items, Page: page, PageSize: pageSize, Total: total,
	}, nil
}

func (catalog *Catalog) GetRegistration(
	ctx context.Context,
	principal xiangwanadmin.Principal,
	registrationID uuid.UUID,
	contactPurpose string,
) (xiangwanadmin.RegistrationDetail, error) {
	if !catalog.valid(ctx) || principal.PrincipalID == uuid.Nil ||
		principal.IdentityLinkID == uuid.Nil || registrationID == uuid.Nil ||
		(contactPurpose != "" && contactPurpose != "activity_coordination" && contactPurpose != "onsite_verification") {
		return xiangwanadmin.RegistrationDetail{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	var detail xiangwanadmin.RegistrationDetail
	var contactName string
	var contactPhone string
	var confirmedAt sql.NullTime
	var cancelledAt sql.NullTime
	var cancellationReason sql.NullString
	var checkinID uuid.NullUUID
	var checkedInAt sql.NullTime
	var paymentStatus sql.NullString
	var refundStatus sql.NullString
	var priceCents sql.NullInt64
	var successfulRefundCents sql.NullInt64
	// Lock the authorization rows ahead of the row-level predicate so a
	// concurrent revocation cannot commit between the check and this read
	// (codex review 2026-09-19).
	tx, txErr := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if txErr != nil {
		return xiangwanadmin.RegistrationDetail{}, fmt.Errorf(
			"begin administrator Registration detail read: %w", txErr,
		)
	}
	defer func() { _ = tx.Rollback() }()
	if err := catalog.authorizer.lockRegistrationDetailReadIdentity(
		ctx, tx, principal.PrincipalID, principal.IdentityLinkID,
	); err != nil {
		return xiangwanadmin.RegistrationDetail{}, err
	}
	err := tx.QueryRowContext(ctx, registrationDetailSQL,
		catalog.tenantID, registrationID, principal.PrincipalID,
		principal.IdentityLinkID).Scan(
		&detail.ID, &detail.SeriesID, &detail.InstanceID, &detail.SessionID,
		&detail.InstanceTitle, &detail.SessionTitle, &contactName, &contactPhone,
		&detail.ParticipationStatus, &paymentStatus, &refundStatus,
		&detail.CheckinStatus, &detail.CreatedAt, &detail.UpdatedAt,
		&priceCents, &detail.InstancePublicationVersion,
		&detail.SessionVersion, &confirmedAt, &cancelledAt,
		&cancellationReason, &checkinID, &checkedInAt,
		&successfulRefundCents, &detail.PrivacyPolicyVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return xiangwanadmin.RegistrationDetail{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return xiangwanadmin.RegistrationDetail{}, fmt.Errorf(
			"get administrator Registration: %w", err,
		)
	}
	detail.ContactNameMasked = maskContactName(contactName)
	detail.ContactPhoneMasked = maskContactPhone(contactPhone)
	if contactPurpose != "" {
		contactPermission := catalog.authorizer.RequireActivityOperatorIdentity(
			ctx, tx, principal.PrincipalID, principal.IdentityLinkID,
		)
		if contactPermission != nil && !errors.Is(contactPermission, xiangwanadmin.ErrScopeForbidden) {
			return xiangwanadmin.RegistrationDetail{}, contactPermission
		}
		outcome := "allowed"
		if contactPermission != nil {
			outcome = "denied"
		}
		// Never copy contact values into the audit event or operation receipts.
		now := catalog.now().UTC()
		requestID := requestctx.RequestID(ctx)
		if requestID == "" {
			requestID = uuid.NewString()
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO xiangwan_admin_audit_events (
    id, tenant_id, actor_id, action_code, target_type, target_id,
    request_id, details, occurred_at, created_at
) VALUES ($1, $2, $3, 'registration.contact_read', 'registration', $4,
    $5, jsonb_build_object('identity_link_id', $6::TEXT, 'purpose', $8::TEXT, 'outcome', $9::TEXT), $7, $7)
`, uuid.New(), catalog.tenantID, principal.PrincipalID, registrationID,
			requestID, principal.IdentityLinkID.String(), now, contactPurpose, outcome); err != nil {
			return xiangwanadmin.RegistrationDetail{}, fmt.Errorf("audit registration contact read: %w", err)
		}
		if contactPermission != nil {
			if err := tx.Commit(); err != nil {
				return xiangwanadmin.RegistrationDetail{}, fmt.Errorf("commit denied contact read audit: %w", err)
			}
			return xiangwanadmin.RegistrationDetail{}, contactPermission
		}
		detail.Contact = &xiangwanadmin.RegistrationContact{Name: contactName, Phone: contactPhone}
	}
	detail.ConfirmedAt = adminNullTimePointer(confirmedAt)
	detail.CancelledAt = adminNullTimePointer(cancelledAt)
	if cancellationReason.Valid {
		detail.CancellationReason = cancellationReason.String
	}
	if checkinID.Valid {
		detail.CheckinID = &checkinID.UUID
	}
	detail.CheckedInAt = adminNullTimePointer(checkedInAt)
	detail.PaymentStatus = adminNullStringPointer(paymentStatus)
	detail.RefundStatus = adminNullStringPointer(refundStatus)
	detail.PriceCents = adminNullInt64Pointer(priceCents)
	detail.SuccessfulRefundCents = adminNullInt64Pointer(successfulRefundCents)
	if err := tx.Commit(); err != nil {
		return xiangwanadmin.RegistrationDetail{}, fmt.Errorf(
			"commit administrator Registration detail read: %w", err,
		)
	}
	return detail, nil
}

const registrationFilterSQL = `
FROM xiangwan_registrations AS registration
JOIN xiangwan_activity_instances AS instance
  ON instance.tenant_id = registration.tenant_id
 AND instance.id = registration.instance_id
JOIN xiangwan_activity_sessions AS session
  ON session.tenant_id = registration.tenant_id
 AND session.id = registration.session_id
LEFT JOIN xiangwan_registration_snapshots AS snapshot
  ON snapshot.tenant_id = registration.tenant_id
 AND snapshot.registration_id = registration.id
LEFT JOIN xiangwan_orders AS activity_order
  ON activity_order.tenant_id = registration.tenant_id
 AND activity_order.registration_id = registration.id
LEFT JOIN xiangwan_refund_cases AS refund
  ON refund.tenant_id = registration.tenant_id
 AND refund.registration_id = registration.id
LEFT JOIN xiangwan_checkins AS checkin
  ON checkin.tenant_id = registration.tenant_id
 AND checkin.registration_id = registration.id
WHERE registration.tenant_id = $1
  AND ($2::UUID IS NULL OR registration.series_id = $2)
  AND ($3::UUID IS NULL OR registration.instance_id = $3)
  AND ($4::UUID IS NULL OR registration.session_id = $4)
  AND ($5 = '' OR registration.participation_status = $5)
  AND EXISTS (
      SELECT 1
      FROM principals AS principal
      JOIN xiangwan_admin_identity_links AS identity_link
       ON identity_link.tenant_id = $1
       AND identity_link.principal_id = principal.id
       AND identity_link.link_status = 'active'
       AND identity_link.id = $7
      JOIN xiangwan_admin_grants AS admin_grant
        ON admin_grant.tenant_id = $1
       AND admin_grant.principal_id = principal.id
       AND admin_grant.domain_code = 'xiangwan'
       AND admin_grant.grant_status = 'active'
      WHERE principal.id = $6
        AND principal.status = 'active'
        AND principal.deleted_at IS NULL
        AND principal.primary_tenant_id = $1
        AND (
            (
                admin_grant.capability IN ('super_admin', 'activity_operator')
                AND admin_grant.scope_type = 'tenant'
                AND admin_grant.scope_id IS NULL
            )
            OR (
                admin_grant.capability = 'onsite_checkin'
                AND (
                    (
                        admin_grant.scope_type = 'tenant'
                        AND admin_grant.scope_id IS NULL
                    )
                    OR (
                        admin_grant.scope_type = 'session'
                        AND admin_grant.scope_id = registration.session_id
                    )
                )
            )
        )
  )
`

const registrationCountSQL = `SELECT COUNT(*) ` + registrationFilterSQL

const registrationListItemProjection = `
    registration.id, registration.series_id, registration.instance_id,
    registration.session_id, instance.title, session.title,
    COALESCE(snapshot.contact_name, ''),
    COALESCE(snapshot.contact_phone_e164, ''),
    registration.participation_status,
    CASE WHEN EXISTS (
        SELECT 1 FROM xiangwan_admin_grants AS financial_grant
        WHERE financial_grant.tenant_id = $1
          AND financial_grant.principal_id = $6
          AND financial_grant.domain_code = 'xiangwan'
          AND financial_grant.grant_status = 'active'
          AND financial_grant.scope_type = 'tenant'
          AND financial_grant.scope_id IS NULL
          AND financial_grant.capability IN ('super_admin', 'activity_operator')
    ) THEN COALESCE(activity_order.payment_status, 'not_required') END,
    CASE WHEN EXISTS (
        SELECT 1 FROM xiangwan_admin_grants AS financial_grant
        WHERE financial_grant.tenant_id = $1
          AND financial_grant.principal_id = $6
          AND financial_grant.domain_code = 'xiangwan'
          AND financial_grant.grant_status = 'active'
          AND financial_grant.scope_type = 'tenant'
          AND financial_grant.scope_id IS NULL
          AND financial_grant.capability IN ('super_admin', 'activity_operator')
    ) THEN COALESCE(refund.refund_status, 'none') END,
    COALESCE(checkin.checkin_status, 'not_checked_in'),
    registration.created_at, registration.updated_at
`

const registrationListSQL = `SELECT ` + registrationListItemProjection +
	registrationFilterSQL + `
ORDER BY registration.created_at DESC, registration.id DESC
OFFSET $8 LIMIT $9
`

const registrationDetailSQL = `
SELECT
    registration.id, registration.series_id, registration.instance_id,
    registration.session_id, instance.title, session.title,
    COALESCE(snapshot.contact_name, ''),
    COALESCE(snapshot.contact_phone_e164, ''),
    registration.participation_status,
    CASE WHEN EXISTS (
        SELECT 1 FROM xiangwan_admin_grants AS financial_grant
        WHERE financial_grant.tenant_id = $1
          AND financial_grant.principal_id = $3
          AND financial_grant.domain_code = 'xiangwan'
          AND financial_grant.grant_status = 'active'
          AND financial_grant.scope_type = 'tenant'
          AND financial_grant.scope_id IS NULL
          AND financial_grant.capability IN ('super_admin', 'activity_operator')
    ) THEN COALESCE(activity_order.payment_status, 'not_required') END,
    CASE WHEN EXISTS (
        SELECT 1 FROM xiangwan_admin_grants AS financial_grant
        WHERE financial_grant.tenant_id = $1
          AND financial_grant.principal_id = $3
          AND financial_grant.domain_code = 'xiangwan'
          AND financial_grant.grant_status = 'active'
          AND financial_grant.scope_type = 'tenant'
          AND financial_grant.scope_id IS NULL
          AND financial_grant.capability IN ('super_admin', 'activity_operator')
    ) THEN COALESCE(refund.refund_status, 'none') END,
    COALESCE(checkin.checkin_status, 'not_checked_in'),
    registration.created_at, registration.updated_at,
    CASE WHEN EXISTS (
        SELECT 1 FROM xiangwan_admin_grants AS financial_grant
        WHERE financial_grant.tenant_id = $1
          AND financial_grant.principal_id = $3
          AND financial_grant.domain_code = 'xiangwan'
          AND financial_grant.grant_status = 'active'
          AND financial_grant.scope_type = 'tenant'
          AND financial_grant.scope_id IS NULL
          AND financial_grant.capability IN ('super_admin', 'activity_operator')
    ) THEN COALESCE(snapshot.price_cents, 0) END,
    COALESCE(snapshot.instance_publication_version, 0),
    COALESCE(snapshot.session_version, 0),
    registration.confirmed_at, registration.cancelled_at,
    registration.cancellation_reason, checkin.id, checkin.checked_in_at,
    CASE WHEN EXISTS (
        SELECT 1 FROM xiangwan_admin_grants AS financial_grant
        WHERE financial_grant.tenant_id = $1
          AND financial_grant.principal_id = $3
          AND financial_grant.domain_code = 'xiangwan'
          AND financial_grant.grant_status = 'active'
          AND financial_grant.scope_type = 'tenant'
          AND financial_grant.scope_id IS NULL
          AND financial_grant.capability IN ('super_admin', 'activity_operator')
    ) THEN COALESCE(refund.successful_refund_cents, 0) END,
    COALESCE(snapshot.privacy_policy_version, '')
FROM xiangwan_registrations AS registration
JOIN xiangwan_activity_instances AS instance
  ON instance.tenant_id = registration.tenant_id
 AND instance.id = registration.instance_id
JOIN xiangwan_activity_sessions AS session
  ON session.tenant_id = registration.tenant_id
 AND session.id = registration.session_id
LEFT JOIN xiangwan_registration_snapshots AS snapshot
  ON snapshot.tenant_id = registration.tenant_id
 AND snapshot.registration_id = registration.id
LEFT JOIN xiangwan_orders AS activity_order
  ON activity_order.tenant_id = registration.tenant_id
 AND activity_order.registration_id = registration.id
LEFT JOIN xiangwan_refund_cases AS refund
  ON refund.tenant_id = registration.tenant_id
 AND refund.registration_id = registration.id
LEFT JOIN xiangwan_checkins AS checkin
  ON checkin.tenant_id = registration.tenant_id
 AND checkin.registration_id = registration.id
WHERE registration.tenant_id = $1
  AND registration.id = $2
  AND EXISTS (
      SELECT 1
      FROM principals AS principal
      JOIN xiangwan_admin_identity_links AS identity_link
       ON identity_link.tenant_id = $1
       AND identity_link.principal_id = principal.id
       AND identity_link.link_status = 'active'
       AND identity_link.id = $4
      JOIN xiangwan_admin_grants AS admin_grant
        ON admin_grant.tenant_id = $1
       AND admin_grant.principal_id = principal.id
       AND admin_grant.domain_code = 'xiangwan'
       AND admin_grant.grant_status = 'active'
      WHERE principal.id = $3
        AND principal.status = 'active'
        AND principal.deleted_at IS NULL
        AND principal.primary_tenant_id = $1
        AND (
            (
                admin_grant.capability IN ('super_admin', 'activity_operator')
                AND admin_grant.scope_type = 'tenant'
                AND admin_grant.scope_id IS NULL
            )
            OR (
                admin_grant.capability = 'onsite_checkin'
                AND (
                    (
                        admin_grant.scope_type = 'tenant'
                        AND admin_grant.scope_id IS NULL
                    )
                    OR (
                        admin_grant.scope_type = 'session'
                        AND admin_grant.scope_id = registration.session_id
                    )
                )
            )
        )
  )
`

type registrationRowScanner interface {
	Scan(...any) error
}

func scanRegistrationItem(row registrationRowScanner) (
	xiangwanadmin.RegistrationItem,
	error,
) {
	var item xiangwanadmin.RegistrationItem
	var contactName string
	var contactPhone string
	var paymentStatus sql.NullString
	var refundStatus sql.NullString
	err := row.Scan(
		&item.ID, &item.SeriesID, &item.InstanceID, &item.SessionID,
		&item.InstanceTitle, &item.SessionTitle, &contactName, &contactPhone,
		&item.ParticipationStatus, &paymentStatus, &refundStatus,
		&item.CheckinStatus, &item.CreatedAt, &item.UpdatedAt,
	)
	if err != nil {
		return xiangwanadmin.RegistrationItem{}, err
	}
	item.ContactNameMasked = maskContactName(contactName)
	item.ContactPhoneMasked = maskContactPhone(contactPhone)
	item.PaymentStatus = adminNullStringPointer(paymentStatus)
	item.RefundStatus = adminNullStringPointer(refundStatus)
	return item, nil
}

func validOptionalID(value *uuid.UUID) bool {
	return value == nil || *value != uuid.Nil
}

func validParticipationState(value string) bool {
	switch value {
	case "", "pending_payment", "confirmed", "cancelled":
		return true
	default:
		return false
	}
}

func maskContactName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "未留姓名"
	}
	first, _ := utf8.DecodeRuneInString(value)
	return string(first) + "**"
}

func maskContactPhone(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) < 8 {
		if len(runes) == 0 {
			return "未留号码"
		}
		return strings.Repeat("*", len(runes))
	}
	return string(runes[:3]) + "****" + string(runes[len(runes)-4:])
}

func adminNullTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time
	return &result
}

func adminNullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func adminNullInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
