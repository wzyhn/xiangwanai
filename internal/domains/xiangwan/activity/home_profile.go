package activity

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode"
)

type BrandLifecycleStatus string
type HomeHeroMode string

const (
	BrandLifecycleActive    BrandLifecycleStatus = "active"
	BrandLifecycleSuspended BrandLifecycleStatus = "suspended"
	HomeHeroModeText        HomeHeroMode         = "text"
	HomeHeroModeImage       HomeHeroMode         = "image"
)

var (
	ErrPublicHomeProfileUnavailable = errors.New(
		"xiangwan public home profile unavailable",
	)
	ErrInvalidPublicHomeProfile = errors.New(
		"invalid xiangwan public home profile",
	)
)

// PublicHomeProfile is the exact approved BrandProfile snapshot selected by
// the tenant aggregate. Draft content never crosses this read boundary.
type PublicHomeProfile struct {
	LifecycleStatus    BrandLifecycleStatus
	PublicationVersion int64
	CommunityName      string
	BrandIntro         string
	HeroMode           HomeHeroMode
	HeroEyebrow        string
	HeroSubtitle       string
	HeroImageURL       string
	HeroImageAlt       string
	AvailableQuickTags []HomeQuickTag
	PublishedAt        time.Time
}

func ValidatePublicHomeProfile(profile PublicHomeProfile) error {
	switch profile.LifecycleStatus {
	case BrandLifecycleActive, BrandLifecycleSuspended:
	default:
		return fmt.Errorf("%w: lifecycle status", ErrInvalidPublicHomeProfile)
	}
	if profile.PublicationVersion < 1 {
		return fmt.Errorf("%w: publication version", ErrInvalidPublicHomeProfile)
	}
	if profile.CommunityName != strings.TrimSpace(profile.CommunityName) ||
		len([]rune(profile.CommunityName)) > 100 {
		return fmt.Errorf("%w: community name", ErrInvalidPublicHomeProfile)
	}
	if profile.BrandIntro != strings.TrimSpace(profile.BrandIntro) ||
		len([]rune(profile.BrandIntro)) > 2000 {
		return fmt.Errorf("%w: brand intro", ErrInvalidPublicHomeProfile)
	}
	if profile.HeroEyebrow != strings.TrimSpace(profile.HeroEyebrow) ||
		len([]rune(profile.HeroEyebrow)) > 100 ||
		containsControl(profile.HeroEyebrow) {
		return fmt.Errorf("%w: hero eyebrow", ErrInvalidPublicHomeProfile)
	}
	if profile.HeroSubtitle != strings.TrimSpace(profile.HeroSubtitle) ||
		len([]rune(profile.HeroSubtitle)) > 200 ||
		containsControl(profile.HeroSubtitle) {
		return fmt.Errorf("%w: hero subtitle", ErrInvalidPublicHomeProfile)
	}
	switch profile.HeroMode {
	case HomeHeroModeText:
		if profile.HeroImageURL != "" || profile.HeroImageAlt != "" {
			return fmt.Errorf("%w: text hero image", ErrInvalidPublicHomeProfile)
		}
	case HomeHeroModeImage:
		if (profile.HeroImageURL != "" && !validHTTPSHeroURL(profile.HeroImageURL)) ||
			profile.HeroImageAlt != strings.TrimSpace(profile.HeroImageAlt) ||
			len([]rune(profile.HeroImageAlt)) > 200 ||
			containsControl(profile.HeroImageAlt) {
			return fmt.Errorf("%w: image hero", ErrInvalidPublicHomeProfile)
		}
	default:
		return fmt.Errorf("%w: hero mode", ErrInvalidPublicHomeProfile)
	}
	if profile.PublishedAt.IsZero() {
		return fmt.Errorf("%w: published at", ErrInvalidPublicHomeProfile)
	}
	if len(profile.AvailableQuickTags) > 20 {
		return fmt.Errorf("%w: too many quick tags", ErrInvalidPublicHomeProfile)
	}
	for _, tag := range profile.AvailableQuickTags {
		if !homeTagCodePattern.MatchString(tag.Code) ||
			tag.Label != strings.TrimSpace(tag.Label) ||
			len([]rune(tag.Label)) < 1 || len([]rune(tag.Label)) > 40 ||
			containsControl(tag.Label) {
			return fmt.Errorf("%w: quick tag", ErrInvalidPublicHomeProfile)
		}
	}
	if _, _, err := normalizeHomeFilter(
		HomeFilter{},
		profile.AvailableQuickTags,
	); err != nil {
		return fmt.Errorf("%w: quick tags: %v", ErrInvalidPublicHomeProfile, err)
	}
	return nil
}

func validHTTPSHeroURL(value string) bool {
	if len(value) < 9 || len(value) > 2048 || value != strings.TrimSpace(value) ||
		containsControl(value) || strings.Contains(value, "#") {
		return false
	}
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" &&
		parsed.User == nil && parsed.Fragment == ""
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}
