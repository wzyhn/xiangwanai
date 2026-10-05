package wechat

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// nilCtxTransport answers wechat upstream calls with a generic 200 JSON body
// so we can exercise the entrypoints without hitting the real WeChat API.
// The token endpoint handshake also needs to succeed for the
// access-token-gated paths.
func nilCtxTransport() roundTripFunc {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body := `{"errcode":0,"errmsg":"ok","access_token":"fake-token","expires_in":7200,"openid":"o","session_key":"s","trace_id":"t"}`
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
}

func newNilCtxMiniApp() *MiniApp {
	m := NewMiniApp()
	m.httpClient = &http.Client{Transport: nilCtxTransport()}
	return m
}

// TestMiniAppNilCtxDoesNotPanic asserts every public outbound method tolerates
// a nil ctx by defaulting to context.Background(). Pre-PR-β safety sweep,
// passing nil panicked inside http.NewRequestWithContext.
func TestMiniAppNilCtxDoesNotPanic(t *testing.T) {
	m := newNilCtxMiniApp()

	t.Run("Code2Session", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		_, _ = m.Code2Session(nil, "appid", "secret", "code") //nolint:staticcheck // SA1012 deliberate nil ctx
	})

	t.Run("GetUnlimitedQRCode", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		_, _ = m.GetUnlimitedQRCode(nil, "appid", "secret", UnlimitedQRCodeRequest{Scene: "a", Page: "p"}) //nolint:staticcheck
	})

	t.Run("SendSubscribeMessage", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		_, _ = m.SendSubscribeMessage(nil, "appid", "secret", SubscribeMessageRequest{ToUser: "u", TemplateID: "t"}) //nolint:staticcheck
	})

	t.Run("MsgSecCheck", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		_, _ = m.MsgSecCheck(nil, "appid", "secret", MsgSecCheckRequest{OpenID: "o", Content: "hello"}) //nolint:staticcheck
	})

	t.Run("MediaCheckAsync", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("panic: %v", r)
			}
		}()
		_, _ = m.MediaCheckAsync(nil, "appid", "secret", MediaCheckAsyncRequest{OpenID: "o", MediaURL: "https://x", MediaType: 1}) //nolint:staticcheck
	})
}
