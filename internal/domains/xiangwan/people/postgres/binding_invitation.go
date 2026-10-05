package peoplepostgres

import (
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/people"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"regexp"
	"strings"
	"time"
)

var (
	ErrBindingInvitationInvalid            = errors.New("invalid People binding invitation request")
	ErrBindingInvitationUnavailable        = errors.New("People binding invitation unavailable")
	ErrBindingInvitationConflict           = errors.New("People binding invitation facts changed")
	ErrBindingInvitationGenerationInactive = errors.New("People binding invitation generation inactive")
	bindingInvitationCodePattern           = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}\.[A-Za-z0-9_-]{43}$`)
)

type BindingInvitationPreview struct {
	PeopleProfileID uuid.UUID `json:"people_profile_id"`
	DisplayName     string    `json:"display_name"`
	Headline        *string   `json:"headline,omitempty"`
	Introduction    string    `json:"introduction"`
	ProfileVersion  int64     `json:"profile_version"`
	ExpiresAt       time.Time `json:"expires_at"`
}
type BindingInvitationConfirmation struct {
	PeopleProfileID uuid.UUID `json:"people_profile_id"`
	Status          string    `json:"status"`
	Duplicate       bool      `json:"duplicate"`
}
type BindingInvitationApprover func(context.Context, *sql.Tx, uuid.UUID, uuid.UUID) error
type BindingInvitationAcceptor struct {
	db                     *sql.DB
	tenantID, generationID uuid.UUID
	privacyPolicy          string
	authorize              BindingInvitationApprover
}

func NewBindingInvitationAcceptor(db *sql.DB, tenantID, generationID uuid.UUID, privacyPolicy string, authorize BindingInvitationApprover) (*BindingInvitationAcceptor, error) {
	if db == nil || tenantID == uuid.Nil || generationID == uuid.Nil || strings.TrimSpace(privacyPolicy) == "" || len(privacyPolicy) > 64 || authorize == nil {
		return nil, ErrBindingInvitationInvalid
	}
	return &BindingInvitationAcceptor{db: db, tenantID: tenantID, generationID: generationID, privacyPolicy: privacyPolicy, authorize: authorize}, nil
}

type bindingInvitationSource struct {
	id, profileID, actorID, identityID uuid.UUID
	version                            int64
	status                             string
	expiresAt                          time.Time
	acceptedBy, bindingID              uuid.NullUUID
	claimOperation                     uuid.NullUUID
	digest                             [32]byte
}

func (a *BindingInvitationAcceptor) begin(ctx context.Context, owner uuid.UUID, code string) (*sql.Tx, bindingInvitationSource, error) {
	var source bindingInvitationSource
	if a == nil || a.db == nil || ctx == nil || owner == uuid.Nil || !bindingInvitationCodePattern.MatchString(code) {
		return nil, source, ErrBindingInvitationInvalid
	}
	tx, err := a.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, source, err
	}
	fail := func(err error) (*sql.Tx, bindingInvitationSource, error) { _ = tx.Rollback(); return nil, source, err }
	var epoch int64
	if err := tx.QueryRowContext(ctx, `SELECT write_epoch FROM xiangwan_runtime_generations WHERE singleton_id=1 AND scope_key='wq-xiangwan' AND tenant_id=$1 AND active_generation_id=$2 AND write_epoch>0 AND bootstrap_completed_at IS NOT NULL FOR SHARE`, a.tenantID, a.generationID).Scan(&epoch); errors.Is(err, sql.ErrNoRows) {
		return fail(ErrBindingInvitationGenerationInactive)
	} else if err != nil {
		return fail(err)
	}
	source.digest = sha256.Sum256([]byte(code))
	if err := tx.QueryRowContext(ctx, `SELECT id,people_profile_id,profile_version,invited_by,identity_link_id,invitation_status,expires_at,accepted_by,binding_id,claim_operation_id FROM xiangwan_people_binding_invitations WHERE tenant_id=$1 AND code_digest=$2`, a.tenantID, source.digest[:]).Scan(&source.id, &source.profileID, &source.version, &source.actorID, &source.identityID, &source.status, &source.expiresAt, &source.acceptedBy, &source.bindingID, &source.claimOperation); errors.Is(err, sql.ErrNoRows) {
		return fail(ErrBindingInvitationUnavailable)
	} else if err != nil {
		return fail(err)
	}
	if err := a.authorize(ctx, tx, source.actorID, source.identityID); err != nil {
		return fail(ErrBindingInvitationUnavailable)
	}
	var active uuid.UUID
	if err := tx.QueryRowContext(ctx, `SELECT id FROM principals WHERE id=$1 AND primary_tenant_id=$2 AND status='active' AND deleted_at IS NULL FOR SHARE`, owner, a.tenantID).Scan(&active); errors.Is(err, sql.ErrNoRows) {
		return fail(ErrBindingInvitationUnavailable)
	} else if err != nil {
		return fail(err)
	}
	return tx, source, nil
}
func (a *BindingInvitationAcceptor) Preview(ctx context.Context, owner uuid.UUID, code string) (result BindingInvitationPreview, err error) {
	tx, source, err := a.begin(ctx, owner, code)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if source.status != "pending" || !time.Now().UTC().Before(source.expiresAt) {
		return result, ErrBindingInvitationUnavailable
	}
	profile, err := NewRepository(tx).GetProfile(ctx, a.tenantID, source.profileID)
	if err != nil {
		return result, err
	}
	if profile.Version != source.version || profile.ProfileStatus != people.ProfileStatusPublished || profile.ModerationStatus != people.ModerationStatusApproved {
		return result, ErrBindingInvitationConflict
	}
	if _, err := NewRepository(tx).GetActiveBindingByProfile(ctx, a.tenantID, source.profileID); err == nil {
		return result, ErrBindingInvitationConflict
	} else if !errors.Is(err, ErrBindingNotFound) {
		return result, err
	}
	result = BindingInvitationPreview{PeopleProfileID: profile.ID, DisplayName: profile.DisplayName, Headline: profile.Headline, Introduction: profile.Introduction, ProfileVersion: profile.Version, ExpiresAt: source.expiresAt}
	return result, tx.Commit()
}
func (a *BindingInvitationAcceptor) Accept(ctx context.Context, owner, operation uuid.UUID, code string, expectedVersion int64, consent bool) (result BindingInvitationConfirmation, resultErr error) {
	defer func() {
		var pg *pgconn.PgError
		if errors.As(resultErr, &pg) && (pg.Code == "23505" || pg.Code == "40001" || pg.Code == "40P01") {
			resultErr = ErrBindingInvitationConflict
		}
	}()
	if operation == uuid.Nil || operation.Version() != 4 || expectedVersion < 1 || !consent {
		return result, ErrBindingInvitationInvalid
	}
	tx, source, err := a.begin(ctx, owner, code)
	if err != nil {
		return result, err
	}
	defer func() { _ = tx.Rollback() }()
	if source.version != expectedVersion {
		return result, ErrBindingInvitationConflict
	}
	if source.status == "accepted" {
		if !source.acceptedBy.Valid || source.acceptedBy.UUID != owner {
			return result, ErrBindingInvitationUnavailable
		}
		if !source.claimOperation.Valid || source.claimOperation.UUID != operation {
			return result, ErrBindingInvitationConflict
		}
		var status string
		if err := tx.QueryRowContext(ctx, `SELECT binding_status FROM xiangwan_people_bindings WHERE tenant_id=$1 AND id=$2 AND principal_id=$3`, a.tenantID, source.bindingID.UUID, owner).Scan(&status); err != nil {
			return result, err
		}
		return BindingInvitationConfirmation{PeopleProfileID: source.profileID, Status: status, Duplicate: true}, tx.Commit()
	}
	if source.status != "pending" || !time.Now().UTC().Before(source.expiresAt) {
		return result, ErrBindingInvitationUnavailable
	}
	profile, err := NewRepository(tx).GetProfileForUpdate(ctx, a.tenantID, source.profileID)
	if err != nil {
		return result, err
	}
	if profile.Version != source.version || profile.ProfileStatus != people.ProfileStatusPublished || profile.ModerationStatus != people.ModerationStatusApproved {
		return result, ErrBindingInvitationConflict
	}
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT invitation_status FROM xiangwan_people_binding_invitations WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, a.tenantID, source.id).Scan(&status); err != nil {
		return result, err
	}
	if status != "pending" {
		return result, ErrBindingInvitationConflict
	}
	var existing bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM xiangwan_people_bindings WHERE tenant_id=$1 AND binding_status='active' AND (people_profile_id=$2 OR principal_id=$3))`, a.tenantID, source.profileID, owner).Scan(&existing); err != nil {
		return result, err
	}
	if existing {
		return result, ErrBindingInvitationConflict
	}
	now := time.Now().UTC()
	if !now.Before(source.expiresAt) {
		return result, ErrBindingInvitationUnavailable
	}
	evidence := sha256.New()
	_, _ = evidence.Write(source.digest[:])
	_, _ = evidence.Write([]byte(a.tenantID.String() + ":" + source.profileID.String() + ":" + owner.String() + ":" + a.privacyPolicy + ":explicit-consent:v1"))
	var proof people.EvidenceDigest
	copy(proof[:], evidence.Sum(nil))
	b, err := people.NewBinding(people.NewBindingCommand{TenantID: a.tenantID, PeopleProfileID: source.profileID, PrincipalID: owner, EvidenceDigest: proof, ActorID: source.actorID, At: now})
	if err != nil {
		return result, err
	}
	b, err = NewRepository(tx).CreateBinding(ctx, b)
	if err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE xiangwan_people_binding_invitations SET invitation_status='accepted',accepted_by=$3,binding_id=$4,claim_operation_id=$5,consent_policy_version=$6,accepted_at=$7,version=2,updated_at=$7 WHERE tenant_id=$1 AND id=$2 AND invitation_status='pending'`, a.tenantID, source.id, owner, b.ID, operation, a.privacyPolicy, now); err != nil {
		return result, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO xiangwan_admin_audit_events(id,tenant_id,actor_id,action_code,target_type,target_id,request_id,details,occurred_at,created_at) VALUES($1,$2,$3,'people_binding.accept','people_binding',$4,$5,'{}',$6,$6)`, uuid.New(), a.tenantID, owner, b.ID, "binding-consent:"+operation.String(), now); err != nil {
		return result, err
	}
	return BindingInvitationConfirmation{PeopleProfileID: source.profileID, Status: "active"}, tx.Commit()
}
