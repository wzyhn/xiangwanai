package xiangwanapi

import "testing"

func TestNormalizeAdminOriginCollapsesTheHTTPSDefaultPort(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{
			name:  "explicit default port",
			value: "https://admin.example.com:443",
			want:  "https://admin.example.com",
		},
		{
			name:  "zero padded default port",
			value: "https://admin.example.com:0443",
			want:  "https://admin.example.com",
		},
		{
			name:  "implicit default port",
			value: "https://Admin.Example.com",
			want:  "https://admin.example.com",
		},
		{
			name:  "non default port is canonicalized",
			value: "https://admin.example.com:08443",
			want:  "https://admin.example.com:8443",
		},
		{
			name:  "bracketed IPv6 host keeps its brackets",
			value: "https://[2001:DB8::1]:443",
			want:  "https://[2001:db8::1]",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			got, err := normalizeAdminOrigin(testCase.value)
			if err != nil || got != testCase.want {
				t.Fatalf("normalizeAdminOrigin(%q) = %q, %v; want %q", testCase.value, got, err, testCase.want)
			}
		})
	}
}

func TestNormalizeAdminOriginMatchesTheBrowserOriginOfADefaultPortConfiguration(t *testing.T) {
	t.Parallel()

	configured, configuredErr := normalizeAdminOrigin("https://admin.example.com:443")
	presented, presentedErr := normalizeAdminOrigin("https://admin.example.com")
	if configuredErr != nil || presentedErr != nil || configured != presented {
		t.Fatalf(
			"normalizeAdminOrigin() configured=%q (%v) presented=%q (%v); want equal",
			configured, configuredErr, presented, presentedErr,
		)
	}
}

func TestNormalizeAdminOriginRejectsNonOriginValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"http://admin.example.com",
		"https://",
		"https://admin.example.com/path",
		"https://admin.example.com?query=1",
		"https://admin.example.com#fragment",
		"https://admin.example.com:65536",
		"://admin.example.com",
	} {
		if got, err := normalizeAdminOrigin(value); err == nil {
			t.Fatalf("normalizeAdminOrigin(%q) = %q, nil; want error", value, got)
		}
	}
}
