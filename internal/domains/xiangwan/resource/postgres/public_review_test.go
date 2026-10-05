package resourcepostgres

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestReadPublicReviewUsesCompleteChainAndAllowlistedProjection(
	t *testing.T,
) {
	t.Parallel()

	tenantID := publicReviewAdapterUUID(1)
	sessionID := publicReviewAdapterUUID(4)
	target := resource.PastHighlightReviewTarget{
		SeriesID:   publicReviewAdapterUUID(2),
		InstanceID: publicReviewAdapterUUID(3),
		SessionID:  &sessionID,
	}
	document := resource.PublicReviewDocumentFacts{
		RelationID:        publicReviewAdapterUUID(5),
		ContentID:         publicReviewAdapterUUID(6),
		SeriesID:          target.SeriesID,
		InstanceID:        target.InstanceID,
		Kind:              resource.RelationKindInstanceReview,
		Title:             "September review",
		RelationSortOrder: 1,
	}
	textBlock := resource.PublicReviewBlockFacts{
		BlockID:   publicReviewAdapterUUID(7),
		Type:      resource.PublicReviewBlockTypeText,
		SortOrder: 1,
		Text:      "A useful recap",
	}
	linkBlock := resource.PublicReviewBlockFacts{
		BlockID:     publicReviewAdapterUUID(8),
		Type:        resource.PublicReviewBlockTypeLink,
		SortOrder:   2,
		Label:       "Slides",
		ExternalURL: "https://docs.example.com/slides",
	}
	var capturedQuery string
	var capturedArgs []any
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(query string, args ...any) (resourceRowsScanner, error) {
			capturedQuery = query
			capturedArgs = append([]any(nil), args...)
			return publicReviewRows(
				publicReviewAdapterRow(target, document, &textBlock),
				publicReviewAdapterRow(target, document, &linkBlock),
			), nil
		},
	}}
	policy, err := resource.NewExternalDomainPolicy([]string{"docs.example.com"})
	if err != nil {
		t.Fatalf("NewExternalDomainPolicy() error = %v", err)
	}

	got, err := repository.ReadPublicReview(
		context.Background(),
		tenantID,
		target,
		policy,
	)
	if err != nil || len(got.Documents) != 1 ||
		len(got.Documents[0].Blocks) != 2 ||
		got.Documents[0].Blocks[0].Text != textBlock.Text ||
		got.Documents[0].Blocks[1].ExternalURL != linkBlock.ExternalURL {
		t.Fatalf("ReadPublicReview() = %+v, %v", got, err)
	}
	if !reflect.DeepEqual(capturedArgs, []any{
		tenantID,
		target.SeriesID,
		target.InstanceID,
		sessionID,
	}) {
		t.Fatalf("ReadPublicReview() args = %#v", capturedArgs)
	}
	for _, fragment := range []string{
		"activity_instance.series_id = $2",
		"target_session.id = $4",
		"activity_instance.status IN ('completed', 'archived')",
		"JOIN xiangwan_resource_content_snapshots AS snapshot",
		"approval.subject_digest = snapshot.subject_digest",
		"approval.decision = 'approved'",
		"relation.access_policy = 'public'",
		"publication.access_policy = 'public'",
		"content.updated_at = relation.content_revision_at",
		"relation.session_id = target.session_id",
		"content_block.type IN ('text', 'image', 'link', 'file', 'video', 'audio')",
		"stored_file.status = 'confirmed'",
		"stored_file.delete_after IS NULL",
		"LIMIT 51",
		"LIMIT 502",
	} {
		if !strings.Contains(capturedQuery, fragment) {
			t.Fatalf("public-review query does not contain %q", fragment)
		}
	}
	if strings.Contains(capturedQuery, "content.metadata->>'product_code'") {
		t.Fatal("public-review query must use the Xiangwan resource publication chain, not mutable Content metadata")
	}
}

func TestReadPublicReviewReturnsEmptyForValidTargetWithoutDocuments(
	t *testing.T,
) {
	t.Parallel()

	target := resource.PastHighlightReviewTarget{
		SeriesID:   publicReviewAdapterUUID(20),
		InstanceID: publicReviewAdapterUUID(21),
	}
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			return publicReviewRows(publicReviewEmptyAdapterRow(target)), nil
		},
	}}
	policy, _ := resource.NewExternalDomainPolicy(nil)
	got, err := repository.ReadPublicReview(
		context.Background(),
		publicReviewAdapterUUID(22),
		target,
		policy,
	)
	if err != nil || len(got.Documents) != 0 ||
		got.Target.SeriesID != target.SeriesID ||
		got.Target.InstanceID != target.InstanceID {
		t.Fatalf("ReadPublicReview(empty) = %+v, %v", got, err)
	}
}

func TestReadPublicReviewDistinguishesMissingTarget(t *testing.T) {
	t.Parallel()

	target := resource.PastHighlightReviewTarget{
		SeriesID:   publicReviewAdapterUUID(30),
		InstanceID: publicReviewAdapterUUID(31),
	}
	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			return &fakePublishedResourceRows{}, nil
		},
	}}
	policy, _ := resource.NewExternalDomainPolicy(nil)
	_, err := repository.ReadPublicReview(
		context.Background(),
		publicReviewAdapterUUID(32),
		target,
		policy,
	)
	if !errors.Is(err, ErrPublicReviewTargetNotFound) {
		t.Fatalf("ReadPublicReview(missing) error = %v", err)
	}
}

