package logx

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/wzyhn/xiangwanai/internal/middleware"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// TestGinAccessLogger_EmitsAccessLine_WithRequestID is the integration smoke
// test for the X3.A scaffold: a gin engine wired with RequestID + GinAccessLogger
// must emit exactly one structured access line per request, and that line must
// carry the same request_id that gets echoed back to the client.
func TestGinAccessLogger_EmitsAccessLine_WithRequestID(t *testing.T) {
	root, logs := newObservedLogger()
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(GinAccessLogger(root))
	r.GET("/echo", func(c *gin.Context) {
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/echo?x=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	respRID := w.Header().Get(middleware.RequestIDKey)
	if respRID == "" {
		t.Fatal("response missing X-Request-ID header")
	}

	entries := logs.AllUntimed()
	if len(entries) != 1 {
		t.Fatalf("expected 1 access log entry, got %d", len(entries))
	}
	if entries[0].Message != "http_access" {
		t.Errorf("unexpected access log message: %q", entries[0].Message)
	}

	wantFields := map[string]bool{
		"method":      false,
		"path":        false,
		"status":      false,
		"duration_ms": false,
		"request_id":  false,
		"client_ip":   false,
	}
	var loggedRID, loggedPath, loggedMethod string
	var loggedStatus int64
	for _, f := range entries[0].Context {
		if _, ok := wantFields[f.Key]; ok {
			wantFields[f.Key] = true
		}
		switch f.Key {
		case "request_id":
			loggedRID = f.String
		case "path":
			loggedPath = f.String
		case "method":
			loggedMethod = f.String
		case "status":
			loggedStatus = f.Integer
		}
	}
	for k, ok := range wantFields {
		if !ok {
			t.Errorf("access log missing field %q", k)
		}
	}
	if loggedRID != respRID {
		t.Errorf("logged request_id %q != response header %q", loggedRID, respRID)
	}
	if loggedMethod != "GET" {
		t.Errorf("logged method %q != GET", loggedMethod)
	}
	if loggedPath != "/echo?x=1" {
		t.Errorf("logged path %q != /echo?x=1 (raw query must be included)", loggedPath)
	}
	if loggedStatus != 200 {
		t.Errorf("logged status %d != 200", loggedStatus)
	}
}

func TestGinAccessLogger_RedactsSignedAndTokenQueryCredentials(t *testing.T) {
	root, logs := newObservedLogger()
	r := gin.New()
	r.Use(GinAccessLogger(root))
	r.GET("/file", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	req := httptest.NewRequest(http.MethodGet, "/file?exp=1800000000&sig=credential-value&access_token=token-value&msg_signature=message-signature&X-Amz-Credential=cloud-credential&q-signature=provider-signature&content_id=visible-id", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	entries := logs.AllUntimed()
	if len(entries) != 1 {
		t.Fatalf("expected one access entry, got %d", len(entries))
	}
	var loggedPath string
	for _, field := range entries[0].Context {
		if field.Key == "path" {
			loggedPath = field.String
			break
		}
	}
	if strings.Contains(loggedPath, "credential-value") || strings.Contains(loggedPath, "token-value") || strings.Contains(loggedPath, "message-signature") || strings.Contains(loggedPath, "cloud-credential") || strings.Contains(loggedPath, "provider-signature") {
		t.Fatalf("access log leaked bearer query credentials: %q", loggedPath)
	}
	if !strings.Contains(loggedPath, "sig=%5BREDACTED%5D") || !strings.Contains(loggedPath, "access_token=%5BREDACTED%5D") || !strings.Contains(loggedPath, "msg_signature=%5BREDACTED%5D") || !strings.Contains(loggedPath, "X-Amz-Credential=%5BREDACTED%5D") || !strings.Contains(loggedPath, "q-signature=%5BREDACTED%5D") {
		t.Fatalf("access log did not preserve explicit redaction markers: %q", loggedPath)
	}
	if !strings.Contains(loggedPath, "content_id=visible-id") || !strings.Contains(loggedPath, "exp=1800000000") {
		t.Fatalf("access log lost non-secret diagnostics: %q", loggedPath)
	}
}

// TestGinAccessLogger_PreservesInboundRequestID verifies that an inbound
// X-Request-ID header is reused (not overwritten) and surfaced in the access
// log — the contract relied on by upstream tracers / load balancers.
func TestGinAccessLogger_PreservesInboundRequestID(t *testing.T) {
	root, logs := newObservedLogger()
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(GinAccessLogger(root))
	r.GET("/p", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	const inbound = "rid-from-upstream-9"
	req := httptest.NewRequest(http.MethodGet, "/p", nil)
	req.Header.Set(middleware.RequestIDKey, inbound)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get(middleware.RequestIDKey); got != inbound {
		t.Errorf("response RID = %q, want %q (must echo inbound)", got, inbound)
	}
	entries := logs.AllUntimed()
	if len(entries) != 1 {
		t.Fatalf("expected 1 access entry, got %d", len(entries))
	}
	for _, f := range entries[0].Context {
		if f.Key == "request_id" {
			if f.String != inbound {
				t.Errorf("logged RID = %q, want %q", f.String, inbound)
			}
			return
		}
	}
	t.Error("logged entry missing request_id field")
}

// TestGinAccessLogger_NilRoot_FallsBackToNop ensures that a misconfigured
// caller passing nil does not crash the request path; the access line
// silently drops, but the handler still runs.
func TestGinAccessLogger_NilRoot_FallsBackToNop(t *testing.T) {
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(GinAccessLogger(nil))
	r.GET("/h", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/h", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("nil root caused handler to fail: status %d", w.Code)
	}
}

// TestGinAccessLogger_StashesLoggerOnRequestContext verifies the contract
// that handlers can call logx.FromContext(c.Request.Context()) and get a
// request-bound logger without reaching into gin.Context themselves —
// this is the path PR #97 (4 high-frequency handlers) will rely on.
func TestGinAccessLogger_StashesLoggerOnRequestContext(t *testing.T) {
	root, logs := newObservedLogger()
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(GinAccessLogger(root))
	r.GET("/inner", func(c *gin.Context) {
		FromContext(c.Request.Context()).Info("handler-emitted")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/inner", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}

	respRID := w.Header().Get(middleware.RequestIDKey)
	entries := logs.AllUntimed()
	// 1 handler entry + 1 access entry = 2
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (handler + access), got %d", len(entries))
	}

	var sawHandlerRID bool
	for _, e := range entries {
		if e.Message != "handler-emitted" {
			continue
		}
		for _, f := range e.Context {
			if f.Key == "request_id" {
				if f.String != respRID {
					t.Errorf("handler-emitted RID %q != response RID %q", f.String, respRID)
				}
				sawHandlerRID = true
			}
		}
	}
	if !sawHandlerRID {
		t.Error("handler-emitted log line missing request_id field — FromContext binding broken")
	}
}

// TestGinAccessLogger_SurfacesGinContextErrors:c.Error() 挂上的错误必须以
// errors 字段落 access 行(response.internalErr 的 fail-closed 500 回退依赖此
// 通路——真错误不出客户端响应体,唯一落点在这里,按 request_id 可查);无错误
// 请求不得出现该字段(零噪音)。
func TestGinAccessLogger_SurfacesGinContextErrors(t *testing.T) {
	root, logs := newObservedLogger()
	r := gin.New()
	r.Use(middleware.RequestID())
	r.Use(GinAccessLogger(root))
	r.GET("/boom", func(c *gin.Context) {
		_ = c.Error(errors.New("dial tcp 10.9.9.9:5432: refused"))
		c.Status(http.StatusInternalServerError)
	})
	r.GET("/fine", func(c *gin.Context) { c.Status(http.StatusOK) })

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/boom", nil))
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, httptest.NewRequest(http.MethodGet, "/fine", nil))

	entries := logs.AllUntimed()
	if len(entries) != 2 {
		t.Fatalf("expected 2 access entries, got %d", len(entries))
	}

	var boomHasErrors, fineHasErrors bool
	for _, e := range entries {
		var path string
		var errsField string
		for _, f := range e.Context {
			if f.Key == "path" {
				path = f.String
			}
			if f.Key == "errors" {
				errsField = f.String
			}
		}
		switch path {
		case "/boom":
			boomHasErrors = strings.Contains(errsField, "10.9.9.9")
		case "/fine":
			fineHasErrors = errsField != ""
		}
	}
	if !boomHasErrors {
		t.Error("c.Error() 挂的错误未以 errors 字段落 access 行")
	}
	if fineHasErrors {
		t.Error("无错误请求不应出现 errors 字段")
	}
}
