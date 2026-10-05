package postgres

import (
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	peoplepostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people/postgres"
	"context"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"regexp"
	"strings"
)

var hostPolicyReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,99}$`)

func readHostRules(ctx context.Context, q RowQueryer, tenant uuid.UUID) (v xiangwanadmin.HostRuleVersion, err error) {
	err = q.QueryRowContext(ctx, `SELECT version,application_cycle,policy_version,requirements,benefits,enabled,approved_at FROM xiangwan_host_rule_versions WHERE tenant_id=$1 ORDER BY version DESC LIMIT 1`, tenant).Scan(&v.Version, &v.ApplicationCycle, &v.PolicyVersion, &v.Requirements, &v.Benefits, &v.Enabled, &v.ApprovedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return v, nil
	}
	return
}
func (catalog *Catalog) GetHostRules(ctx context.Context, p xiangwanadmin.Principal) (xiangwanadmin.HostRuleVersion, error) {
	if !catalog.valid(ctx) {
		return xiangwanadmin.HostRuleVersion{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, p)
	if err != nil {
		return xiangwanadmin.HostRuleVersion{}, err
	}
	defer func() { _ = tx.Rollback() }()
	v, err := readHostRules(ctx, tx, catalog.tenantID)
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}
func (catalog *Catalog) PublishHostRules(ctx context.Context, c xiangwanadmin.PublishHostRulesCommand) (result xiangwanadmin.HostRuleVersion, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, c.ActorID, c.IdentityLinkID, c.OperationID, "host_rules.publish", "host_rules", catalog.tenantID, c.RequestID, resultErr)
	}()
	c.Reason = strings.TrimSpace(c.Reason)
	c.Rules.Requirements = strings.TrimSpace(c.Rules.Requirements)
	c.Rules.Benefits = strings.TrimSpace(c.Rules.Benefits)
	if !catalog.valid(ctx) || !validWriteIdentity(c.ActorID, c.IdentityLinkID, c.OperationID, c.RequestID) || c.ExpectedVersion < 0 || !hostPolicyReference.MatchString(c.Rules.ApplicationCycle) || !hostPolicyReference.MatchString(c.Rules.PolicyVersion) || c.Reason == "" || len([]rune(c.Reason)) > 500 || c.Rules.Requirements == "" || len([]rune(c.Rules.Requirements)) > 4000 || c.Rules.Benefits == "" || len([]rune(c.Rules.Benefits)) > 4000 {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	// Ignore server-owned receipt fields when digesting the supplied policy.
	c.Rules.Version = 0
	at := catalog.now().UTC()
	digest, err := commandDigest(struct {
		Expected                                      int64
		Cycle, Policy, Requirements, Benefits, Reason string
		Enabled                                       bool
	}{c.ExpectedVersion, c.Rules.ApplicationCycle, c.Rules.PolicyVersion, c.Rules.Requirements, c.Rules.Benefits, c.Reason, c.Rules.Enabled})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, c.ActorID, c.IdentityLinkID, c.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = catalog.authorizer.RequireSuperAdminIdentity(ctx, tx, c.ActorID, c.IdentityLinkID); err != nil {
		return result, err
	}
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.HostRuleVersion]](ctx, tx, catalog.tenantID, c.ActorID, c.OperationID, "host_rules.publish", digest); err != nil {
		return result, err
	} else if replay {
		return receipt.Value, tx.Commit()
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "xiangwan:host-rules:"+catalog.tenantID.String()); err != nil {
		return result, err
	}
	current, err := readHostRules(ctx, tx, catalog.tenantID)
	if err != nil {
		return result, err
	}
	if current.Version != c.ExpectedVersion {
		return result, xiangwanadmin.ErrVersionConflict
	}
	var changed bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM xiangwan_host_rule_versions WHERE tenant_id=$1 AND policy_version=$2 AND (application_cycle<>$3 OR requirements<>$4 OR benefits<>$5))`, catalog.tenantID, c.Rules.PolicyVersion, c.Rules.ApplicationCycle, c.Rules.Requirements, c.Rules.Benefits).Scan(&changed); err != nil {
		return result, err
	}
	if changed {
		return result, xiangwanadmin.ErrVersionConflict
	}
	result = c.Rules
	result.Version = current.Version + 1
	result.ApprovedAt = at
	if _, err = tx.ExecContext(ctx, `INSERT INTO xiangwan_host_rule_versions(id,tenant_id,version,application_cycle,policy_version,requirements,benefits,enabled,approved_by,identity_link_id,approval_reason,approved_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, c.OperationID, catalog.tenantID, result.Version, result.ApplicationCycle, result.PolicyVersion, result.Requirements, result.Benefits, result.Enabled, c.ActorID, c.IdentityLinkID, c.Reason, at); err != nil {
		return result, err
	}
	if err = writeOperationAndAudit(ctx, tx, catalog.tenantID, c.ActorID, c.IdentityLinkID, c.OperationID, "host_rules.publish", digest, operationResult[xiangwanadmin.HostRuleVersion]{Value: result}, catalog.tenantID, result.Version, c.RequestID, at); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func hostApplicationItem(a people.HostApplication) xiangwanadmin.HostApplicationItem {
	v := xiangwanadmin.HostApplicationItem{ID: a.ID, ApplicationCycle: a.ApplicationCycle, PolicyVersion: a.PolicyVersion, Status: a.ApplicationStatus, Version: a.Version, SubmittedAt: a.SubmittedAt, UpdatedAt: a.UpdatedAt}
	if a.ReviewComment != nil {
		v.ReviewComment = *a.ReviewComment
	}
	return v
}
func (catalog *Catalog) ListHostApplications(ctx context.Context, p xiangwanadmin.Principal, status string, page, size int) (result xiangwanadmin.HostApplicationPage, err error) {
	if !catalog.valid(ctx) {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	page, size, err = normalizePage(page, size)
	if err != nil || page-1 > maxPostgresInteger/size || (status != "" && status != "pending" && status != "approved" && status != "rejected" && status != "withdrawn") {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.beginAuthorizedRead(ctx, p)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	result.Page = page
	result.PageSize = size
	result.Items = []xiangwanadmin.HostApplicationItem{}
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM xiangwan_host_applications WHERE tenant_id=$1 AND ($2='' OR application_status=$2)`, catalog.tenantID, status).Scan(&result.Total); err != nil {
		return result, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,application_cycle,policy_version,application_status,COALESCE(review_comment,''),version,submitted_at,updated_at FROM xiangwan_host_applications WHERE tenant_id=$1 AND ($2='' OR application_status=$2) ORDER BY submitted_at DESC,id DESC OFFSET $3 LIMIT $4`, catalog.tenantID, status, (page-1)*size, size)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v xiangwanadmin.HostApplicationItem
		if err = rows.Scan(&v.ID, &v.ApplicationCycle, &v.PolicyVersion, &v.Status, &v.ReviewComment, &v.Version, &v.SubmittedAt, &v.UpdatedAt); err != nil {
			return result, err
		}
		result.Items = append(result.Items, v)
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	rows.Close()
	return result, tx.Commit()
}
func (catalog *Catalog) GetHostApplication(ctx context.Context, p xiangwanadmin.Principal, id uuid.UUID, purpose, request string) (result xiangwanadmin.HostApplicationDetail, err error) {
	if !catalog.valid(ctx) || id == uuid.Nil || purpose != "host_application_review" || request == "" {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	tx, err := catalog.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	var generation uuid.UUID
	if err = tx.QueryRowContext(ctx, `SELECT active_generation_id FROM xiangwan_runtime_generations WHERE tenant_id=$1 AND bootstrap_completed_at IS NOT NULL FOR SHARE`, catalog.tenantID).Scan(&generation); err != nil || generation != catalog.generationID {
		return result, xiangwanadmin.ErrVersionConflict
	}
	if err = catalog.authorizer.RequireActivityOperatorIdentity(ctx, tx, p.PrincipalID, p.IdentityLinkID); err != nil {
		return result, err
	}
	a, err := peoplepostgres.NewRepository(tx).GetHostApplication(ctx, catalog.tenantID, id)
	if errors.Is(err, peoplepostgres.ErrHostApplicationNotFound) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, err
	}
	result = xiangwanadmin.HostApplicationDetail{HostApplicationItem: hostApplicationItem(a), PersonalIntroduction: a.PersonalIntroduction, RelevantExperience: a.RelevantExperience, Availability: a.Availability, ContactMethod: a.ContactMethod}
	if _, err = tx.ExecContext(ctx, `INSERT INTO xiangwan_admin_audit_events(id,tenant_id,actor_id,action_code,target_type,target_id,request_id,details,occurred_at,created_at) VALUES($1,$2,$3,'host_application.detail_read','host_application',$4,$5,jsonb_build_object('purpose',$6::text,'identity_link_id',$7::text),clock_timestamp(),clock_timestamp())`, uuid.New(), catalog.tenantID, p.PrincipalID, id, request, purpose, p.IdentityLinkID.String()); err != nil {
		return result, err
	}
	return result, tx.Commit()
}
func (catalog *Catalog) ReviewHostApplication(ctx context.Context, c xiangwanadmin.ReviewHostApplicationCommand) (result xiangwanadmin.HostApplicationItem, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(ctx, c.ActorID, c.IdentityLinkID, c.OperationID, "host_application.review", "host_application", c.ApplicationID, c.RequestID, resultErr)
	}()
	c.Comment = strings.TrimSpace(c.Comment)
	if !catalog.valid(ctx) || !validWriteIdentity(c.ActorID, c.IdentityLinkID, c.OperationID, c.RequestID) || c.ApplicationID == uuid.Nil || c.ExpectedVersion < 1 || c.Comment == "" || len([]rune(c.Comment)) > people.MaxHostApplicationReviewRunes || (c.Decision != people.HostApplicationStatusApproved && c.Decision != people.HostApplicationStatusRejected) {
		return result, xiangwanadmin.ErrInvalidCatalogRequest
	}
	digest, err := commandDigest(struct {
		ID       uuid.UUID
		Version  int64
		Decision people.HostApplicationStatus
		Comment  string
	}{c.ApplicationID, c.ExpectedVersion, c.Decision, c.Comment})
	if err != nil {
		return result, err
	}
	tx, err := catalog.beginActivityWrite(ctx, c.ActorID, c.IdentityLinkID, c.OperationID)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if receipt, replay, err := readOperation[operationResult[xiangwanadmin.HostApplicationItem]](ctx, tx, catalog.tenantID, c.ActorID, c.OperationID, "host_application.review", digest); err != nil {
		return result, err
	} else if replay {
		return receipt.Value, tx.Commit()
	}
	repo := peoplepostgres.NewRepository(tx)
	a, err := repo.GetHostApplicationForUpdate(ctx, catalog.tenantID, c.ApplicationID)
	if errors.Is(err, peoplepostgres.ErrHostApplicationNotFound) {
		return result, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return result, err
	}
	if a.Version != c.ExpectedVersion || a.ApplicationStatus != people.HostApplicationStatusPending {
		return result, xiangwanadmin.ErrVersionConflict
	}
	at := catalog.now().UTC()
	updated, _, err := people.ReviewHostApplication(a, people.ReviewHostApplicationCommand{Decision: c.Decision, ActorID: c.ActorID, Comment: c.Comment, At: at})
	if err != nil {
		return result, err
	}
	updated, err = repo.UpdateHostApplication(ctx, updated, c.ExpectedVersion)
	if err != nil {
		return result, err
	}
	result = hostApplicationItem(updated)
	if err = writeOperationAndAudit(ctx, tx, catalog.tenantID, c.ActorID, c.IdentityLinkID, c.OperationID, "host_application.review", digest, operationResult[xiangwanadmin.HostApplicationItem]{Value: result}, result.ID, result.Version, c.RequestID, at); err != nil {
		return result, err
	}
	return result, tx.Commit()
}

var _ xiangwanadmin.HostApplicationAdministration = (*Catalog)(nil)
