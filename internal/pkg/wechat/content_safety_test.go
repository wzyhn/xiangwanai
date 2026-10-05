package wechat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestDoAccessTokenJSONRequestDoesNotEscapeSignedMediaURL(t *testing.T) {
	var capturedBody string
	client := &MiniApp{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("ReadAll request body failed: %v", err)
				}
				capturedBody = string(body)
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": []string{"application/json"},
					},
					Body: io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok","trace_id":"trace-1"}`)),
				}, nil
			}),
		},
	}

	_, err := client.doAccessTokenJSONRequest(context.Background(), "token-1", "/wxa/media_check_async", MediaCheckAsyncRequest{
		MediaURL:  "https://api.example.com/api/v1/files/file-1/content?exp=1&sig=test",
		MediaType: 2,
	})
	if err != nil {
		t.Fatalf("doAccessTokenJSONRequest failed: %v", err)
	}

	if !strings.Contains(capturedBody, "&sig=test") {
		t.Fatalf("expected raw ampersand in request body, got %q", capturedBody)
	}
	if strings.Contains(capturedBody, "\\u0026") {
		t.Fatalf("expected request body to avoid unicode-escaped ampersand, got %q", capturedBody)
	}
}
