package activity

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSeriesFavoriteStateValidationAndRedaction(t *testing.T) {
	t.Parallel()

	state := SeriesFavoriteState{
		TenantID:      uuid.New(),
		PrincipalID:   uuid.New(),
		SeriesID:      uuid.New(),
		Favorited:     true,
		Changed:       true,
		FavoriteCount: 3,
		SeriesVersion: 4,
		OccurredAt:    time.Now().UTC(),
	}
	if err := ValidateSeriesFavoriteState(state); err != nil {
		t.Fatalf("ValidateSeriesFavoriteState() error = %v", err)
	}
	formatted := fmt.Sprintf("%+v", state)
	if strings.Contains(formatted, state.PrincipalID.String()) ||
		strings.Contains(formatted, state.TenantID.String()) {
		t.Fatalf("Series favorite formatting leaked owner identity: %s", formatted)
	}

	invalid := state
	invalid.FavoriteCount = -1
	if err := ValidateSeriesFavoriteState(invalid); err == nil {
		t.Fatal("negative favorite count accepted")
	}
}

func TestMyFavoriteItemValidation(t *testing.T) {
	t.Parallel()

	item := MyFavoriteItem{
		SeriesID:          uuid.New(),
		Title:             "AI Roundtable",
		SeriesStatus:      SeriesStatusActive,
		FavoriteCount:     8,
		FavoritedAt:       time.Now().UTC(),
		SessionsAvailable: true,
	}
	if err := ValidateMyFavoriteItem(item); err != nil {
		t.Fatalf("ValidateMyFavoriteItem() error = %v", err)
	}
	item.SeriesStatus = SeriesStatusArchived
	if err := ValidateMyFavoriteItem(item); err == nil {
		t.Fatal("archived Series accepted as Sessions-available")
	}
}
