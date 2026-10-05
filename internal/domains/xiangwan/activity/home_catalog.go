package activity

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	BusinessTimezone        = "Asia/Shanghai"
	DefaultHomeLimit        = 20
	MaxHomeLimit            = 50
	MaxHomeQuickTags        = 5
	MaxHomeCursorAge        = 15 * time.Minute
	MaxHomeCursorFutureSkew = 5 * time.Second
	// MaxPublicParticipantAvatars caps the confirmed-registrant avatar stack
	// exposed on one home card; the batch read applies the same limit in SQL.
	MaxPublicParticipantAvatars = 3
	// MaxPublicFavoriteAvatars caps the current "want to go" avatar stack
	// exposed on one public card. The count remains the authoritative full
	// Series favorite count; only this small public preview is returned.
	MaxPublicFavoriteAvatars       = 3
	maxHomeHeatCount         int64 = 1<<63 - 1
)

type ActivityType string

const (
	ActivityTypeAll          ActivityType = "all"
	ActivityTypeAIRoundtable ActivityType = "ai_roundtable"
	ActivityTypeSpecialEvent ActivityType = "special_event"
	ActivityTypeCourse       ActivityType = "course"
	ActivityTypeCompetition  ActivityType = "competition"
	ActivityTypeCustom       ActivityType = "custom"
)

type AreaCode string

const (
	AreaCodeAll      AreaCode = "all"
	AreaCodeHeping   AreaCode = "heping"
	AreaCodeHexi     AreaCode = "hexi"
	AreaCodeHebei    AreaCode = "hebei"
	AreaCodeNankai   AreaCode = "nankai"
	AreaCodeHongqiao AreaCode = "hongqiao"
	AreaCodeHedong   AreaCode = "hedong"
	AreaCodeWuqing   AreaCode = "wuqing"
	AreaCodeDongli   AreaCode = "dongli"
	AreaCodeBinhai   AreaCode = "binhai"
	AreaCodeOnline   AreaCode = "online"
)

type HomeTimeWindow string

const (
	HomeTimeWindowAll       HomeTimeWindow = "all"
	HomeTimeWindowThisWeek  HomeTimeWindow = "this_week"
	HomeTimeWindowNextWeek  HomeTimeWindow = "next_week"
	HomeTimeWindowLocalDate HomeTimeWindow = "local_date"
)

type HomeFilter struct {
	ActivityType ActivityType
	Area         AreaCode
	TimeWindow   HomeTimeWindow
	LocalDate    string
	QuickTags    []string
	Cursor       string
	Limit        int
}

type HomeQuickTag struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// HomeSessionFacts are the trusted facts required by the read model. Adapters
// may load more rows than are public: the domain filters every draft,
// non-current Instance, archived Series, and hidden Session before projection.
type HomeSessionFacts struct {
	SeriesID                    uuid.UUID
	SeriesStatus                SeriesStatus
	SeriesIsRecurring           bool
	SeriesHomeVisible           bool
	CurrentPublicInstance       bool
	InstanceID                  uuid.UUID
	InstanceTitle               string
	InstanceStatus              InstanceStatus
	InstanceActivityType        ActivityType
	InstanceQuickTagCodes       []string
	InstanceCoverImageURL       string
	PublicationVersion          int64
	SessionID                   uuid.UUID
	SessionTitle                string
	SessionStatus               SessionStatus
	RegistrationStartAt         time.Time
	RegistrationEndAt           time.Time
	SessionStartAt              time.Time
	SessionEndAt                time.Time
	Capacity                    int
	ConfirmedCount              int
	ActiveHoldCount             int
	GroupMinimum                int
	LowStockThreshold           *int
	PriceCents                  int64
	DeliveryMode                DeliveryMode
	Area                        AreaCode
	VenueName                   string
	OnlineParticipationMode     string
	SortOrder                   int
	SeriesFavoriteCount         int64
	HistoricalRegistrationCount int64
}

