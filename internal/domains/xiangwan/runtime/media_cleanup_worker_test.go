package xiangwanruntime

import (
	"context"
	"errors"
	"testing"
	"time"

	storagestand "github.com/wzyhn/xiangwanai/internal/capabilities/storage/standalonepg"
	"github.com/google/uuid"
)

type mediaCleanupStoreProbe struct {
	claims    []storagestand.ReviewCleanupClaim
	index     int
	expired   []uuid.UUID
	failed    []uuid.UUID
	markError error
}

func (probe *mediaCleanupStoreProbe) ClaimNext(context.Context) (storagestand.ReviewCleanupClaim, bool, error) {
	if probe.index >= len(probe.claims) {
		return storagestand.ReviewCleanupClaim{}, false, nil
	}
	claim := probe.claims[probe.index]
	probe.index++
	return claim, true, nil
}

func (probe *mediaCleanupStoreProbe) MarkExpired(
	_ context.Context, claim storagestand.ReviewCleanupClaim,
) error {
	probe.expired = append(probe.expired, claim.FileID)
	return probe.markError
}

func (probe *mediaCleanupStoreProbe) RecordFailure(
	_ context.Context, claim storagestand.ReviewCleanupClaim, category string,
) error {
	if category != "provider_delete_failed" {
		return errors.New("unsafe cleanup detail")
	}
	probe.failed = append(probe.failed, claim.FileID)
	return probe.markError
}

type mediaCleanupObjectProbe struct {
	deleted []uuid.UUID
	failID  uuid.UUID
	pruned  int
}

func (probe *mediaCleanupObjectProbe) DeleteSelected(_ context.Context, fileID uuid.UUID) error {
	probe.deleted = append(probe.deleted, fileID)
	if fileID == probe.failID {
		return errors.New("synthetic provider error")
	}
	return nil
}

func (probe *mediaCleanupObjectProbe) PruneStaleTemporaries(
	context.Context, time.Time,
) (int, error) {
	probe.pruned++
	return 0, nil
}

func TestMediaCleanupWorkerCompletesClaimOnlyAfterProviderDelete(t *testing.T) {
	goodID, failedID := uuid.New(), uuid.New()
	store := &mediaCleanupStoreProbe{claims: []storagestand.ReviewCleanupClaim{
		{FileID: goodID, Lease: time.Now()},
		{FileID: failedID, Lease: time.Now()},
	}}
	objects := &mediaCleanupObjectProbe{failID: failedID}
	worker := &MediaCleanupWorker{store: store, objects: objects}
	count, err := worker.processBatch(context.Background())
	if err != nil || count != 2 || store.index != 2 ||
		len(objects.deleted) != 2 || len(store.expired) != 1 ||
		store.expired[0] != goodID || len(store.failed) != 1 ||
		store.failed[0] != failedID {
		t.Fatalf("cleanup count=%d err=%v store=%+v objects=%+v", count, err, store, objects)
	}
}

func TestMediaCleanupWorkerHandlesLostLeaseAndCompletionFailure(t *testing.T) {
	fileID := uuid.New()
	store := &mediaCleanupStoreProbe{
		claims:    []storagestand.ReviewCleanupClaim{{FileID: fileID, Lease: time.Now()}},
		markError: storagestand.ErrReviewCleanupLeaseLost,
	}
	worker := &MediaCleanupWorker{store: store, objects: &mediaCleanupObjectProbe{}}
	if count, err := worker.processBatch(context.Background()); err != nil || count != 1 {
		t.Fatalf("lost lease = %d, %v", count, err)
	}
	store.index = 0
	store.markError = errors.New("database unavailable")
	if count, err := worker.processBatch(context.Background()); count != 1 || err == nil {
		t.Fatalf("completion failure = %d, %v", count, err)
	}
}

func TestLoadMediaCleanupWorkerConfigRequiresOnlyOwnCredentials(t *testing.T) {
	values := map[string]string{
		MediaCleanupDatabaseDSNEnv: "postgres://cleanup:synthetic@db/xiangwan",
		TenantIDEnv:                uuid.NewString(),
		GenerationIDEnv:            uuid.NewString(),
		StorageLocalDirEnv:         "C:/private/xiangwan",
	}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	config, err := LoadMediaCleanupWorkerConfig(lookup)
	if err != nil || config.DatabaseDSN != values[MediaCleanupDatabaseDSNEnv] ||
		config.StorageDir != values[StorageLocalDirEnv] ||
		config.TenantID.String() != values[TenantIDEnv] ||
		config.GenerationID.String() != values[GenerationIDEnv] {
		t.Fatalf("cleanup config = %+v, %v", config, err)
	}
	for _, key := range []string{
		MediaCleanupDatabaseDSNEnv, TenantIDEnv,
		GenerationIDEnv, StorageLocalDirEnv,
	} {
		without := key
		_, err := LoadMediaCleanupWorkerConfig(func(name string) (string, bool) {
			if name == without {
				return "", false
			}
			return lookup(name)
		})
		if err == nil {
			t.Fatalf("missing %s accepted", key)
		}
	}
}
