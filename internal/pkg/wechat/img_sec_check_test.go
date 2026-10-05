package wechat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestImgSecCheckSendsMultipartMediaAndPasses(t *testing.T) {
	t.Parallel()

	var sawTokenRequest bool
	var sawImageRequest bool
	var capturedMedia []byte
	var capturedField string
	var capturedContentType string
	client := NewMiniApp()
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/cgi-bin/token"):
				sawTokenRequest = true
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(
						`{"access_token":"token-1","expires_in":7200}`,
					)),
				}, nil
			case strings.Contains(req.URL.Path, "/wxa/img_sec_check"):
				sawImageRequest = true
				if req.URL.Query().Get("access_token") != "token-1" {
					t.Errorf("img_sec_check token = %q, want token-1", req.URL.Query().Get("access_token"))
				}
				mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
				if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
					t.Fatalf("img_sec_check content type = %q err=%v", req.Header.Get("Content-Type"), err)
				}
				capturedContentType = req.Header.Get("Content-Type")
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("ReadAll() error = %v", err)
				}
				reader := multipart.NewReader(strings.NewReader(string(body)), params["boundary"])
				for {
					part, err := reader.NextPart()
					if err != nil {
						break
					}
					capturedField = part.FormName()
					capturedMedia, _ = io.ReadAll(part)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"application/json"}},
					Body:       io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)),
				}, nil
			default:
				t.Errorf("unexpected request to %s", req.URL)
				return nil, nil
			}
		}),
	}

	result, err := client.ImgSecCheck(
		context.Background(), "wxappid123", "secret-value-0123456789",
		[]byte("fake-image-bytes"), "avatar.png",
	)
	if err != nil {
		t.Fatalf("ImgSecCheck() error = %v", err)
	}
	if result.ErrCode != 0 {
		t.Fatalf("ImgSecCheck() errcode = %d", result.ErrCode)
	}
	if !sawTokenRequest || !sawImageRequest {
		t.Fatalf("sawTokenRequest=%t sawImageRequest=%t", sawTokenRequest, sawImageRequest)
	}
	if capturedField != "media" || string(capturedMedia) != "fake-image-bytes" {
		t.Fatalf("multipart field=%q media=%q", capturedField, capturedMedia)
	}
	if !strings.Contains(capturedContentType, "boundary=") {
		t.Fatalf("multipart content type = %q", capturedContentType)
	}
}

func TestImgSecCheckSurfacesRiskyVerdictAsData(t *testing.T) {
	t.Parallel()

	client := NewMiniApp()
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/cgi-bin/token"):
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						`{"access_token":"token-1","expires_in":7200}`,
					)),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"errcode":87014,"errmsg":"content is risky"}`)),
				}, nil
			}
		}),
	}
	result, err := client.ImgSecCheck(
		context.Background(), "wxappid123", "secret-value-0123456789", []byte("x"), "a.png",
	)
	if err != nil {
		t.Fatalf("ImgSecCheck() error = %v", err)
	}
	if result.ErrCode != 87014 {
		t.Fatalf("ImgSecCheck() errcode = %d, want 87014", result.ErrCode)
	}
}

func TestImgSecCheckRefreshesStaleTokenOnce(t *testing.T) {
	t.Parallel()

	tokenRequests := 0
	imageAttempts := 0
	client := NewMiniApp()
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/cgi-bin/token"):
				tokenRequests++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body: io.NopCloser(strings.NewReader(
						fmt.Sprintf(`{"access_token":"token-%d","expires_in":7200}`, tokenRequests),
					)),
				}, nil
			default:
				imageAttempts++
				if imageAttempts == 1 {
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(strings.NewReader(`{"errcode":40001,"errmsg":"invalid credential"}`)),
					}, nil
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(`{"errcode":0,"errmsg":"ok"}`)),
				}, nil
			}
		}),
	}
	result, err := client.ImgSecCheck(
		context.Background(), "wxappid123", "secret-value-0123456789", []byte("x"), "a.png",
	)
	if err != nil {
		t.Fatalf("ImgSecCheck() error = %v", err)
	}
	if result.ErrCode != 0 || imageAttempts != 2 || tokenRequests != 2 {
		t.Fatalf("errcode=%d imageAttempts=%d tokenRequests=%d", result.ErrCode, imageAttempts, tokenRequests)
	}
}

func TestImgSecCheckTokenFailureIsUnavailable(t *testing.T) {
	t.Parallel()

	client := NewMiniApp()
	client.httpClient = &http.Client{
		Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"errcode":40013,"errmsg":"invalid appid"}`)),
			}, nil
		}),
	}
	if _, err := client.ImgSecCheck(
		context.Background(), "wxappid123", "secret-value-0123456789", []byte("x"), "a.png",
	); err == nil {
		t.Fatal("ImgSecCheck() with failing token endpoint returned nil error")
	}
}

func TestImgSecCheckTransportErrorIsSanitized(t *testing.T) {
	t.Parallel()

	// The img_sec_check request URL carries access_token in its query string
	// and the token endpoint carries appid/secret: a raw *url.Error renders
	// the full URL, so both failure paths must strip the query before the
	// error can reach logs.
	newFailingTransport := func() *http.Client {
		return &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return nil, &url.Error{
					Op:  req.Method,
					URL: req.URL.String(),
					Err: errors.New("dial tcp 203.0.113.10:443: i/o timeout"),
				}
			}),
		}
	}

	t.Run("img request failure", func(t *testing.T) {
		t.Parallel()

		client := NewMiniApp()
		client.httpClient = &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				switch {
				case strings.Contains(req.URL.Path, "/cgi-bin/token"):
					return &http.Response{
						StatusCode: http.StatusOK,
						Body: io.NopCloser(strings.NewReader(
							`{"access_token":"token-1","expires_in":7200}`,
						)),
					}, nil
				default:
					// http.Client.Do wraps a RoundTripper failure in a
					// *url.Error whose URL keeps the access_token query —
					// the exact production leak this guard exists for.
					return nil, errors.New("dial tcp 203.0.113.10:443: i/o timeout")
				}
			}),
		}
		_, err := client.ImgSecCheck(
			context.Background(), "wxappid123", "secret-value-0123456789",
			[]byte("x"), "a.png",
		)
		if err == nil {
			t.Fatal("ImgSecCheck() with failing transport returned nil error")
		}
		for _, leaked := range []string{"access_token", "token-1"} {
			if strings.Contains(err.Error(), leaked) {
				t.Fatalf("ImgSecCheck() error leaks %q: %v", leaked, err)
			}
		}
	})

	t.Run("token endpoint failure", func(t *testing.T) {
		t.Parallel()

		client := NewMiniApp()
		client.httpClient = newFailingTransport()
		_, err := client.ImgSecCheck(
			context.Background(), "wxappid123", "secret-value-0123456789",
			[]byte("x"), "a.png",
		)
		if err == nil {
			t.Fatal("ImgSecCheck() with failing token endpoint returned nil error")
		}
		for _, leaked := range []string{"secret", "secret-value-0123456789"} {
			if strings.Contains(err.Error(), leaked) {
				t.Fatalf("ImgSecCheck() error leaks %q: %v", leaked, err)
			}
		}
	})
}