type HomeCTAAction string

const HomeCTAActionSessionDetail HomeCTAAction = "session_detail"

type HomeCTALabel string

const (
	HomeCTALabelViewDetails       HomeCTALabel = "view_details"
	HomeCTALabelActivityEnded     HomeCTALabel = "activity_ended"
	HomeCTALabelActivityCancelled HomeCTALabel = "activity_cancelled"
)

type HomeCard struct {
	SeriesID                uuid.UUID
	InstanceID              uuid.UUID
	SessionID               uuid.UUID
	PublicationVersion      int64
	InstanceTitle           string
	SessionTitle            string
	ActivityType            ActivityType
	QuickTagCodes           []string
	CoverImageURL           string
	SessionStartAt          time.Time
	SessionEndAt            time.Time
	PriceCents              int64
	DeliveryMode            DeliveryMode
	Area                    AreaCode
	VenueName               string
	OnlineParticipationMode string
	Display                 SessionDisplayDecision
	HomeGroup               HomeGroup
	SortOrder               int
	HeatCount               int64
	CurrentFavoriteUsers    int64
	CTAAction               HomeCTAAction
	CTALabel                HomeCTALabel
	// ParticipantAvatars carries up to three public avatar URLs of confirmed
	// registrants. The read service fills it after BuildHomeCatalog returns
	// (one batch query per page), so it never feeds the catalog cursor or
	// revision fingerprint.
	ParticipantAvatars []string
	// FavoriteAvatars carries up to three public avatar URLs of users with a
	// current Series favorite (the product's "想去" relation). It is separate
	// from ParticipantAvatars: a favorite never implies a Registration.
	FavoriteAvatars []string
}

type HomeCatalog struct {
	Cards                        []HomeCard
	AvailableQuickTags           []HomeQuickTag
	ActiveFilter                 HomeFilter
	BusinessTimezone             string
	OpenRegistrationSessionCount int
	AsOf                         time.Time
	NextCursor                   string
}

var (
	ErrInvalidHomeFilter = errors.New("invalid xiangwan home filter")
	ErrInvalidHomeFacts  = errors.New("invalid xiangwan home facts")
	ErrInvalidHomeCursor = errors.New("invalid xiangwan home cursor")
	ErrStaleHomeCursor   = errors.New("stale xiangwan home cursor")
)

var homeTagCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// BuildHomeCatalog is side-effect free. It filters and projects one card per
// public Session, applies Canonical six-group ordering, and creates a stable
// cursor bound to filters, as-of time, and all matching publication facts.
func BuildHomeCatalog(
	facts []HomeSessionFacts,
	filter HomeFilter,
	availableQuickTags []HomeQuickTag,
	now time.Time,
) (HomeCatalog, error) {
	if now.IsZero() {
		return HomeCatalog{}, fmt.Errorf("%w: now is required", ErrInvalidHomeFilter)
	}
	normalized, availableCodes, err := normalizeHomeFilter(filter, availableQuickTags)
	if err != nil {
		return HomeCatalog{}, err
	}
	filterFingerprint, err := fingerprintHomeFilter(normalized)
	if err != nil {
		return HomeCatalog{}, err
	}

	asOf := now.UTC()
	var cursor decodedHomeCursor
	if normalized.Cursor != "" {
		cursor, err = decodeHomeCursor(normalized.Cursor)
		if err != nil {
			return HomeCatalog{}, err
		}
		if cursor.FilterFingerprint != filterFingerprint {
			return HomeCatalog{}, fmt.Errorf("%w: filter changed", ErrStaleHomeCursor)
		}
		asOf, err = time.Parse(time.RFC3339Nano, cursor.AsOf)
		if err != nil {
			return HomeCatalog{}, fmt.Errorf("%w: invalid as_of", ErrInvalidHomeCursor)
		}
		if asOf.After(now.UTC().Add(MaxHomeCursorFutureSkew)) ||
			now.UTC().Sub(asOf) > MaxHomeCursorAge {
			return HomeCatalog{}, fmt.Errorf("%w: as_of expired", ErrStaleHomeCursor)
		}
	}

	timeRange, err := resolveHomeTimeRange(normalized, asOf)
	if err != nil {
		return HomeCatalog{}, err
	}
	cards, err := projectHomeCards(facts, normalized, availableCodes, timeRange, asOf)
	if err != nil {
		return HomeCatalog{}, err
	}
	sort.Slice(cards, func(left, right int) bool {
		return homeCardLess(cards[left], cards[right])
	})

	revision, err := homeCatalogRevision(cards, availableQuickTags)
	if err != nil {
		return HomeCatalog{}, err
	}
	start := 0
	if normalized.Cursor != "" {
		if cursor.CatalogRevision != revision {
			return HomeCatalog{}, fmt.Errorf("%w: publication facts changed", ErrStaleHomeCursor)
		}
		start = homeCursorStart(cards, cursor)
		if start < 0 {
			return HomeCatalog{}, fmt.Errorf("%w: position no longer exists", ErrStaleHomeCursor)
		}
	}

	openCount := 0
	for _, card := range cards {
		if card.Display.State.RegistrationAllowed() {
			openCount++
		}
	}

	end := start + normalized.Limit
	if end > len(cards) {
		end = len(cards)
	}
	page := append([]HomeCard(nil), cards[start:end]...)
	nextCursor := ""
	if end < len(cards) && len(page) > 0 {
		nextCursor, err = encodeHomeCursor(
			filterFingerprint,
			revision,
			asOf,
			page[len(page)-1],
		)
		if err != nil {
			return HomeCatalog{}, err
		}
	}
	normalized.Cursor = ""

	return HomeCatalog{
		Cards:                        page,
		AvailableQuickTags:           append([]HomeQuickTag(nil), availableQuickTags...),
		ActiveFilter:                 normalized,
		BusinessTimezone:             BusinessTimezone,
		OpenRegistrationSessionCount: openCount,
		AsOf:                         asOf,
		NextCursor:                   nextCursor,
	}, nil
}

func normalizeHomeFilter(
	filter HomeFilter,
	availableQuickTags []HomeQuickTag,
) (HomeFilter, map[string]struct{}, error) {
	if filter.ActivityType == "" {
		filter.ActivityType = ActivityTypeAll
	}
	if !knownActivityType(filter.ActivityType, true) {
		return HomeFilter{}, nil, fmt.Errorf("%w: unknown activity_type", ErrInvalidHomeFilter)
	}
	if filter.Area == "" {
		filter.Area = AreaCodeAll
	}
	if !knownAreaCode(filter.Area, true) {
		return HomeFilter{}, nil, fmt.Errorf("%w: unknown area", ErrInvalidHomeFilter)
	}
	if filter.TimeWindow == "" {
		filter.TimeWindow = HomeTimeWindowAll
	}
	if !knownHomeTimeWindow(filter.TimeWindow) {
		return HomeFilter{}, nil, fmt.Errorf("%w: unknown time_window", ErrInvalidHomeFilter)
	}
	if filter.TimeWindow == HomeTimeWindowLocalDate {
		if _, err := time.Parse("2006-01-02", filter.LocalDate); err != nil {
			return HomeFilter{}, nil, fmt.Errorf("%w: local_date must be YYYY-MM-DD", ErrInvalidHomeFilter)
		}
	} else if filter.LocalDate != "" {
		return HomeFilter{}, nil, fmt.Errorf(
			"%w: local_date requires time_window=local_date",
			ErrInvalidHomeFilter,
		)
	}
	switch {
	case filter.Limit == 0:
		filter.Limit = DefaultHomeLimit
	case filter.Limit < 1 || filter.Limit > MaxHomeLimit:
		return HomeFilter{}, nil, fmt.Errorf("%w: limit must be 1..50", ErrInvalidHomeFilter)
	}
	if len(filter.QuickTags) > MaxHomeQuickTags {
		return HomeFilter{}, nil, fmt.Errorf("%w: at most 5 quick_tag values", ErrInvalidHomeFilter)
	}

	availableCodes := make(map[string]struct{}, len(availableQuickTags))
	for _, tag := range availableQuickTags {
		if !homeTagCodePattern.MatchString(tag.Code) || strings.TrimSpace(tag.Label) == "" {
			return HomeFilter{}, nil, fmt.Errorf("%w: malformed available quick tag", ErrInvalidHomeFacts)
		}
		if _, duplicate := availableCodes[tag.Code]; duplicate {
			return HomeFilter{}, nil, fmt.Errorf("%w: duplicate available quick tag", ErrInvalidHomeFacts)
		}
		availableCodes[tag.Code] = struct{}{}
	}

	selectedQuickTags := append([]string(nil), filter.QuickTags...)
	uniqueSelected := make(map[string]struct{}, len(selectedQuickTags))
	filter.QuickTags = make([]string, 0, len(selectedQuickTags))
	for _, code := range selectedQuickTags {
		if _, known := availableCodes[code]; !known {
			return HomeFilter{}, nil, fmt.Errorf("%w: unknown quick_tag", ErrInvalidHomeFilter)
		}
		if _, duplicate := uniqueSelected[code]; !duplicate {
			uniqueSelected[code] = struct{}{}
			filter.QuickTags = append(filter.QuickTags, code)
		}
	}
	sort.Strings(filter.QuickTags)
	return filter, availableCodes, nil
}

