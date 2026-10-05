package resourcepostgres

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestAuthorizePublicFileRequiresExactPublishedBlockChain(t *testing.T) {
	t.Parallel()

	grant := knownPublicFileAdapterGrant()
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return publicFileGrantRows(grant), nil
		},
	}}

	got, err := repository.AuthorizePublicFile(
		context.Background(),
		grant.TenantID,
		grant.RelationID,
		grant.BlockID,
		grant.FileID,
	)
	if err != nil || !reflect.DeepEqual(got, grant) {
		t.Fatalf("AuthorizePublicFile() = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{
		grant.TenantID,
		grant.RelationID,
		grant.BlockID,
		grant.FileID,
	}) {
		t.Fatalf("AuthorizePublicFile() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"JOIN xiangwan_resource_content_snapshots AS snapshot",
		"approval.subject_digest = snapshot.subject_digest",
		"approval.decision = 'approved'",
		"content.updated_at = relation.content_revision_at",
		"content_block.id = $3",
		"stored_file.id = $4",
		"content_block.data->>'file_id'",
		"stored_file.principal_id = relation.created_by",
		"stored_file.status = 'confirmed'",
		"stored_file.delete_after IS NULL",
		"relation.tenant_id = $1",
		"relation.id = $2",
		"relation.access_policy = 'public'",
		"publication.access_policy = 'public'",
		"activity_instance.version = relation.expected_target_version",
		"activity_session.version = relation.expected_target_version",
		"LIMIT 2",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("public-file query does not contain %q", fragment)
		}
	}
}

func TestAuthorizePublicFileRejectsInvalidInputBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			t.Fatal("invalid public-file identity reached PostgreSQL")
			return nil, nil
		},
	}}
	if _, err := repository.AuthorizePublicFile(
		context.Background(),
		uuid.Nil,
		uuid.New(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrInvalidPublicFileQuery) {
		t.Fatalf("AuthorizePublicFile(invalid) error = %v", err)
	}
}

func TestAuthorizePublicFileReturnsOpaqueNotFound(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			return &fakePublishedResourceRows{}, nil
		},
	}}
	if _, err := repository.AuthorizePublicFile(
		context.Background(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
		uuid.New(),
	); !errors.Is(err, ErrPublicFileNotFound) {
		t.Fatalf("AuthorizePublicFile(not found) error = %v", err)
	}
}

func TestScanPublicFileGrantRejectsUnsafeOrDuplicateFacts(t *testing.T) {
	t.Parallel()

	unsafe := knownPublicFileAdapterGrant()
	unsafe.MIME = "image/svg+xml"
	if _, err := scanPublicFileGrant(
		publicFileGrantRows(unsafe),
	); !errors.Is(err, ErrPublicFileFactsConflict) {
		t.Fatalf("scanPublicFileGrant(unsafe) error = %v", err)
	}
	valid := knownPublicFileAdapterGrant()
	if _, err := scanPublicFileGrant(
		publicFileGrantRows(valid, valid),
	); !errors.Is(err, ErrPublicFileFactsConflict) {
		t.Fatalf("scanPublicFileGrant(duplicate) error = %v", err)
	}
}

func knownPublicFileAdapterGrant() resource.PublicFileGrant {
	return resource.PublicFileGrant{
		TenantID:   uuid.New(),
		RelationID: uuid.New(),
		ContentID:  uuid.New(),
		BlockID:    uuid.New(),
		FileID:     uuid.New(),
		SeriesID:   uuid.New(),
		InstanceID: uuid.New(),
		Kind:       resource.RelationKindInstanceReview,
		BlockType:  resource.PublicReviewBlockTypeImage,
		MIME:       "image/webp",
		Size:       128,
	}
}

func publicFileGrantRows(
	grants ...resource.PublicFileGrant,
) *fakePublishedResourceRows {
	rows := &fakePublishedResourceRows{
		values: make([][]any, 0, len(grants)),
	}
	for _, grant := range grants {
		sessionID := uuid.NullUUID{}
		if grant.SessionID != nil {
			sessionID = uuid.NullUUID{UUID: *grant.SessionID, Valid: true}
		}
		rows.values = append(rows.values, []any{
			grant.TenantID,
			grant.RelationID,
			grant.ContentID,
			grant.BlockID,
			grant.FileID,
			grant.SeriesID,
			grant.InstanceID,
			sessionID,
			grant.Kind,
			grant.BlockType,
			grant.MIME,
			grant.Size,
		})
	}
	return rows
}
