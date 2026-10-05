package xiangwanruntime

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPublicMediaStreamClearsServerWriteDeadline(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		method    string
		path      string
		wantClear bool
	}{
		{name: "get media", method: http.MethodGet, path: publicMediaPathPrefix + "relation/block/file", wantClear: true},
		{name: "head media", method: http.MethodHead, path: publicMediaPathPrefix + "relation/block/file", wantClear: true},
		{name: "post media", method: http.MethodPost, path: publicMediaPathPrefix + "relation/block/file"},
		{name: "missing identity", method: http.MethodGet, path: publicMediaPathPrefix + "relation/block"},
		{name: "extra identity", method: http.MethodGet, path: publicMediaPathPrefix + "relation/block/file/extra"},
		{name: "other route", method: http.MethodGet, path: "/api/v1/xiangwan/sessions"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			writer := &deadlineResponseWriter{header: make(http.Header)}
			nextCalls := 0
			handler := withoutPublicMediaWriteDeadline(http.HandlerFunc(
				func(http.ResponseWriter, *http.Request) { nextCalls++ },
			))
			handler.ServeHTTP(writer, httptest.NewRequest(test.method, test.path, nil))
			wantCalls := 0
			if test.wantClear {
				wantCalls = 1
			}
			if writer.deadlineCalls != wantCalls ||
				(test.wantClear && !writer.deadline.IsZero()) || nextCalls != 1 {
				t.Fatalf(
					"deadline calls=%d deadline=%v next calls=%d",
					writer.deadlineCalls,
					writer.deadline,
					nextCalls,
				)
			}
		})
	}
}

func TestPublicMediaStreamAllowsUnsupportedTestWriter(t *testing.T) {
	t.Parallel()

	nextCalls := 0
	handler := withoutPublicMediaWriteDeadline(http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { nextCalls++ },
	))
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(
			http.MethodGet,
			publicMediaPathPrefix+"relation/block/file",
			nil,
		),
	)
	if nextCalls != 1 {
		t.Fatalf("next calls=%d", nextCalls)
	}
}

type deadlineResponseWriter struct {
	header        http.Header
	deadline      time.Time
	deadlineCalls int
}

func (writer *deadlineResponseWriter) Header() http.Header {
	return writer.header
}

func (*deadlineResponseWriter) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (*deadlineResponseWriter) WriteHeader(int) {}

func (writer *deadlineResponseWriter) SetWriteDeadline(deadline time.Time) error {
	writer.deadline = deadline
	writer.deadlineCalls++
	return nil
}