type homeTimeRange struct {
	start time.Time
	end   time.Time
	set   bool
}

func resolveHomeTimeRange(filter HomeFilter, asOf time.Time) (homeTimeRange, error) {
	if filter.TimeWindow == HomeTimeWindowAll {
		return homeTimeRange{}, nil
	}
	location, err := time.LoadLocation(BusinessTimezone)
	if err != nil {
		return homeTimeRange{}, fmt.Errorf("%w: load business timezone: %v", ErrInvalidHomeFacts, err)
	}

	localNow := asOf.In(location)
	var start time.Time
	switch filter.TimeWindow {
	case HomeTimeWindowThisWeek, HomeTimeWindowNextWeek:
		dayStart := time.Date(
			localNow.Year(),
			localNow.Month(),
			localNow.Day(),
			0,
			0,
			0,
			0,
			location,
		)
		daysSinceMonday := (int(dayStart.Weekday()) + 6) % 7
		start = dayStart.AddDate(0, 0, -daysSinceMonday)
		if filter.TimeWindow == HomeTimeWindowNextWeek {
			start = start.AddDate(0, 0, 7)
		}
	case HomeTimeWindowLocalDate:
		start, err = time.ParseInLocation("2006-01-02", filter.LocalDate, location)
		if err != nil {
			return homeTimeRange{}, fmt.Errorf("%w: local_date must be YYYY-MM-DD", ErrInvalidHomeFilter)
		}
	default:
		return homeTimeRange{}, fmt.Errorf("%w: unknown time_window", ErrInvalidHomeFilter)
	}

	days := 7
	if filter.TimeWindow == HomeTimeWindowLocalDate {
		days = 1
	}
	return homeTimeRange{
		start: start.UTC(),
		end:   start.AddDate(0, 0, days).UTC(),
		set:   true,
	}, nil
}

