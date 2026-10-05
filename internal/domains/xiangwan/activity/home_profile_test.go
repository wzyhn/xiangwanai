package activity

import (
	"errors"
	"testing"
	"time"
)

func TestValidatePublicHomeProfile(t *testing.T) {
	t.Parallel()

	valid := PublicHomeProfile{
		LifecycleStatus:    BrandLifecycleActive,
		PublicationVersion: 3,
		CommunityName:      "Xiangwan Tianjin",
		BrandIntro:         "Meet people through thoughtful activities.",
		HeroMode:           HomeHeroModeText,
		HeroEyebrow:        "TIANJIN AI COMMUNITY",
		AvailableQuickTags: []HomeQuickTag{{Code: "ai", Label: "AI"}},
		PublishedAt:        time.Now().UTC(),
	}
	if err := ValidatePublicHomeProfile(valid); err != nil {
		t.Fatalf("ValidatePublicHomeProfile(valid) error = %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*PublicHomeProfile)
	}{
		{name: "draft", mutate: func(value *PublicHomeProfile) { value.LifecycleStatus = "draft" }},
		{name: "version", mutate: func(value *PublicHomeProfile) { value.PublicationVersion = 0 }},
		{name: "name", mutate: func(value *PublicHomeProfile) { value.CommunityName = " Xiangwan" }},
		{name: "intro whitespace", mutate: func(value *PublicHomeProfile) { value.BrandIntro = " " }},
		{name: "tag", mutate: func(value *PublicHomeProfile) {
			value.AvailableQuickTags = []HomeQuickTag{{Code: "Not Valid", Label: "AI"}}
		}},
		{name: "published at", mutate: func(value *PublicHomeProfile) { value.PublishedAt = time.Time{} }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := valid
			test.mutate(&value)
			if err := ValidatePublicHomeProfile(value); !errors.Is(err, ErrInvalidPublicHomeProfile) {
				t.Fatalf("ValidatePublicHomeProfile() error = %v", err)
			}
		})
	}
}

func TestValidatePublicHomeProfileImageHero(t *testing.T) {
	t.Parallel()

	valid := PublicHomeProfile{
		LifecycleStatus: BrandLifecycleActive, PublicationVersion: 1,
		CommunityName: "享玩 AI", BrandIntro: "在线下产生真实连接。",
		HeroMode: HomeHeroModeImage, HeroEyebrow: "天津 AI 共创社区",
		HeroSubtitle: "线下深度交流", HeroImageURL: "https://assets.example.com/hero.jpg",
		HeroImageAlt: "社区成员在活动现场交流", AvailableQuickTags: []HomeQuickTag{},
		PublishedAt: time.Now().UTC(),
	}
	if err := ValidatePublicHomeProfile(valid); err != nil {
		t.Fatalf("ValidatePublicHomeProfile(image) error = %v", err)
	}
	for _, imageURL := range []string{
		"http://assets.example.com/hero.jpg",
		"https://user:pass@assets.example.com/hero.jpg",
		"https://assets.example.com/hero.jpg#fragment",
	} {
		value := valid
		value.HeroImageURL = imageURL
		if err := ValidatePublicHomeProfile(value); !errors.Is(err, ErrInvalidPublicHomeProfile) {
			t.Fatalf("ValidatePublicHomeProfile(%q) error = %v", imageURL, err)
		}
	}
}

func TestValidatePublicHomeProfileAllowsOptionalBrandCopy(t *testing.T) {
	t.Parallel()

	profile := PublicHomeProfile{
		LifecycleStatus:    BrandLifecycleActive,
		PublicationVersion: 2,
		HeroMode:           HomeHeroModeText,
		AvailableQuickTags: []HomeQuickTag{},
		PublishedAt:        time.Now().UTC(),
	}
	if err := ValidatePublicHomeProfile(profile); err != nil {
		t.Fatalf("ValidatePublicHomeProfile(empty optional copy) error = %v", err)
	}
	profile.HeroMode = HomeHeroModeImage
	if err := ValidatePublicHomeProfile(profile); err != nil {
		t.Fatalf("ValidatePublicHomeProfile(empty image mode) error = %v", err)
	}
	profile.HeroImageURL = "https://assets.example.com/hero.jpg"
	if err := ValidatePublicHomeProfile(profile); err != nil {
		t.Fatalf("ValidatePublicHomeProfile(image without optional alt) error = %v", err)
	}
}
