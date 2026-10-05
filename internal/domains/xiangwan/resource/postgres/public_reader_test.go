package resourcepostgres

import (
	"context"
	"crypto/sha256"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestListPublishedInstanceReviewUsesCompletePublicationChain(
	t *testing.T,
) {
	t.Parallel()

	value := knownPublishedResource()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return publishedResourceRows(value), nil
		},
	}}
	got, err := repository.ListPublishedInstanceReview(
		context.Background(),
		value.TenantID,
		value.InstanceID,
	)
	if err != nil || len(got) != 1 ||
		got[0].PublicationID != value.PublicationID ||
		got[0].SubjectDigest != value.SubjectDigest {
		t.Fatalf("ListPublishedInstanceReview() = %+v, %v", got, err)
	}
	for _, fragment := range []string{
		"JOIN xiangwan_resource_content_snapshots AS snapshot",
		"approval.subject_digest = snapshot.subject_digest",
		"approval.decision = 'approved'",
		"instance.status IN ('completed', 'archived')",
		"instance.version = relation.expected_target_version",
		"relation.access_policy = 'public'",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("Instance review query does not contain %q", fragment)
		}
	}
	if !reflect.DeepEqual(
		capturedArgs,
		[]any{value.TenantID, value.InstanceID},
	) {
		t.Fatalf("ListPublishedInstanceReview() args = %#v", capturedArgs)
	}
}

func TestListPublishedSessionResourcesRequiresConfirmedRegistration(
	t *testing.T,
) {
	t.Parallel()

	value := knownPublishedResource()
	sessionID := uuid.New()
	value.SessionID = &sessionID
	value.Kind = resource.RelationKindSessionResources
	value.AccessPolicy = resource.AccessPolicyConfirmedRegistration
	principalID := uuid.New()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return publishedResourceRows(value), nil
		},
	}}
	got, err := repository.ListPublishedSessionResources(
		context.Background(),
		value.TenantID,
		sessionID,
		&principalID,
	)
	if err != nil || len(got) != 1 || got[0].SessionID == nil ||
		*got[0].SessionID != sessionID {
		t.Fatalf("ListPublishedSessionResources() = %+v, %v", got, err)
	}
	for _, fragment := range []string{
		"session.version = relation.expected_target_version",
		"relation.access_policy = 'confirmed_registration'",
		"registration.principal_id = $3",
		"registration.participation_status = 'confirmed'",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("Session resource query does not contain %q", fragment)
		}
	}
	if !reflect.DeepEqual(
		capturedArgs,
		[]any{value.TenantID, sessionID, principalID},
	) {
		t.Fatalf("ListPublishedSessionResources() args = %#v", capturedArgs)
	}
}

func TestListPublishedSessionResourcesAllowsAnonymousPublicQuery(
	t *testing.T,
) {
	t.Parallel()

	tenantID := uuid.New()
	sessionID := uuid.New()
	var principalArgument any = "not-called"
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(_ string, args ...any) (resourceRowsScanner, error) {
			principalArgument = args[2]
			return &fakePublishedResourceRows{}, nil
		},
	}}
	got, err := repository.ListPublishedSessionResources(
		context.Background(),
		tenantID,
		sessionID,
		nil,
	)
	if err != nil || len(got) != 0 || principalArgument != nil {
		t.Fatalf(
			"ListPublishedSessionResources(anonymous) = %+v, %v, principal=%#v",
			got,
			err,
			principalArgument,
		)
	}
}

func TestListPublishedResourcesRejectsInvalidScopeBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			t.Fatal("invalid public resource scope reached PostgreSQL")
			return nil, nil
		},
	}}
	nilPrincipal := uuid.Nil
	if _, err := repository.ListPublishedSessionResources(
		context.Background(),
		uuid.New(),
		uuid.New(),
		&nilPrincipal,
	); !errors.Is(err, ErrInvalidPublicResourceQuery) {
		t.Fatalf("ListPublishedSessionResources(invalid) error = %v", err)
	}
}

func TestScanPublishedResourcesRejectsCorruptDigest(t *testing.T) {
	t.Parallel()

	value := knownPublishedResource()
	rows := publishedResourceRows(value)
	rows.values[0][14] = []byte("short")
	if _, err := scanPublishedResources(rows); !errors.Is(
		err,
		ErrPublishedResourceFactsConflict,
	) {
		t.Fatalf("scanPublishedResources(corrupt) error = %v", err)
	}
}

func TestScanPublishedResourcesRejectsUnboundedContext(t *testing.T) {
	t.Parallel()

	values := make(
		[]resource.PublishedResource,
		resource.MaxPublishedResourcesPerContext+1,
	)
	for index := range values {
		values[index] = knownPublishedResource()
		values[index].SortOrder = index
	}
	if _, err := scanPublishedResources(
		publishedResourceRows(values...),
	); !errors.Is(err, ErrPublishedResourceFactsConflict) {
		t.Fatalf("scanPublishedResources(unbounded) error = %v", err)
	}
}

func knownPublishedResource() resource.PublishedResource {
	now := time.Date(2026, time.September, 13, 18, 0, 0, 0, time.UTC)
	digest := sha256.Sum256([]byte("resource snapshot"))
	return resource.PublishedResource{
		PublicationID:         uuid.New(),
		RelationID:            uuid.New(),
		TenantID:              uuid.New(),
		SeriesID:              uuid.New(),
		InstanceID:            uuid.New(),
		Kind:                  resource.RelationKindInstanceReview,
		ContentID:             uuid.New(),
		ContentRevision:       now.Add(-time.Minute),
		AccessPolicy:          resource.AccessPolicyPublic,
		SortOrder:             1,
		TargetVersion:         2,
		ApprovalObservationID: uuid.New(),
		SnapshotSchema:        resource.ContentSnapshotSchema,
		SubjectDigest:         digest,
		PublishedAt:           now,
	}
}

func publishedResourceRows(
	values ...resource.PublishedResource,
) *fakePublishedResourceRows {
	result := &fakePublishedResourceRows{
		values: make([][]any, 0, len(values)),
	}
	for _, value := range values {
		sessionID := uuid.NullUUID{}
		if value.SessionID != nil {
			sessionID = uuid.NullUUID{UUID: *value.SessionID, Valid: true}
		}
		result.values = append(result.values, []any{
			value.PublicationID, value.RelationID, value.TenantID,
			value.SeriesID, value.InstanceID, sessionID, value.Kind,
			value.ContentID, value.ContentRevision, value.AccessPolicy,
			value.SortOrder, value.TargetVersion,
			value.ApprovalObservationID, value.SnapshotSchema,
			append([]byte(nil), value.SubjectDigest[:]...), value.PublishedAt,
		})
	}
	return result
}

type fakePublishedResourceRows struct {
	values [][]any
	index  int
	err    error
	closed bool
}

func (rows *fakePublishedResourceRows) Next() bool {
	if rows.index >= len(rows.values) {
		return false
	}
	rows.index++
	return true
}

func (rows *fakePublishedResourceRows) Scan(destinations ...any) error {
	if rows.index == 0 || rows.index > len(rows.values) {
		return errors.New("fake published resource row is not positioned")
	}
	return (&fakeResourceRow{values: rows.values[rows.index-1]}).Scan(
		destinations...,
	)
}

func (rows *fakePublishedResourceRows) Err() error {
	return rows.err
}

func (rows *fakePublishedResourceRows) Close() error {
	rows.closed = true
	return nil
}
