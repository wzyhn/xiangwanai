package activity

import (
	"strings"
	"testing"
)

func TestValidInstanceCoverImageURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty means no cover", value: "", want: true},
		{name: "https absolute", value: "https://cdn.example.com/covers/a.jpg", want: true},
		{name: "https with query", value: "https://cdn.example.com/covers/a.jpg?v=2", want: true},
		{name: "fragment rejected", value: "https://cdn.example.com/covers/a.jpg#frag", want: false},
		{name: "http rejected", value: "http://cdn.example.com/covers/a.jpg", want: false},
		{name: "relative cover path accepted", value: "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.jpg", want: true},
		{name: "relative webp cover path accepted", value: "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp", want: true},
		{name: "relative path wrong extension rejected", value: "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.gif", want: false},
		{name: "relative path uppercase rejected", value: "/api/v1/xiangwan/covers/0123456789ABCDEF0123456789ABCDEF.jpg", want: false},
		{name: "relative path traversal rejected", value: "/api/v1/xiangwan/covers/../0123456789abcdef0123456789abcdef.jpg", want: false},
		{name: "hostless rejected", value: "https:///covers/a.jpg", want: false},
		{name: "surrounding space rejected", value: " https://cdn.example.com/a.jpg", want: false},
		{name: "control character rejected", value: "https://cdn.example.com/a\n.jpg", want: false},
		{name: "too short", value: "https://", want: false},
		{name: "too long", value: "https://cdn.example.com/" + strings.Repeat("a", 2048), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidInstanceCoverImageURL(test.value); got != test.want {
				t.Fatalf("ValidInstanceCoverImageURL(%q) = %v, want %v", test.value, got, test.want)
			}
		})
	}
}