func TestReadPublicReviewRejectsInvalidScopeBeforeSQL(t *testing.T) {
	t.Parallel()

	repository := &Repository{db: &fakeResourceExecutor{
		query: func(string, ...any) (resourceRowsScanner, error) {
			t.Fatal("invalid public-review scope reached PostgreSQL")
			return nil, nil
		},
	}}
	policy, _ := resource.NewExternalDomainPolicy(nil)
	if _, err := repository.ReadPublicReview(
		context.Background(),
		publicReviewAdapterUUID(40),
		resource.PastHighlightReviewTarget{},
		policy,
	); !errors.Is(err, ErrInvalidPublicReviewQuery) {
		t.Fatalf("ReadPublicReview(invalid) error = %v", err)
	}
}

func TestScanPublicReviewRejectsCrossContextRows(t *testing.T) {
	t.Parallel()

	target := resource.PastHighlightReviewTarget{
		SeriesID:   publicReviewAdapterUUID(50),
		InstanceID: publicReviewAdapterUUID(51),
	}
	document := resource.PublicReviewDocumentFacts{
		RelationID:        publicReviewAdapterUUID(52),
		ContentID:         publicReviewAdapterUUID(53),
		SeriesID:          target.SeriesID,
		InstanceID:        target.InstanceID,
		Kind:              resource.RelationKindInstanceReview,
		RelationSortOrder: 1,
	}
	row := publicReviewAdapterRow(target, document, nil)
	row[0] = uuid.New()
	policy, _ := resource.NewExternalDomainPolicy(nil)
	if _, err := scanPublicReview(
		publicReviewRows(row),
		target,
		policy,
	); !errors.Is(err, ErrPublicReviewFactsConflict) {
		t.Fatalf("scanPublicReview(cross context) error = %v", err)
	}
}

func publicReviewAdapterRow(
	target resource.PastHighlightReviewTarget,
	document resource.PublicReviewDocumentFacts,
	block *resource.PublicReviewBlockFacts,
) []any {
	targetSessionID := uuid.NullUUID{}
	if target.SessionID != nil {
		targetSessionID = uuid.NullUUID{UUID: *target.SessionID, Valid: true}
	}
	documentSessionID := uuid.NullUUID{}
	if document.SessionID != nil {
		documentSessionID = uuid.NullUUID{UUID: *document.SessionID, Valid: true}
	}
	row := make([]any, 23)
	row[0] = target.SeriesID
	row[1] = target.InstanceID
	row[2] = targetSessionID
	row[3] = uuid.NullUUID{UUID: document.RelationID, Valid: true}
	row[4] = uuid.NullUUID{UUID: document.ContentID, Valid: true}
	row[5] = uuid.NullUUID{UUID: document.SeriesID, Valid: true}
	row[6] = uuid.NullUUID{UUID: document.InstanceID, Valid: true}
	row[7] = documentSessionID
	row[8] = sql.NullString{String: string(document.Kind), Valid: true}
	row[9] = sql.NullString{String: document.Title, Valid: true}
	row[10] = sql.NullInt64{Int64: int64(document.RelationSortOrder), Valid: true}
	row[11] = uuid.NullUUID{}
	row[12] = sql.NullString{}
	row[13] = sql.NullInt64{}
	row[14] = sql.NullString{}
	row[15] = sql.NullString{}
	row[16] = sql.NullString{}
	row[17] = sql.NullString{}
	row[18] = uuid.NullUUID{}
	row[19] = sql.NullString{}
	row[20] = sql.NullString{}
	row[21] = sql.NullString{}
	row[22] = sql.NullBool{}
	if block == nil {
		return row
	}
	row[11] = uuid.NullUUID{UUID: block.BlockID, Valid: true}
	row[12] = sql.NullString{String: string(block.Type), Valid: true}
	row[13] = sql.NullInt64{Int64: int64(block.SortOrder), Valid: true}
	row[14] = sql.NullString{String: block.Text, Valid: true}
	row[15] = sql.NullString{String: block.Label, Valid: true}
	row[16] = sql.NullString{String: block.Subtitle, Valid: true}
	row[17] = sql.NullString{String: block.ExternalURL, Valid: true}
	row[22] = sql.NullBool{Bool: block.IsCover, Valid: true}
	if block.FileID != nil {
		row[18] = uuid.NullUUID{UUID: *block.FileID, Valid: true}
		row[19] = sql.NullString{String: block.MIME, Valid: true}
	}
	return row
}

func publicReviewEmptyAdapterRow(
	target resource.PastHighlightReviewTarget,
) []any {
	targetSessionID := uuid.NullUUID{}
	if target.SessionID != nil {
		targetSessionID = uuid.NullUUID{UUID: *target.SessionID, Valid: true}
	}
	row := make([]any, 23)
	row[0] = target.SeriesID
	row[1] = target.InstanceID
	row[2] = targetSessionID
	row[3] = uuid.NullUUID{}
	row[4] = uuid.NullUUID{}
	row[5] = uuid.NullUUID{}
	row[6] = uuid.NullUUID{}
	row[7] = uuid.NullUUID{}
	row[8] = sql.NullString{}
	row[9] = sql.NullString{}
	row[10] = sql.NullInt64{}
	row[11] = uuid.NullUUID{}
	row[12] = sql.NullString{}
	row[13] = sql.NullInt64{}
	row[14] = sql.NullString{}
	row[15] = sql.NullString{}
	row[16] = sql.NullString{}
	row[17] = sql.NullString{}
	row[18] = uuid.NullUUID{}
	row[19] = sql.NullString{}
	row[20] = sql.NullString{}
	row[21] = sql.NullString{}
	row[22] = sql.NullBool{}
	return row
}

func publicReviewRows(values ...[]any) *fakePublishedResourceRows {
	return &fakePublishedResourceRows{values: values}
}

func publicReviewAdapterUUID(lastByte byte) uuid.UUID {
	var value uuid.UUID
	value[len(value)-1] = lastByte
	return value
}
