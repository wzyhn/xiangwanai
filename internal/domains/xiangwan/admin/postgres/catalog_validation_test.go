package postgres

import (
	"database/sql"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wzyhn/xiangwanai/internal/domains/xiangwan/activity"
	xiangwanadmin "github.com/wzyhn/xiangwanai/internal/domains/xiangwan/admin"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCompletionSessionFactsTransitionsDuePublishedSessions(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	facts, dueCount := completionSessionFacts([]completionSessionRow{
		{Status: activity.SessionStatusPublished, SessionEndAt: sql.NullTime{Valid: true, Time: now.Add(-time.Minute)}},
		{Status: activity.SessionStatusCancelled, SessionEndAt: sql.NullTime{Valid: true, Time: now.Add(time.Hour)}},
	}, now)
	if want := []activity.SessionTerminalFact{
		activity.SessionTerminalFactEnded,
		activity.SessionTerminalFactCancelled,
	}; !reflect.DeepEqual(facts, want) {
		t.Fatalf("completionSessionFacts() facts = %v, want %v", facts, want)
	}
	if dueCount != 1 {
		t.Fatalf("completionSessionFacts() due count = %d, want 1", dueCount)
	}
	decision, err := activity.DecideInstanceClosure(facts)
	if err != nil || decision.Closure != activity.InstanceClosureCompleted {
		t.Fatalf("DecideInstanceClosure() = %+v, %v, want completed", decision, err)
	}
}

func TestCompletionSessionFactsKeepsFuturePublishedSessionNonTerminal(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 28, 10, 0, 0, 0, time.UTC)
	facts, dueCount := completionSessionFacts([]completionSessionRow{
		{Status: activity.SessionStatusPublished, SessionEndAt: sql.NullTime{Valid: true, Time: now.Add(-time.Minute)}},
		{Status: activity.SessionStatusPublished, SessionEndAt: sql.NullTime{Valid: true, Time: now.Add(time.Minute)}},
	}, now)
	want := []activity.SessionTerminalFact{
		activity.SessionTerminalFactEnded,
		activity.SessionTerminalFactNonTerminal,
	}
	if !reflect.DeepEqual(facts, want) {
		t.Fatalf("completionSessionFacts() facts = %v, want %v", facts, want)
	}
	if dueCount != 1 {
		t.Fatalf("completionSessionFacts() due count = %d, want 1", dueCount)
	}
	decision, err := activity.DecideInstanceClosure(facts)
	if err != nil {
		t.Fatalf("DecideInstanceClosure() error = %v", err)
	}
	if decision.Closure != activity.InstanceClosureNone {
		t.Fatalf("DecideInstanceClosure() closure = %q, want none", decision.Closure)
	}
}

