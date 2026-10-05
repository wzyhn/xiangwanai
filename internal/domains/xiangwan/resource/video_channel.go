package resource

import (
	"strings"
	"unicode"
)

// WeChat requires both IDs from Channels Assistant. A shared webpage URL is
// not sufficient and is never guessed or parsed into these identities.
type ReviewVideoChannel struct {
	FinderUserName string `json:"finder_user_name"`
	FeedID         string `json:"feed_id"`
}

func ValidReviewVideoChannel(value ReviewVideoChannel) bool {
	if !strings.HasPrefix(value.FinderUserName, "sph") || len(value.FinderUserName) <= 3 || len(value.FinderUserName) > 128 || len(value.FeedID) == 0 || len(value.FeedID) > 512 {
		return false
	}
	for _, value := range []string{value.FinderUserName, value.FeedID} {
		for _, char := range value {
			if char > unicode.MaxASCII || unicode.IsSpace(char) || unicode.IsControl(char) {
				return false
			}
		}
	}
	return true
}
