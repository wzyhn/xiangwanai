package xiangwanruntime

import (
	"errors"
	"net/http"
	"strings"
	"time"
)

const publicMediaPathPrefix = "/api/v1/xiangwan/media/"

func withoutPublicMediaWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if isPublicMediaStreamRequest(request) {
			err := http.NewResponseController(writer).SetWriteDeadline(time.Time{})
			if err != nil && !errors.Is(err, http.ErrNotSupported) {
				http.Error(writer, "service unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func isPublicMediaStreamRequest(request *http.Request) bool {
	if request == nil || request.URL == nil ||
		(request.Method != http.MethodGet && request.Method != http.MethodHead) ||
		!strings.HasPrefix(request.URL.Path, publicMediaPathPrefix) {
		return false
	}
	suffix := strings.TrimPrefix(request.URL.Path, publicMediaPathPrefix)
	parts := strings.Split(suffix, "/")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
	}
	return true
}
