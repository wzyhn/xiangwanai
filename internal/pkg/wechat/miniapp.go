package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// SessionResult is the response from WeChat code2session API.
type SessionResult struct {
	OpenID     string `json:"openid"`
	SessionKey string `json:"session_key"`
	UnionID    string `json:"unionid"`
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

type accessTokenResult struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	ErrCode     int    `json:"errcode"`
	ErrMsg      string `json:"errmsg"`
}

type SubscribeMessageDataItem struct {
	Value string `json:"value"`
}

type SubscribeMessageRequest struct {
	ToUser           string                              `json:"touser"`
	TemplateID       string                              `json:"template_id"`
	Page             string                              `json:"page,omitempty"`
	Data             map[string]SubscribeMessageDataItem `json:"data"`
	MiniprogramState string                              `json:"miniprogram_state,omitempty"`
	Lang             string                              `json:"lang,omitempty"`
}

type SubscribeMessageResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	MsgID   int64  `json:"msgid"`
}

// UnlimitedQRCodeRequest is the request body for getwxacodeunlimit.
type UnlimitedQRCodeRequest struct {
	Scene      string `json:"scene"`
	Page       string `json:"page"`
	CheckPath  bool   `json:"check_path"`
	EnvVersion string `json:"env_version,omitempty"`
	Width      int    `json:"width,omitempty"`
}

type cachedAccessToken struct {
	value     string
	expiresAt time.Time
}

// MiniApp is a WeChat Mini Program client.
type MiniApp struct {
	httpClient *http.Client
	mu         sync.Mutex
	tokens     map[string]cachedAccessToken
}

// NewMiniApp creates a MiniApp client.
func NewMiniApp() *MiniApp {
	return &MiniApp{
		httpClient: &http.Client{Timeout: 5 * time.Second},
		tokens:     make(map[string]cachedAccessToken),
	}
}

// Code2Session exchanges a login code for an openid/session_key/unionid.
//
// ctx propagation (PR #101):the request inherits ctx so handler-level
// cancel/timeout aborts the upstream WeChat call instead of letting it
// run to the client's fixed timeout.
func (m *MiniApp) Code2Session(ctx context.Context, appID, appSecret, code string) (*SessionResult, error) {
	ctx = ensureCtx(ctx)
	endpoint := url.URL{
		Scheme: "https",
		Host:   "api.weixin.qq.com",
		Path:   "/sns/jscode2session",
	}
	query := endpoint.Query()
	query.Set("appid", appID)
	query.Set("secret", appSecret)
	query.Set("js_code", code)
	query.Set("grant_type", "authorization_code")
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("wechat code2session request build failed: %w", err)
	}
	applyRequestIDHeader(httpReq)
	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("wechat code2session request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wechat code2session read body failed: %w", err)
	}

	var result SessionResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("wechat code2session unmarshal failed: %w", err)
	}

	if result.ErrCode != 0 {
		return nil, fmt.Errorf("wechat code2session error: %d %s", result.ErrCode, result.ErrMsg)
	}

	return &result, nil
}

// GetUnlimitedQRCode generates a WeChat mini program code image.
func (m *MiniApp) GetUnlimitedQRCode(ctx context.Context, appID, appSecret string, req UnlimitedQRCodeRequest) ([]byte, error) {
	ctx = ensureCtx(ctx)
	accessToken, err := m.getAccessToken(ctx, appID, appSecret, false)
	if err != nil {
		return nil, err
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("wechat getwxacodeunlimit marshal failed: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://api.weixin.qq.com/wxa/getwxacodeunlimit?access_token=%s", accessToken),
		bytes.NewReader(payload),
	)
	if err != nil {
		return nil, fmt.Errorf("wechat getwxacodeunlimit request build failed: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	applyRequestIDHeader(httpReq)

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("wechat getwxacodeunlimit request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wechat getwxacodeunlimit read body failed: %w", err)
	}

	if isWechatJSONResponse(resp.Header.Get("Content-Type"), body) {
		var result accessTokenResult
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("wechat getwxacodeunlimit unmarshal failed: %w", err)
		}
		if shouldRetryWechatAccessToken(result.ErrCode) {
			refreshedToken, refreshErr := m.getAccessToken(ctx, appID, appSecret, true)
			if refreshErr == nil && refreshedToken != accessToken {
				return m.GetUnlimitedQRCode(ctx, appID, appSecret, req)
			}
		}
		if result.ErrCode != 0 {
			return nil, fmt.Errorf("wechat getwxacodeunlimit error: %d %s", result.ErrCode, result.ErrMsg)
		}
		return nil, fmt.Errorf("wechat getwxacodeunlimit returned unexpected json response")
	}

	return body, nil
}

func (m *MiniApp) SendSubscribeMessage(ctx context.Context, appID, appSecret string, req SubscribeMessageRequest) (*SubscribeMessageResponse, error) {
	ctx = ensureCtx(ctx)
	var resp SubscribeMessageResponse
	if err := m.postWithAccessToken(ctx, appID, appSecret, "/cgi-bin/message/subscribe/send", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *MiniApp) getAccessToken(ctx context.Context, appID, appSecret string, forceRefresh bool) (string, error) {
	ctx = ensureCtx(ctx)
	now := time.Now().UTC()

	if !forceRefresh {
		m.mu.Lock()
		if cached, ok := m.tokens[appID]; ok && cached.value != "" && now.Before(cached.expiresAt) {
			m.mu.Unlock()
			return cached.value, nil
		}
		m.mu.Unlock()
	}

	endpoint := url.URL{
		Scheme: "https",
		Host:   "api.weixin.qq.com",
		Path:   "/cgi-bin/token",
	}
	query := endpoint.Query()
	query.Set("grant_type", "client_credential")
	query.Set("appid", appID)
	query.Set("secret", appSecret)
	endpoint.RawQuery = query.Encode()

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		endpoint.String(),
		nil,
	)
	if err != nil {
		return "", fmt.Errorf("wechat access_token request build failed: %w", err)
	}
	applyRequestIDHeader(httpReq)
	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("wechat access_token request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("wechat access_token read body failed: %w", err)
	}

	var result accessTokenResult
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("wechat access_token unmarshal failed: %w", err)
	}
	if result.ErrCode != 0 {
		return "", fmt.Errorf("wechat access_token error: %d %s", result.ErrCode, result.ErrMsg)
	}
	if strings.TrimSpace(result.AccessToken) == "" {
		return "", fmt.Errorf("wechat access_token missing access_token")
	}

	expiresIn := time.Duration(result.ExpiresIn) * time.Second
	if expiresIn <= 0 {
		expiresIn = 2 * time.Hour
	}
	expiresAt := now.Add(expiresIn - 2*time.Minute)
	if !expiresAt.After(now) {
		expiresAt = now.Add(time.Minute)
	}

	m.mu.Lock()
	m.tokens[appID] = cachedAccessToken{
		value:     strings.TrimSpace(result.AccessToken),
		expiresAt: expiresAt,
	}
	m.mu.Unlock()

	return strings.TrimSpace(result.AccessToken), nil
}

func isWechatJSONResponse(contentType string, body []byte) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(contentType)), "application/json") {
		return true
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func shouldRetryWechatAccessToken(errCode int) bool {
	return errCode == 40001 || errCode == 42001
}