func projectHomeCards(
	facts []HomeSessionFacts,
	filter HomeFilter,
	availableCodes map[string]struct{},
	timeRange homeTimeRange,
	asOf time.Time,
) ([]HomeCard, error) {
	cards := make([]HomeCard, 0, len(facts))
	seenSessionIDs := make(map[uuid.UUID]struct{}, len(facts))
	selectedTags := make(map[string]struct{}, len(filter.QuickTags))
	for _, code := range filter.QuickTags {
		selectedTags[code] = struct{}{}
	}

	for _, fact := range facts {
		if !homeFactIsPublic(fact) {
			continue
		}
		if _, duplicate := seenSessionIDs[fact.SessionID]; duplicate {
			return nil, fmt.Errorf("%w: duplicate public Session", ErrInvalidHomeFacts)
		}
		seenSessionIDs[fact.SessionID] = struct{}{}
		if err := validatePublicHomeFact(fact, availableCodes); err != nil {
			return nil, err
		}
		if filter.ActivityType != ActivityTypeAll &&
			fact.InstanceActivityType != filter.ActivityType {
			continue
		}
		if filter.Area != AreaCodeAll && fact.Area != filter.Area {
			continue
		}
		if timeRange.set &&
			(fact.SessionStartAt.Before(timeRange.start) || !fact.SessionStartAt.Before(timeRange.end)) {
			continue
		}
		if len(selectedTags) > 0 && !homeFactMatchesAnyTag(fact.InstanceQuickTagCodes, selectedTags) {
			continue
		}

		display, err := DecideSessionDisplay(SessionDisplayFacts{
			InstanceCancelled:          fact.InstanceStatus == InstanceStatusCancelled,
			SessionCancelled:           fact.SessionStatus == SessionStatusCancelled,
			SeriesInRecurringGap:       fact.SeriesIsRecurring && fact.InstanceStatus == InstanceStatusCompleted,
			Now:                        asOf,
			RegistrationStartAt:        fact.RegistrationStartAt.UTC(),
			RegistrationEndAt:          fact.RegistrationEndAt.UTC(),
			SessionStartAt:             fact.SessionStartAt.UTC(),
			SessionEndAt:               fact.SessionEndAt.UTC(),
			Capacity:                   fact.Capacity,
			ConfirmedRegistrationCount: fact.ConfirmedCount,
			ActiveHoldCount:            fact.ActiveHoldCount,
			GroupMinimum:               fact.GroupMinimum,
			LowStockThreshold:          fact.LowStockThreshold,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: Session display: %v", ErrInvalidHomeFacts, err)
		}
		group, known := display.State.HomeGroup()
		if !known {
			return nil, fmt.Errorf("%w: unknown display state", ErrInvalidHomeFacts)
		}
		ctaLabel := HomeCTALabelViewDetails
		switch display.State {
		case DisplayStateEnded:
			ctaLabel = HomeCTALabelActivityEnded
		case DisplayStateCancelled:
			ctaLabel = HomeCTALabelActivityCancelled
		}
		cards = append(cards, HomeCard{
			SeriesID:                fact.SeriesID,
			InstanceID:              fact.InstanceID,
			SessionID:               fact.SessionID,
			PublicationVersion:      fact.PublicationVersion,
			InstanceTitle:           fact.InstanceTitle,
			SessionTitle:            fact.SessionTitle,
			ActivityType:            fact.InstanceActivityType,
			QuickTagCodes:           append([]string(nil), fact.InstanceQuickTagCodes...),
			CoverImageURL:           fact.InstanceCoverImageURL,
			SessionStartAt:          fact.SessionStartAt.UTC(),
			SessionEndAt:            fact.SessionEndAt.UTC(),
			PriceCents:              fact.PriceCents,
			DeliveryMode:            fact.DeliveryMode,
			Area:                    fact.Area,
			VenueName:               fact.VenueName,
			OnlineParticipationMode: fact.OnlineParticipationMode,
			Display:                 display,
			HomeGroup:               group,
			SortOrder:               fact.SortOrder,
			HeatCount:               fact.SeriesFavoriteCount + fact.HistoricalRegistrationCount,
			CurrentFavoriteUsers:    fact.SeriesFavoriteCount,
			CTAAction:               HomeCTAActionSessionDetail,
			CTALabel:                ctaLabel,
		})
	}
	return cards, nil
}

func homeFactIsPublic(fact HomeSessionFacts) bool {
	if !fact.SeriesHomeVisible ||
		fact.SeriesStatus != SeriesStatusActive ||
		!fact.CurrentPublicInstance {
		return false
	}
	switch fact.InstanceStatus {
	case InstanceStatusPublished, InstanceStatusCompleted, InstanceStatusCancelled:
	default:
		return false
	}
	switch fact.SessionStatus {
	case SessionStatusPublished, SessionStatusEnded, SessionStatusCancelled:
		return true
	default:
		return false
	}
}

func validatePublicHomeFact(
	fact HomeSessionFacts,
	availableCodes map[string]struct{},
) error {
	switch {
	case fact.SeriesID == uuid.Nil:
		return fmt.Errorf("%w: series_id is required", ErrInvalidHomeFacts)
	case fact.InstanceID == uuid.Nil:
		return fmt.Errorf("%w: instance_id is required", ErrInvalidHomeFacts)
	case fact.SessionID == uuid.Nil:
		return fmt.Errorf("%w: session_id is required", ErrInvalidHomeFacts)
	case strings.TrimSpace(fact.InstanceTitle) == "":
		return fmt.Errorf("%w: Instance title is blank", ErrInvalidHomeFacts)
	case strings.TrimSpace(fact.SessionTitle) == "":
		return fmt.Errorf("%w: Session title is blank", ErrInvalidHomeFacts)
	case fact.PublicationVersion < 1:
		return fmt.Errorf("%w: publication version is invalid", ErrInvalidHomeFacts)
	case !knownActivityType(fact.InstanceActivityType, false):
		return fmt.Errorf("%w: activity type is invalid", ErrInvalidHomeFacts)
	case fact.PriceCents < 0:
		return fmt.Errorf("%w: price is negative", ErrInvalidHomeFacts)
	case fact.SeriesFavoriteCount < 0 || fact.HistoricalRegistrationCount < 0:
		return fmt.Errorf("%w: heat count is negative", ErrInvalidHomeFacts)
	case fact.SeriesFavoriteCount > maxHomeHeatCount-fact.HistoricalRegistrationCount:
		return fmt.Errorf("%w: heat count overflows", ErrInvalidHomeFacts)
	}
	switch fact.DeliveryMode {
	case DeliveryModeOffline:
		if !knownAreaCode(fact.Area, false) || fact.Area == AreaCodeOnline {
			return fmt.Errorf("%w: offline area is invalid", ErrInvalidHomeFacts)
		}
		if strings.TrimSpace(fact.VenueName) == "" {
			return fmt.Errorf("%w: offline venue is blank", ErrInvalidHomeFacts)
		}
	case DeliveryModeOnline:
		if fact.Area != AreaCodeOnline || strings.TrimSpace(fact.OnlineParticipationMode) == "" {
			return fmt.Errorf("%w: online delivery facts are invalid", ErrInvalidHomeFacts)
		}
	default:
		return fmt.Errorf("%w: delivery mode is invalid", ErrInvalidHomeFacts)
	}
	seenTagCodes := make(map[string]struct{}, len(fact.InstanceQuickTagCodes))
	for _, code := range fact.InstanceQuickTagCodes {
		if _, known := availableCodes[code]; !known {
			// A BrandProfile publication can legitimately advance after a
			// previously published Instance was pinned. Admin writes reject new
			// unknown codes, but a legacy/stale code must not take the whole
			// public home read down. Keep it in the card as an opaque code; the
			// detail projection already has the same stable-code fallback.
			continue
		}
		if _, duplicate := seenTagCodes[code]; duplicate {
			return fmt.Errorf("%w: Session exposes a duplicate quick tag", ErrInvalidHomeFacts)
		}
		seenTagCodes[code] = struct{}{}
	}
	return nil
}

func homeFactMatchesAnyTag(codes []string, selected map[string]struct{}) bool {
	for _, code := range codes {
		if _, match := selected[code]; match {
			return true
		}
	}
	return false
}

func homeCardLess(left HomeCard, right HomeCard) bool {
	if left.HomeGroup != right.HomeGroup {
		return left.HomeGroup < right.HomeGroup
	}
	leftTime := homeCardSortTime(left)
	rightTime := homeCardSortTime(right)
	if !leftTime.Equal(rightTime) {
		if left.HomeGroup == HomeGroupEnded {
			return leftTime.After(rightTime)
		}
		return leftTime.Before(rightTime)
	}
	if left.SortOrder != right.SortOrder {
		return left.SortOrder < right.SortOrder
	}
	return left.SessionID.String() < right.SessionID.String()
}

func homeCardSortTime(card HomeCard) time.Time {
	if card.HomeGroup == HomeGroupEnded {
		return card.SessionEndAt
	}
	return card.SessionStartAt
}

func knownActivityType(value ActivityType, allowAll bool) bool {
	switch value {
	case ActivityTypeAIRoundtable,
		ActivityTypeSpecialEvent,
		ActivityTypeCourse,
		ActivityTypeCompetition,
		ActivityTypeCustom:
		return true
	case ActivityTypeAll:
		return allowAll
	default:
		return false
	}
}

func knownAreaCode(value AreaCode, allowAll bool) bool {
	switch value {
	case AreaCodeHeping,
		AreaCodeHexi,
		AreaCodeHebei,
		AreaCodeNankai,
		AreaCodeHongqiao,
		AreaCodeHedong,
		AreaCodeWuqing,
		AreaCodeDongli,
		AreaCodeBinhai,
		AreaCodeOnline:
		return true
	case AreaCodeAll:
		return allowAll
	default:
		return false
	}
}

func knownHomeTimeWindow(value HomeTimeWindow) bool {
	switch value {
	case HomeTimeWindowAll,
		HomeTimeWindowThisWeek,
		HomeTimeWindowNextWeek,
		HomeTimeWindowLocalDate:
		return true
	default:
		return false
	}
}

func fingerprintHomeFilter(filter HomeFilter) (string, error) {
	encoded, err := json.Marshal(struct {
		ActivityType ActivityType   `json:"activity_type"`
		Area         AreaCode       `json:"area"`
		TimeWindow   HomeTimeWindow `json:"time_window"`
		LocalDate    string         `json:"local_date"`
		QuickTags    []string       `json:"quick_tags"`
	}{
		ActivityType: filter.ActivityType,
		Area:         filter.Area,
		TimeWindow:   filter.TimeWindow,
		LocalDate:    filter.LocalDate,
		QuickTags:    filter.QuickTags,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode filter", ErrInvalidHomeFilter)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func homeCatalogRevision(cards []HomeCard, quickTags []HomeQuickTag) (string, error) {
	type revisionCard struct {
		SessionID          string       `json:"session_id"`
		PublicationVersion int64        `json:"publication_version"`
		State              DisplayState `json:"state"`
		Group              HomeGroup    `json:"group"`
		SortAt             string       `json:"sort_at"`
		SortOrder          int          `json:"sort_order"`
		ConfirmedCount     int          `json:"confirmed_count"`
		SellableCapacity   int          `json:"sellable_capacity"`
		HeatCount          int64        `json:"heat_count"`
	}
	revisionCards := make([]revisionCard, 0, len(cards))
	for _, card := range cards {
		revisionCards = append(revisionCards, revisionCard{
			SessionID:          card.SessionID.String(),
			PublicationVersion: card.PublicationVersion,
			State:              card.Display.State,
			Group:              card.HomeGroup,
			SortAt:             homeCardSortTime(card).UTC().Format(time.RFC3339Nano),
			SortOrder:          card.SortOrder,
			ConfirmedCount:     card.Display.ConfirmedRegistrationCount,
			SellableCapacity:   card.Display.SellableCapacity,
			HeatCount:          card.HeatCount,
		})
	}
	sort.Slice(revisionCards, func(left, right int) bool {
		return revisionCards[left].SessionID < revisionCards[right].SessionID
	})
	encoded, err := json.Marshal(struct {
		Cards     []revisionCard `json:"cards"`
		QuickTags []HomeQuickTag `json:"quick_tags"`
	}{
		Cards:     revisionCards,
		QuickTags: quickTags,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode catalog revision", ErrInvalidHomeFacts)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

type decodedHomeCursor struct {
	Version            int       `json:"v"`
	FilterFingerprint  string    `json:"filter"`
	CatalogRevision    string    `json:"revision"`
	AsOf               string    `json:"as_of"`
	Group              HomeGroup `json:"group"`
	SortAt             string    `json:"sort_at"`
	SortOrder          int       `json:"sort_order"`
	SessionID          string    `json:"session_id"`
	PublicationVersion int64     `json:"publication_version"`
}

func encodeHomeCursor(
	filterFingerprint string,
	catalogRevision string,
	asOf time.Time,
	card HomeCard,
) (string, error) {
	encoded, err := json.Marshal(decodedHomeCursor{
		Version:            1,
		FilterFingerprint:  filterFingerprint,
		CatalogRevision:    catalogRevision,
		AsOf:               asOf.UTC().Format(time.RFC3339Nano),
		Group:              card.HomeGroup,
		SortAt:             homeCardSortTime(card).UTC().Format(time.RFC3339Nano),
		SortOrder:          card.SortOrder,
		SessionID:          card.SessionID.String(),
		PublicationVersion: card.PublicationVersion,
	})
	if err != nil {
		return "", fmt.Errorf("%w: encode", ErrInvalidHomeCursor)
	}
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeHomeCursor(value string) (decodedHomeCursor, error) {
	if len(value) > 2048 {
		return decodedHomeCursor{}, fmt.Errorf("%w: payload is too large", ErrInvalidHomeCursor)
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return decodedHomeCursor{}, fmt.Errorf("%w: malformed base64", ErrInvalidHomeCursor)
	}
	var cursor decodedHomeCursor
	if err := json.Unmarshal(encoded, &cursor); err != nil {
		return decodedHomeCursor{}, fmt.Errorf("%w: malformed payload", ErrInvalidHomeCursor)
	}
	if cursor.Version != 1 ||
		len(cursor.FilterFingerprint) != 64 ||
		len(cursor.CatalogRevision) != 64 ||
		cursor.Group < HomeGroupOpen ||
		cursor.Group > HomeGroupCancelled ||
		cursor.SortAt == "" ||
		cursor.SessionID == "" ||
		cursor.PublicationVersion < 1 {
		return decodedHomeCursor{}, fmt.Errorf("%w: invalid fields", ErrInvalidHomeCursor)
	}
	if _, err := uuid.Parse(cursor.SessionID); err != nil {
		return decodedHomeCursor{}, fmt.Errorf("%w: invalid Session identity", ErrInvalidHomeCursor)
	}
	if _, err := time.Parse(time.RFC3339Nano, cursor.SortAt); err != nil {
		return decodedHomeCursor{}, fmt.Errorf("%w: invalid sort time", ErrInvalidHomeCursor)
	}
	return cursor, nil
}

func homeCursorStart(cards []HomeCard, cursor decodedHomeCursor) int {
	for index, card := range cards {
		if card.HomeGroup == cursor.Group &&
			homeCardSortTime(card).UTC().Format(time.RFC3339Nano) == cursor.SortAt &&
			card.SortOrder == cursor.SortOrder &&
			card.SessionID.String() == cursor.SessionID &&
			card.PublicationVersion == cursor.PublicationVersion {
			return index + 1
		}
	}
	return -1
}
