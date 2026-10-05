package fileurl

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const DefaultSignedAccessTTL = 30 * 24 * time.Hour

// BuildSignedPurposeAccessPath creates a bearer URL whose signature covers a
// fixed route, explicit purpose, resource identities, optional owner and
// expiry. It is deliberately separate from the legacy file-only signer: a
// caller must name the business purpose and all source-bound identifiers.
func BuildSignedPurposeAccessPath(path, purpose string, resourceIDs []uuid.UUID, principalID *uuid.UUID, expiresAt time.Time, secret string) string {
	path = strings.TrimSpace(path)
	purpose = strings.TrimSpace(purpose)
	if path == "" || purpose == "" || len(resourceIDs) == 0 || expiresAt.IsZero() || strings.TrimSpace(secret) == "" {
		return ""
	}
	for _, resourceID := range resourceIDs {
		if resourceID == uuid.Nil {
			return ""
		}
	}
	expUnix := expiresAt.UTC().Unix()
	sig := signPurposeAccess(path, purpose, resourceIDs, principalID, expUnix, secret)
	return path + "?purpose=" + url.QueryEscape(purpose) + "&exp=" + strconv.FormatInt(expUnix, 10) + "&sig=" + sig
}

func BuildAccessPath(fileID uuid.UUID, contentID *uuid.UUID) string {
	path := "/api/v1/files/" + fileID.String() + "/content"
	if contentID == nil || *contentID == uuid.Nil {
		return path
	}
	return path + "?content_id=" + contentID.String()
}

func BuildSignedAccessPath(fileID uuid.UUID, expiresAt time.Time, secret string) string {
	expUnix := expiresAt.UTC().Unix()
	sig := signAccess(fileID, expUnix, secret)
	return BuildAccessPath(fileID, nil) + "?exp=" + strconv.FormatInt(expUnix, 10) + "&sig=" + sig
}

// BuildSignedSourceBoundAccessPath creates a short-lived media grant whose
// signature covers the current source content, optional block reference and
// purpose.  The query values are part of the credential so a bearer URL cannot
// be retargeted to another Content or use.
func BuildSignedSourceBoundAccessPath(fileID, contentID, blockID uuid.UUID, purpose string, principalID *uuid.UUID, expiresAt time.Time, secret string) string {
	if fileID == uuid.Nil || contentID == uuid.Nil || blockID == uuid.Nil || strings.TrimSpace(purpose) == "" {
		return ""
	}
	expUnix := expiresAt.UTC().Unix()
	sig := signSourceAccess(fileID, contentID, blockID, purpose, principalID, expUnix, secret)
	path := BuildAccessPath(fileID, &contentID)
	query := "&purpose=" + url.QueryEscape(strings.TrimSpace(purpose))
	if blockID != uuid.Nil {
		query += "&block_id=" + blockID.String()
	}
	if principalID != nil && *principalID != uuid.Nil {
		query += "&principal_id=" + principalID.String()
	}
	return path + query + "&exp=" + strconv.FormatInt(expUnix, 10) + "&sig=" + sig
}

// BuildSignedSourceBoundAccessPathWithTenant is the school-scoped counterpart
// to BuildSignedSourceBoundAccessPath. The tenant is included in the HMAC only
// when present so existing non-school source-bound consumers keep their exact
// credential format.
func BuildSignedSourceBoundAccessPathWithTenant(fileID, contentID, blockID uuid.UUID, purpose string, principalID, tenantID *uuid.UUID, expiresAt time.Time, secret string) string {
	if tenantID == nil || *tenantID == uuid.Nil {
		return BuildSignedSourceBoundAccessPath(fileID, contentID, blockID, purpose, principalID, expiresAt, secret)
	}
	if fileID == uuid.Nil || contentID == uuid.Nil || blockID == uuid.Nil || strings.TrimSpace(purpose) == "" {
		return ""
	}
	expUnix := expiresAt.UTC().Unix()
	sig := signSourceAccessWithTenant(fileID, contentID, blockID, purpose, principalID, tenantID, expUnix, secret)
	path := BuildAccessPath(fileID, &contentID)
	query := "&purpose=" + url.QueryEscape(strings.TrimSpace(purpose))
	query += "&block_id=" + blockID.String()
	query += "&tenant_id=" + tenantID.String()
	if principalID != nil && *principalID != uuid.Nil {
		query += "&principal_id=" + principalID.String()
	}
	return path + query + "&exp=" + strconv.FormatInt(expUnix, 10) + "&sig=" + sig
}

