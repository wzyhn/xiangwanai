package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	activitypostgres "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity/postgres"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/google/uuid"
)

// UpdateSeries changes the durable, long-lived Series fields that are safe to
// edit independently of an Instance snapshot.  In particular, homepage
// exposure is a Series fact, while an Instance's generated title remains
// immutable after creation so historical periods cannot be renamed silently.
func (catalog *Catalog) UpdateSeries(
	ctx context.Context,
	command xiangwanadmin.UpdateSeriesCommand,
) (result activity.Series, resultErr error) {
	defer func() {
		resultErr = catalog.auditRejectedCommand(
			ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
			"series.update", "series", command.SeriesID, command.RequestID, resultErr,
		)
	}()

	var title string
	if command.Title != nil {
		title = strings.TrimSpace(*command.Title)
	}
	if !catalog.valid(ctx) || !validWriteIdentity(
		command.ActorID, command.IdentityLinkID, command.OperationID, command.RequestID,
	) || command.SeriesID == uuid.Nil || command.ExpectedVersion < 1 ||
		(command.Title == nil && command.HomeVisible == nil) ||
		(command.Title != nil && (utf8.RuneCountInString(title) < 1 ||
			utf8.RuneCountInString(title) > 200)) {
		return activity.Series{}, xiangwanadmin.ErrInvalidCatalogRequest
	}

	digest, err := commandDigest(struct {
		SeriesID        uuid.UUID `json:"series_id"`
		ExpectedVersion int64     `json:"expected_version"`
		Title           *string   `json:"title"`
		HomeVisible     *bool     `json:"home_visible"`
	}{
		SeriesID: command.SeriesID, ExpectedVersion: command.ExpectedVersion,
		Title: command.Title, HomeVisible: command.HomeVisible,
	})
	if err != nil {
		return activity.Series{}, err
	}

	tx, err := catalog.beginActivityWrite(
		ctx, command.ActorID, command.IdentityLinkID, command.OperationID,
	)
	if err != nil {
		return activity.Series{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if receipt, replay, readErr := readOperation[operationResult[activity.Series]](
		ctx, tx, catalog.tenantID, command.ActorID, command.OperationID,
		"series.update", digest,
	); readErr != nil {
		return activity.Series{}, readErr
	} else if replay {
		if receipt.Value.ID == uuid.Nil || receipt.Value.TenantID != catalog.tenantID ||
			receipt.Value.Version < 1 {
			return activity.Series{}, xiangwanadmin.ErrOperationConflict
		}
		if err := tx.Commit(); err != nil {
			return activity.Series{}, fmt.Errorf("commit admin Series update replay: %w", err)
		}
		return receipt.Value, nil
	}

	var currentTitle string
	var currentStatus activity.SeriesStatus
	var currentHomeVisible bool
	var currentVersion int64
	err = tx.QueryRowContext(ctx, `
SELECT title, status, home_visible, version
FROM xiangwan_activity_series
WHERE tenant_id = $1 AND id = $2
FOR UPDATE
`, catalog.tenantID, command.SeriesID).Scan(
		&currentTitle, &currentStatus, &currentHomeVisible, &currentVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return activity.Series{}, xiangwanadmin.ErrTargetNotFound
	}
	if err != nil {
		return activity.Series{}, fmt.Errorf("lock admin Series for update: %w", err)
	}
	if currentVersion != command.ExpectedVersion {
		return activity.Series{}, xiangwanadmin.ErrVersionConflict
	}
	if command.Title == nil {
		title = currentTitle
	}
	homeVisible := currentHomeVisible
	if command.HomeVisible != nil {
		homeVisible = *command.HomeVisible
	}
	// Archived Series have no public home projection.  Keeping this explicit
	// prevents a future query from accidentally reviving an archived series by
	// only changing the visibility flag.
	if currentStatus == activity.SeriesStatusArchived && homeVisible {
		return activity.Series{}, xiangwanadmin.ErrInvalidCatalogRequest
	}
	titleChanged := title != currentTitle
	homeVisibleChanged := homeVisible != currentHomeVisible
	if titleChanged || homeVisibleChanged {
		updated, updateErr := tx.ExecContext(ctx, `
UPDATE xiangwan_activity_series
SET title = $3,
    home_visible = $4,
    version = version + 1,
    updated_at = $5
WHERE tenant_id = $1 AND id = $2 AND version = $6
`, catalog.tenantID, command.SeriesID, title, homeVisible, catalog.now().UTC(), command.ExpectedVersion)
		if updateErr != nil {
			return activity.Series{}, fmt.Errorf("update admin Series: %w", updateErr)
		}
		rows, rowsErr := updated.RowsAffected()
		if rowsErr != nil {
			return activity.Series{}, fmt.Errorf("read updated admin Series count: %w", rowsErr)
		}
		if rows != 1 {
			return activity.Series{}, xiangwanadmin.ErrVersionConflict
		}
	}

	result, err = activitypostgres.NewRepository(tx).GetSeries(
		ctx, catalog.tenantID, command.SeriesID,
	)
	if err != nil {
		return activity.Series{}, err
	}
	if err := writeOperationAndAudit(
		ctx, tx, catalog.tenantID, command.ActorID, command.IdentityLinkID,
		command.OperationID, "series.update", digest,
		operationResult[activity.Series]{Value: result}, result.ID, result.Version,
		command.RequestID, catalog.now().UTC(),
	); err != nil {
		return activity.Series{}, err
	}
	if err := tx.Commit(); err != nil {
		return activity.Series{}, fmt.Errorf("commit admin Series update: %w", err)
	}
	return result, nil
}
