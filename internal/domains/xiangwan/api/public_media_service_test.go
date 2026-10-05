package xiangwanapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/capabilities/storage/publicobject"
	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/resource"
	"github.com/google/uuid"
)

func TestPublicMediaServiceAuthorizesBeforeOpeningExactObject(t *testing.T) {
	t.Parallel()

	grant := knownPublicMediaGrant()
	authorizer := &fakePublicFileAuthorizer{grant: grant}
	objects := &fakePublicObjectReader{object: publicMediaObject(grant)}
	service, err := NewPublicMediaService(grant.TenantID, authorizer, objects)
	if err != nil {
		t.Fatalf("NewPublicMediaService() error = %v", err)
	}
	media, err := service.Open(
		context.Background(),
		grant.RelationID,
		grant.BlockID,
		grant.FileID,
	)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = media.Object.Close() }()
	if media.Grant != grant || objects.fileID != grant.FileID ||
		!reflect.DeepEqual(authorizer.args, []uuid.UUID{
			grant.TenantID,
			grant.RelationID,
			grant.BlockID,
			grant.FileID,
		}) {
		t.Fatalf("Open() media=%+v authorizer=%+v objects=%+v", media, authorizer, objects)
	}
}

func TestPublicMediaServiceClosesDriftedObject(t *testing.T) {
	t.Parallel()

	grant := knownPublicMediaGrant()
	closer := &trackingCloser{}
	object := publicMediaObject(grant)
	object.Size++
	object.Closer = closer
	service, err := NewPublicMediaService(
		grant.TenantID,
		&fakePublicFileAuthorizer{grant: grant},
		&fakePublicObjectReader{object: object},
	)
	if err != nil {
		t.Fatalf("NewPublicMediaService() error = %v", err)
	}
	if _, err := service.Open(
		context.Background(),
		grant.RelationID,
		grant.BlockID,
		grant.FileID,
	); !errors.Is(err, ErrPublicMediaFactsConflict) || !closer.closed {
		t.Fatalf("Open(drift) error=%v closed=%v", err, closer.closed)
	}
}

func TestPublicMediaServiceDoesNotOpenUnauthorizedObject(t *testing.T) {
	t.Parallel()

	grant := knownPublicMediaGrant()
	objects := &fakePublicObjectReader{}
	service, err := NewPublicMediaService(
		grant.TenantID,
		&fakePublicFileAuthorizer{err: resource.ErrPublicFileUnavailable},
		objects,
	)
	if err != nil {
		t.Fatalf("NewPublicMediaService() error = %v", err)
	}
	_, err = service.Open(
		context.Background(),
		grant.RelationID,
		grant.BlockID,
		grant.FileID,
	)
	if !publicMediaNotFound(err) || objects.calls != 0 {
		t.Fatalf("Open(unauthorized) error=%v object calls=%d", err, objects.calls)
	}
}

func knownPublicMediaGrant() resource.PublicFileGrant {
	return resource.PublicFileGrant{
		TenantID:   apiUUID(70),
		RelationID: apiUUID(71),
		ContentID:  apiUUID(72),
		BlockID:    apiUUID(73),
		FileID:     apiUUID(74),
		SeriesID:   apiUUID(75),
		InstanceID: apiUUID(76),
		Kind:       resource.RelationKindInstanceReview,
		BlockType:  resource.PublicReviewBlockTypeImage,
		MIME:       "image/png",
		Size:       8,
	}
}

func publicMediaObject(grant resource.PublicFileGrant) *publicobject.Object {
	content := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	return &publicobject.Object{
		FileID:       grant.FileID,
		MIME:         grant.MIME,
		DetectedMIME: grant.MIME,
		Size:         int64(len(content)),
		Content:      bytes.NewReader(content),
		Closer:       io.NopCloser(bytes.NewReader(nil)),
	}
}

type fakePublicFileAuthorizer struct {
	grant resource.PublicFileGrant
	err   error
	args  []uuid.UUID
}

func (fake *fakePublicFileAuthorizer) AuthorizePublicFile(
	_ context.Context,
	tenantID uuid.UUID,
	relationID uuid.UUID,
	blockID uuid.UUID,
	fileID uuid.UUID,
) (resource.PublicFileGrant, error) {
	fake.args = []uuid.UUID{tenantID, relationID, blockID, fileID}
	return fake.grant, fake.err
}

type fakePublicObjectReader struct {
	object *publicobject.Object
	err    error

	calls  int
	fileID uuid.UUID
}

func (fake *fakePublicObjectReader) OpenConfirmedObject(
	_ context.Context,
	fileID uuid.UUID,
) (*publicobject.Object, error) {
	fake.calls++
	fake.fileID = fileID
	return fake.object, fake.err
}

type trackingCloser struct {
	closed bool
}

func (closer *trackingCloser) Close() error {
	closer.closed = true
	return nil
}