func TestValidSessionLocationLengthsMatchesPersistedColumnLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		venueName  string
		onlineMode string
		want       bool
	}{
		{name: "exact venue limit", venueName: strings.Repeat("v", 200), want: true},
		{name: "venue too long", venueName: strings.Repeat("v", 201), want: false},
		{name: "exact online mode limit", onlineMode: strings.Repeat("o", 80), want: true},
		{name: "online mode too long", onlineMode: strings.Repeat("o", 81), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := validSessionLocationLengths(test.venueName, test.onlineMode); got != test.want {
				t.Fatalf("validSessionLocationLengths() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestSessionIntegersFitPostgres(t *testing.T) {
	t.Parallel()

	if !sessionIntegersFitPostgres(
		maxPostgresInteger,
		maxPostgresInteger,
		maxPostgresInteger,
		maxPostgresInteger,
	) {
		t.Fatal("PostgreSQL INTEGER maximum was rejected")
	}
	if strconv.IntSize == 64 {
		tooLarge := int64(maxPostgresInteger) + 1
		if sessionIntegersFitPostgres(int(tooLarge), 1, 1, 0) {
			t.Fatal("value above PostgreSQL INTEGER maximum was accepted")
		}
	}
}

// 空(含 nil)主题列表必须编码成 JSON 数组:json.Marshal 会把 nil slice 渲染
// 成 null,而 xiangwan_valid_brand_quick_tags 拒绝非数组——首次发布不带主题
// 就会以 SQLSTATE 23514 变成 500(2026-09-19 生产事故根因)。
func TestEncodeBrandQuickTagsKeepsEmptyListAnArray(t *testing.T) {
	t.Parallel()

	for name, tags := range map[string][]activity.HomeQuickTag{
		"nil slice":    nil,
		"empty slice":  {},
		"with one tag": {{Code: "ai_roundtable", Label: "AI 圆桌派"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			encoded, err := encodeBrandQuickTags(tags)
			if err != nil {
				t.Fatalf("encodeBrandQuickTags() error = %v", err)
			}
			if !strings.HasPrefix(string(encoded), "[") {
				t.Fatalf("encodeBrandQuickTags() = %s, want a JSON array", encoded)
			}
		})
	}
}

// 品牌档案只能收缩到不再被已发布期次引用的标签集合:删掉仍被引用的编码
// 会让 home 事实校验整体失败(codex review 2026-09-19, P1)。
func TestRemovedBrandTagCodes(t *testing.T) {
	t.Parallel()

	published := []activity.HomeQuickTag{
		{Code: "ai_roundtable", Label: "AI 圆桌派"},
		{Code: "maker", Label: "动手工坊"},
	}
	if removed := removedBrandTagCodes(published, published); len(removed) != 0 {
		t.Fatalf("identical list removed %v", removed)
	}
	if removed := removedBrandTagCodes(published, []activity.HomeQuickTag{
		{Code: "maker", Label: "动手工坊"},
	}); len(removed) != 1 || removed[0] != "ai_roundtable" {
		t.Fatalf("removed = %v, want [ai_roundtable]", removed)
	}
	if removed := removedBrandTagCodes(published, []activity.HomeQuickTag{
		{Code: "ai_roundtable", Label: "AI 圆桌派"},
		{Code: "maker", Label: "动手工坊"},
		{Code: "course", Label: "课程"},
	}); len(removed) != 0 {
		t.Fatalf("additive list removed %v", removed)
	}
	if removed := removedBrandTagCodes(nil, []activity.HomeQuickTag{
		{Code: "course", Label: "课程"},
	}); len(removed) != 0 {
		t.Fatalf("first publish removed %v", removed)
	}
}

func TestCoverFilenamesFromReferencesAreUniqueAndDeterministic(t *testing.T) {
	t.Parallel()

	got := coverFilenamesFromReferences(
		"/api/v1/xiangwan/covers/ffffffffffffffffffffffffffffffff.png",
		"https://admin.example/api/v1/xiangwan/covers/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg",
		"/api/v1/xiangwan/covers/ffffffffffffffffffffffffffffffff.png",
	)
	want := []string{
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg",
		"ffffffffffffffffffffffffffffffff.png",
	}
	if len(got) != len(want) {
		t.Fatalf("coverFilenamesFromReferences() = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("coverFilenamesFromReferences() = %v, want %v", got, want)
		}
	}
}

func TestClassifyBrandPublicationWriteErrorTranslatesCheckViolation(t *testing.T) {
	t.Parallel()

	classified := classifyBrandPublicationWriteError(&pgconn.PgError{
		Code:           "23514",
		ConstraintName: "xw_brand_publication_quick_tags_check",
	})
	if !errors.Is(classified, xiangwanadmin.ErrInvalidCatalogRequest) {
		t.Fatalf("classifyBrandPublicationWriteError(23514) = %v, want ErrInvalidCatalogRequest", classified)
	}

	untouched := errors.New("connection refused")
	if got := classifyBrandPublicationWriteError(untouched); !errors.Is(got, untouched) {
		t.Fatalf("classifyBrandPublicationWriteError(other) = %v, want the original error", got)
	}
}
