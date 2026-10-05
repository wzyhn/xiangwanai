package wechat

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCode2SessionEncodesEveryQueryValue(t *testing.T) {
	t.Parallel()

	const (
		appID     = "wx-app&id"
		appSecret = "test" + "-secret=owned&server"
		code      = "code&grant_type=client_credentials"
	)
	client := &MiniApp{
		httpClient: &http.Client{Transport: roundTripFunc(func(
			request *http.Request,
		) (*http.Response, error) {
			query := request.URL.Query()
			if query.Get("appid") != appID || query.Get("secret") != appSecret ||
				query.Get("js_code") != code ||
				query.Get("grant_type") != "authorization_code" || len(query) != 4 {
				t.Fatalf("code2session query = %#v", query)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(
					`{"openid":"openid-1","session_key":"private"}`,
				)),
			}, nil
		})},
		tokens: make(map[string]cachedAccessToken),
	}

	result, err := client.Code2Session(
		context.Background(),
		appID,
		appSecret,
		code,
	)
	if err != nil || result == nil || result.OpenID != "openid-1" {
		t.Fatalf("Code2Session() = %+v, %v", result, err)
	}
}
