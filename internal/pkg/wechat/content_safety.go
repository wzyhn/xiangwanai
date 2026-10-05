package wechat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const contentSafetyVersion = 2

type contentSafetyError struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

type ContentSafetyResult struct {
	Suggest string `json:"suggest"`
	Label   int    `json:"label"`
}

type ContentSafetyDetail struct {
	Strategy string `json:"strategy"`
	ErrCode  int    `json:"errcode"`
	Suggest  string `json:"suggest"`
	Label    int    `json:"label"`
	Keyword  string `json:"keyword,omitempty"`
	Prob     int    `json:"prob,omitempty"`
	Level    int    `json:"level,omitempty"`
}

type MsgSecCheckRequest struct {
	OpenID    string `json:"openid"`
	Scene     int    `json:"scene"`
	Version   int    `json:"version"`
	Content   string `json:"content"`
	Title     string `json:"title,omitempty"`
	Nickname  string `json:"nickname,omitempty"`
	Signature string `json:"signature,omitempty"`
}

type MsgSecCheckResponse struct {
	ErrCode int                   `json:"errcode"`
	ErrMsg  string                `json:"errmsg"`
	Result  ContentSafetyResult   `json:"result"`
	Detail  []ContentSafetyDetail `json:"detail"`
	TraceID string                `json:"trace_id"`
}

type MediaCheckAsyncRequest struct {
	OpenID    string `json:"openid"`
	Scene     int    `json:"scene"`
	Version   int    `json:"version"`
	MediaURL  string `json:"media_url"`
	MediaType int    `json:"media_type"`
}

type MediaCheckAsyncResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
	TraceID string `json:"trace_id"`
}

type MediaCheckAsyncCallback struct {
	ToUserName   string                `json:"ToUserName"`
	FromUserName string                `json:"FromUserName"`
	CreateTime   int64                 `json:"CreateTime"`
	MsgType      string                `json:"MsgType"`
	Event        string                `json:"Event"`
	AppID        string                `json:"appid"`
	TraceID      string                `json:"trace_id"`
	Version      int                   `json:"version"`
	ErrCode      int                   `json:"errcode"`
	ErrMsg       string                `json:"errmsg"`
	Result       ContentSafetyResult   `json:"result"`
	Detail       []ContentSafetyDetail `json:"detail"`
	Encrypt      string                `json:"Encrypt,omitempty"`
}

func (m *MiniApp) MsgSecCheck(ctx context.Context, appID, appSecret string, req MsgSecCheckRequest) (*MsgSecCheckResponse, error) {
	ctx = ensureCtx(ctx)
	if req.Version == 0 {
		req.Version = contentSafetyVersion
	}
	var resp MsgSecCheckResponse
	if err := m.postWithAccessToken(ctx, appID, appSecret, "/wxa/msg_sec_check", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *MiniApp) MediaCheckAsync(ctx context.Context, appID, appSecret string, req MediaCheckAsyncRequest) (*MediaCheckAsyncResponse, error) {
	ctx = ensureCtx(ctx)
	if req.Version == 0 {
		req.Version = contentSafetyVersion
	}
	var resp MediaCheckAsyncResponse
	if err := m.postWithAccessToken(ctx, appID, appSecret, "/wxa/media_check_async", req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (m *MiniApp) postWithAccessToken(ctx context.Context, appID, appSecret, path string, payload any, out any) error {
	ctx = ensureCtx(ctx)
	accessToken, err := m.getAccessToken(ctx, appID, appSecret, false)
	if err != nil {
		return err
	}

	respBody, err := m.doAccessTokenJSONRequest(ctx, accessToken, path, payload)
	if err != nil {
		if shouldRetryWechatAccessTokenErr(err) {
			refreshedToken, refreshErr := m.getAccessToken(ctx, appID, appSecret, true)
			if refreshErr == nil && refreshedToken != accessToken {
				respBody, err = m.doAccessTokenJSONRequest(ctx, refreshedToken, path, payload)
			}
		}
	}
	if err != nil {
		return err
	}

	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("wechat %s unmarshal failed: %w", path, err)
	}
	return nil
}

func (m *MiniApp) doAccessTokenJSONRequest(ctx context.Context, accessToken, path string, payload any) ([]byte, error) {
	var bodyBuffer bytes.Buffer
	encoder := json.NewEncoder(&bodyBuffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return nil, fmt.Errorf("wechat %s marshal failed: %w", path, err)
	}
	body := bytes.TrimSpace(bodyBuffer.Bytes())

	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("https://api.weixin.qq.com%s?access_token=%s", path, accessToken),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("wechat %s request build failed: %w", path, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	applyRequestIDHeader(httpReq)

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("wechat %s request failed: %w", path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("wechat %s read body failed: %w", path, err)
	}

	var wechatErr contentSafetyError
	if err := json.Unmarshal(respBody, &wechatErr); err == nil && wechatErr.ErrCode != 0 {
		return nil, fmt.Errorf("wechat %s error: %d %s", path, wechatErr.ErrCode, strings.TrimSpace(wechatErr.ErrMsg))
	}

	return respBody, nil
}

func shouldRetryWechatAccessTokenErr(err error) bool {
	normalized := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(normalized, "40001") || strings.Contains(normalized, "42001")
}
