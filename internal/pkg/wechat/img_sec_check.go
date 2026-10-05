package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
)

// ImgSecCheckResponse is the synchronous image content-security verdict of
// wxa/img_sec_check: ErrCode 0 passes and 87014 means the image contains
// risky content. Any other ErrCode is a provider-side failure the caller must
// treat as "moderation unavailable", never as a pass.
type ImgSecCheckResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

// ImgSecCheck screens one image through the WeChat wxa/img_sec_check API.
// The image travels as multipart/form-data field "media"; the verdict is
// synchronous, unlike mediaCheckAsync. Access-token fetch and the in-memory
// TTL cache are shared with the other MiniApp calls, and a stale token
// (40001/42001) triggers one controlled refresh + retry. Callers bound the
// media size before invoking: the API caps media at 1 MiB upstream, so
// anything larger is a guaranteed provider refusal and the avatar handler
// enforces the same 1 MiB bound before calling.
func (m *MiniApp) ImgSecCheck(
	ctx context.Context,
	appID string,
	appSecret string,
	media []byte,
	filename string,
) (*ImgSecCheckResponse, error) {
	ctx = ensureCtx(ctx)
	accessToken, err := m.getAccessToken(ctx, appID, appSecret, false)
	if err != nil {
		// The token endpoint carries appid/secret in its query string, so a
		// transport failure must be sanitized before it can reach logs.
		return nil, sanitizeWechatRequestError(err)
	}
	result, err := m.doImgSecCheckRequest(ctx, accessToken, media, filename)
	if err != nil && shouldRetryWechatAccessTokenErr(err) {
		refreshedToken, refreshErr := m.getAccessToken(ctx, appID, appSecret, true)
		if refreshErr == nil && refreshedToken != accessToken {
			result, err = m.doImgSecCheckRequest(ctx, refreshedToken, media, filename)
		}
	}
	if err != nil {
		return nil, err
	}
	return result, nil
}

// sanitizeWechatRequestError strips credential-bearing URL query strings from
// a transport error before it can reach logs. http.Client.Do failures are
// *url.Error values whose Error() renders the full request URL, and both the
// img_sec_check access_token and the token endpoint's appid/secret travel in
// that query string — logging the raw error would write credentials into the
// access log. The sanitized copy keeps only the operation, the scheme/host/
// path (never the query), and the inner error classification; nested
// *url.Error values are sanitized the same way, and the result stays an
// *url.Error chain so errors.As/Is callers keep working.
func sanitizeWechatRequestError(err error) error {
	var urlErr *url.Error
	if !errors.As(err, &urlErr) {
		return err
	}
	sanitized := *urlErr
	if parsed, parseErr := url.Parse(urlErr.URL); parseErr == nil {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		sanitized.URL = parsed.String()
	} else {
		sanitized.URL = "(redacted unparseable URL)"
	}
	if nested := sanitizeWechatRequestError(urlErr.Err); nested != urlErr.Err {
		sanitized.Err = nested
	}
	return &sanitized
}

func (m *MiniApp) doImgSecCheckRequest(
	ctx context.Context,
	accessToken string,
	media []byte,
	filename string,
) (*ImgSecCheckResponse, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("media", filename)
	if err != nil {
		return nil, fmt.Errorf("wechat img_sec_check multipart build failed: %w", err)
	}
	if _, err := part.Write(media); err != nil {
		return nil, fmt.Errorf("wechat img_sec_check multipart write failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("wechat img_sec_check multipart close failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://api.weixin.qq.com/wxa/img_sec_check?access_token=%s", accessToken),
		&body,
	)
	if err != nil {
		return nil, fmt.Errorf("wechat img_sec_check request build failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	applyRequestIDHeader(httpReq)

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		// The request URL carries access_token in its query string; the
		// sanitized error keeps the operation and failure class only.
		return nil, fmt.Errorf(
			"wechat img_sec_check request failed: %w", sanitizeWechatRequestError(err),
		)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wechat img_sec_check read body failed: %w", err)
	}
	var result ImgSecCheckResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("wechat img_sec_check unmarshal failed: %w", err)
	}
	// 87014 is the content verdict (risky image) and must reach the caller as
	// data; every other non-zero errcode is a provider failure like the other
	// endpoints map it, which also lets the 40001/42001 stale-token retry
	// trigger on the message.
	if result.ErrCode != 0 && result.ErrCode != 87014 {
		return nil, fmt.Errorf(
			"wechat img_sec_check error: %d %s", result.ErrCode, result.ErrMsg,
		)
	}
	return &result, nil
}