// BuildAbsoluteSignedSourceBoundAccessURL is the absolute-URL counterpart
// used by API response projections.
func BuildAbsoluteSignedSourceBoundAccessURL(baseURL string, fileID, contentID, blockID uuid.UUID, purpose string, principalID *uuid.UUID, expiresAt time.Time, secret string) string {
	return BuildAbsoluteURL(baseURL, BuildSignedSourceBoundAccessPath(fileID, contentID, blockID, purpose, principalID, expiresAt, secret))
}

func BuildAbsoluteSignedSourceBoundAccessURLWithTenant(baseURL string, fileID, contentID, blockID uuid.UUID, purpose string, principalID, tenantID *uuid.UUID, expiresAt time.Time, secret string) string {
	return BuildAbsoluteURL(baseURL, BuildSignedSourceBoundAccessPathWithTenant(fileID, contentID, blockID, purpose, principalID, tenantID, expiresAt, secret))
}

func BuildAbsoluteURL(baseURL, relativePath string) string {
	trimmedPath := strings.TrimSpace(relativePath)
	if trimmedPath == "" {
		return ""
	}
	if strings.HasPrefix(trimmedPath, "http://") || strings.HasPrefix(trimmedPath, "https://") {
		return trimmedPath
	}

	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmedBase == "" {
		return trimmedPath
	}
	if strings.HasPrefix(trimmedPath, "/") {
		return trimmedBase + trimmedPath
	}
	return trimmedBase + "/" + trimmedPath
}

func BuildAbsoluteSignedAccessURL(baseURL string, fileID uuid.UUID, expiresAt time.Time, secret string) string {
	return BuildAbsoluteURL(baseURL, BuildSignedAccessPath(fileID, expiresAt, secret))
}

func ParseAccessPathFileID(raw string) (uuid.UUID, bool) {
	location := strings.TrimSpace(raw)
	if location == "" {
		return uuid.UUID{}, false
	}
	if parsedURL, err := url.Parse(location); err == nil {
		if strings.TrimSpace(parsedURL.Path) != "" {
			location = parsedURL.Path
		}
	}
	location = strings.SplitN(location, "?", 2)[0]
	location = strings.SplitN(location, "#", 2)[0]
	location = strings.TrimSpace(strings.Trim(location, "/"))
	parts := strings.Split(location, "/")
	if len(parts) != 5 {
		return uuid.UUID{}, false
	}
	if parts[0] != "api" || parts[1] != "v1" || parts[2] != "files" || parts[4] != "content" {
		return uuid.UUID{}, false
	}
	fileID, err := uuid.Parse(strings.TrimSpace(parts[3]))
	if err != nil {
		return uuid.UUID{}, false
	}
	return fileID, true
}

// SignAvatarURL converts a stored avatar reference into a freshly-signed file path
// that an unauthenticated <image> load can fetch. If raw is a platform file access
// path (/api/v1/files/<id>/content...), it re-signs with a fresh exp+sig so the
// signed-access gate passes (an empty secret yields the unsigned access path). Non-file
// values (including Auth's current-principal avatar route) pass through unchanged.
// This is a legacy compatibility signer, not a current-principal authorization
// check. Auth's profile and directory projections no longer issue these grants.
func SignAvatarURL(raw, secret string) string {
	fileID, ok := ParseAccessPathFileID(raw)
	if !ok {
		return raw
	}
	if strings.TrimSpace(secret) != "" {
		return BuildSignedAccessPath(fileID, time.Now().Add(DefaultSignedAccessTTL), secret)
	}
	return BuildAccessPath(fileID, nil)
}

func ValidateSignedAccess(fileID uuid.UUID, expUnix int64, sig, secret string, now time.Time) bool {
	return ValidateSignedAccessAny(fileID, expUnix, sig, []string{secret}, now)
}

// ValidateSignedAccessAny accepts signatures made with the active key or a
// bounded list of compatibility keys. Callers must always issue new URLs with
// the active key and remove old keys after the longest signed-URL TTL elapses.
func ValidateSignedAccessAny(fileID uuid.UUID, expUnix int64, sig string, secrets []string, now time.Time) bool {
	sig = strings.TrimSpace(sig)
	if fileID == uuid.Nil || expUnix <= 0 || sig == "" || len(secrets) == 0 {
		return false
	}
	if now.UTC().Unix() > expUnix {
		return false
	}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		expected := signAccess(fileID, expUnix, secret)
		if hmac.Equal([]byte(expected), []byte(sig)) {
			return true
		}
	}
	return false
}

