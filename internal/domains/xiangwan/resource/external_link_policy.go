package resource

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strings"
)

var externalDomainPattern = regexp.MustCompile(
	`^(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.)+` +
		`[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`,
)

var ErrInvalidExternalDomainPolicy = errors.New(
	"invalid xiangwan external-domain policy",
)

type ExternalDomainPolicy struct {
	exactHosts    map[string]struct{}
	wildcardRoots map[string]struct{}
}

func (policy ExternalDomainPolicy) Configured() bool {
	return len(policy.exactHosts) > 0 || len(policy.wildcardRoots) > 0
}

func NewExternalDomainPolicy(domains []string) (ExternalDomainPolicy, error) {
	policy := ExternalDomainPolicy{
		exactHosts:    make(map[string]struct{}, len(domains)),
		wildcardRoots: make(map[string]struct{}),
	}
	for _, raw := range domains {
		value := strings.ToLower(strings.TrimSpace(raw))
		wildcard := strings.HasPrefix(value, "*.")
		if wildcard {
			value = strings.TrimPrefix(value, "*.")
		}
		if !validExternalDomain(value) {
			return ExternalDomainPolicy{}, ErrInvalidExternalDomainPolicy
		}
		if wildcard {
			policy.wildcardRoots[value] = struct{}{}
		} else {
			policy.exactHosts[value] = struct{}{}
		}
	}
	return policy, nil
}

func (policy ExternalDomainPolicy) AllowURL(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed != raw || strings.ContainsAny(trimmed, "\\\r\n\t") {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !parsed.IsAbs() ||
		!strings.EqualFold(parsed.Scheme, "https") || parsed.Opaque != "" ||
		parsed.User != nil || parsed.Hostname() == "" {
		return "", false
	}
	port := parsed.Port()
	if port != "" && port != "443" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if !validExternalDomain(host) || !policy.allowsHost(host) {
		return "", false
	}
	parsed.Scheme = "https"
	parsed.Host = host
	return parsed.String(), true
}

func (policy ExternalDomainPolicy) allowsHost(host string) bool {
	if _, allowed := policy.exactHosts[host]; allowed {
		return true
	}
	for root := range policy.wildcardRoots {
		if strings.HasSuffix(host, "."+root) {
			return true
		}
	}
	return false
}

func validExternalDomain(value string) bool {
	return value != "" && len(value) <= 253 &&
		net.ParseIP(value) == nil && value != "localhost" &&
		externalDomainPattern.MatchString(value)
}
