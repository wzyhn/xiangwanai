package contentsecurity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wzyhn/xiangwanai/internal/pkg/contentsecurity"
	"github.com/wzyhn/xiangwanai/internal/pkg/errx"

	"github.com/google/uuid"
)

type recordingLegacyObserver struct {
	kinds []contentsecurity.LegacyFailOpenKind
}

func (o *recordingLegacyObserver) RecordLegacyFailOpen(_ context.Context, kind contentsecurity.LegacyFailOpenKind) {
	o.kinds = append(o.kinds, kind)
}

func TestEnforceUserTextLegacyObservesEveryFailOpenBranch(t *testing.T) {
	passChecker := &stubChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestPass}}
	resolved := func(uuid.UUID, string) (string, error) { return "openid-placeholder", nil }
	tests := []struct {
		name     string
		checker  contentsecurity.Checker
		appID    string
		resolver func(uuid.UUID, string) (string, error)
		text     string
		wantKind contentsecurity.LegacyFailOpenKind
		wantErr  bool
	}{
		{
			name: "nil checker", appID: "wx-app", resolver: resolved, text: "text",
			wantKind: contentsecurity.LegacyCheckerNotConfigured,
		},
		{
			name: "disabled checker", checker: contentsecurity.DisabledChecker{}, appID: "wx-app", resolver: resolved, text: "text",
			wantKind: contentsecurity.LegacyCheckerNotConfigured,
		},
		{
			name: "invalid app binding", checker: passChecker, resolver: resolved, text: "text",
			wantKind: contentsecurity.LegacyInvalidAppBinding,
		},
		{
			name: "resolver not configured", checker: passChecker, appID: "wx-app", text: "text",
			wantKind: contentsecurity.LegacyResolverNotConfigured,
		},
		{
			name: "identity not bound", checker: passChecker, appID: "wx-app", text: "text",
			resolver: func(uuid.UUID, string) (string, error) {
				return "", errx.NewForbidden("identity missing")
			},
			wantKind: contentsecurity.LegacyIdentityNotBound,
		},
		{
			name: "resolver rejected app binding", checker: passChecker, appID: "wx-app", text: "text",
			resolver: func(uuid.UUID, string) (string, error) {
				return "", errx.NewBadRequest("app mismatch")
			},
			wantKind: contentsecurity.LegacyInvalidAppBinding,
		},
		{
			name: "resolver unavailable", checker: passChecker, appID: "wx-app", text: "text",
			resolver: func(uuid.UUID, string) (string, error) {
				return "", errors.New("identity store unavailable")
			},
			wantKind: contentsecurity.LegacyResolverUnavailable,
		},
		{
			name: "empty openid", checker: passChecker, appID: "wx-app", text: "text",
			resolver: func(uuid.UUID, string) (string, error) { return " ", nil },
			wantKind: contentsecurity.LegacyIdentityNotBound,
		},
		{
			name: "checker transient", checker: &stubChecker{err: errors.New("provider unavailable")}, appID: "wx-app", resolver: resolved, text: "text",
			wantKind: contentsecurity.LegacyCheckerTransient,
		},
		{
			name: "review allowed", checker: &stubChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestReview}}, appID: "wx-app", resolver: resolved, text: "text",
			wantKind: contentsecurity.LegacyReviewAllowed,
		},
		{
			name: "invalid response allowed", checker: &stubChecker{result: contentsecurity.Result{Suggest: "made_up"}}, appID: "wx-app", resolver: resolved, text: "text",
			wantKind: contentsecurity.LegacyInvalidResponse,
		},
		{
			name: "pass", checker: passChecker, appID: "wx-app", resolver: resolved, text: "text",
		},
		{
			name: "risky rejects", checker: &stubChecker{result: contentsecurity.Result{Suggest: contentsecurity.SuggestRisky}}, appID: "wx-app", resolver: resolved, text: "text", wantErr: true,
		},
		{
			name: "empty text", appID: "wx-app", resolver: resolved, text: "   ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observer := &recordingLegacyObserver{}
			err := contentsecurity.EnforceUserTextLegacy(
				context.Background(), tc.checker, observer, tc.resolver,
				uuid.New(), "test.caller", tc.appID, tc.text, 2,
			)
			if tc.wantErr && err == nil {
				t.Fatal("expected rejection")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantKind == "" {
				if len(observer.kinds) != 0 {
					t.Fatalf("observed %v, want none", observer.kinds)
				}
				return
			}
			if len(observer.kinds) != 1 || observer.kinds[0] != tc.wantKind {
				t.Fatalf("observed %v, want [%s]", observer.kinds, tc.wantKind)
			}
		})
	}
}

func TestLegacyFailOpenKindValid(t *testing.T) {
	for _, kind := range []contentsecurity.LegacyFailOpenKind{
		contentsecurity.LegacyCheckerNotConfigured,
		contentsecurity.LegacyInvalidAppBinding,
		contentsecurity.LegacyResolverNotConfigured,
		contentsecurity.LegacyIdentityNotBound,
		contentsecurity.LegacyResolverUnavailable,
		contentsecurity.LegacyCheckerTransient,
		contentsecurity.LegacyReviewAllowed,
		contentsecurity.LegacyInvalidResponse,
	} {
		if !kind.Valid() {
			t.Fatalf("registered kind %q must be valid", kind)
		}
	}
	if contentsecurity.LegacyFailOpenKind("made_up").Valid() {
		t.Fatal("unregistered kind must be invalid")
	}
}