// ValidateSignedPurposeAccess verifies a purpose-bound grant. Callers must
// still re-read the current business row and file lifecycle before returning
// bytes; this check only prevents query tampering and route retargeting.
func ValidateSignedPurposeAccess(path, purpose string, resourceIDs []uuid.UUID, principalID *uuid.UUID, expUnix int64, sig string, secrets []string, now time.Time) bool {
	path = strings.TrimSpace(path)
	purpose = strings.TrimSpace(purpose)
	sig = strings.TrimSpace(sig)
	if path == "" || purpose == "" || len(resourceIDs) == 0 || expUnix <= 0 || sig == "" || len(secrets) == 0 {
		return false
	}
	for _, resourceID := range resourceIDs {
		if resourceID == uuid.Nil {
			return false
		}
	}
	if now.UTC().Unix() > expUnix {
		return false
	}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		expected := signPurposeAccess(path, purpose, resourceIDs, principalID, expUnix, secret)
		if hmac.Equal([]byte(expected), []byte(sig)) {
			return true
		}
	}
	return false
}

// ValidateSignedSourceBoundAccess verifies a source-bound grant.  Callers
// still need to re-read Content/blocks/file state after this cryptographic
// check; the signature only prevents query tampering and retargeting.
func ValidateSignedSourceBoundAccess(fileID, contentID, blockID uuid.UUID, purpose string, principalID *uuid.UUID, expUnix int64, sig string, secrets []string, now time.Time) bool {
	return ValidateSignedSourceBoundAccessWithTenant(fileID, contentID, blockID, purpose, principalID, nil, expUnix, sig, secrets, now)
}

func ValidateSignedSourceBoundAccessWithTenant(fileID, contentID, blockID uuid.UUID, purpose string, principalID, tenantID *uuid.UUID, expUnix int64, sig string, secrets []string, now time.Time) bool {
	sig = strings.TrimSpace(sig)
	if fileID == uuid.Nil || contentID == uuid.Nil || blockID == uuid.Nil || strings.TrimSpace(purpose) == "" || expUnix <= 0 || sig == "" || len(secrets) == 0 {
		return false
	}
	if now.UTC().Unix() > expUnix {
		return false
	}
	for _, secret := range secrets {
		secret = strings.TrimSpace(secret)
		if secret == "" {
			continue
		}
		expected := signSourceAccessWithTenant(fileID, contentID, blockID, purpose, principalID, tenantID, expUnix, secret)
		if hmac.Equal([]byte(expected), []byte(sig)) {
			return true
		}
	}
	return false
}

func signAccess(fileID uuid.UUID, expUnix int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	mac.Write([]byte(fileID.String()))
	mac.Write([]byte{'\n'})
	mac.Write([]byte(strconv.FormatInt(expUnix, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func signPurposeAccess(path, purpose string, resourceIDs []uuid.UUID, principalID *uuid.UUID, expUnix int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	values := []string{strings.TrimSpace(path), strings.TrimSpace(purpose)}
	for _, resourceID := range resourceIDs {
		values = append(values, resourceID.String())
	}
	if principalID == nil || *principalID == uuid.Nil {
		values = append(values, "")
	} else {
		values = append(values, principalID.String())
	}
	values = append(values, strconv.FormatInt(expUnix, 10))
	for _, value := range values {
		mac.Write([]byte(value))
		mac.Write([]byte{'\n'})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func signSourceAccess(fileID, contentID, blockID uuid.UUID, purpose string, principalID *uuid.UUID, expUnix int64, secret string) string {
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	for _, value := range []string{
		fileID.String(),
		contentID.String(),
		blockID.String(),
		strings.TrimSpace(purpose),
		func() string {
			if principalID == nil || *principalID == uuid.Nil {
				return ""
			}
			return principalID.String()
		}(),
		strconv.FormatInt(expUnix, 10),
	} {
		mac.Write([]byte(value))
		mac.Write([]byte{'\n'})
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func signSourceAccessWithTenant(fileID, contentID, blockID uuid.UUID, purpose string, principalID, tenantID *uuid.UUID, expUnix int64, secret string) string {
	if tenantID == nil || *tenantID == uuid.Nil {
		return signSourceAccess(fileID, contentID, blockID, purpose, principalID, expUnix, secret)
	}
	mac := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	for _, value := range []string{
		fileID.String(),
		contentID.String(),
		func() string {
			if blockID == uuid.Nil {
				return ""
			}
			return blockID.String()
		}(),
		strings.TrimSpace(purpose),
		func() string {
			if principalID == nil || *principalID == uuid.Nil {
				return ""
			}
			return principalID.String()
		}(),
		func() string {
			if tenantID == nil || *tenantID == uuid.Nil {
				return ""
			}
			return tenantID.String()
		}(),
		strconv.FormatInt(expUnix, 10),
	} {
		mac.Write([]byte(value))
		mac.Write([]byte{'\n'})
	}
	return hex.EncodeToString(mac.Sum(nil))
}
