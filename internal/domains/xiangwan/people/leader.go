package people

import (
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	maxLeaderDisplayNameRunes = 120
	maxLeaderHeadlineRunes    = 200
	maxLeaderAvatarURLRunes   = 2048
	// MaxSessionLeaders is the product cap on the public Session detail
	// leader stack. The SQL pre-pass fetches one extra row so invalid or
	// duplicate bindings cannot starve the projection.
	MaxSessionLeaders = 8
)

// InstanceRoleLabel is the public Chinese label of one Instance role. It must
// stay in sync with the Mini Program vocabulary
// (apps/miniprogram-xiangwan/features/benefits/model.js ROLE_LABELS); the
// server emits it so the public Session detail never re-derives wording.
func InstanceRoleLabel(code InstanceRoleCode) string {
	switch code {
	case InstanceRoleHost:
		return "主理人"
	case InstanceRoleInvitedGuest:
		return "特邀嘉宾"
	case InstanceRoleCourseInstructor:
		return "课程讲师"
	case InstanceRoleEventSpeaker:
		return "活动分享者"
	default:
		return ""
	}
}

// instanceRoleDisplayOrder pins the public leader ordering: host first, then
// guests, instructors, and speakers.
func instanceRoleDisplayOrder(code InstanceRoleCode) int {
	switch code {
	case InstanceRoleHost:
		return 0
	case InstanceRoleInvitedGuest:
		return 1
	case InstanceRoleCourseInstructor:
		return 2
	case InstanceRoleEventSpeaker:
		return 3
	default:
		return -1
	}
}

// SessionLeaderFacts is one active Instance role binding joined to its
// trusted, published PeopleProfile and the Principal avatar ("" when the
// account has none).
type SessionLeaderFacts struct {
	BindingID   uuid.UUID
	RoleCode    InstanceRoleCode
	DisplayName string
	Headline    string
	AvatarURL   string
	GrantedAt   time.Time
}

// SessionLeader is the public projection of one Instance leader.
type SessionLeader struct {
	RoleCode    InstanceRoleCode
	RoleLabel   string
	DisplayName string
	Headline    string
	AvatarURL   string
}

// ProjectSessionLeaders keeps only well-formed leader facts and orders them
// by role display order, grant time, then binding identity, so the public
// page is stable. Invalid elements are dropped rather than failing the read,
// and the result is capped at MaxSessionLeaders.
func ProjectSessionLeaders(facts []SessionLeaderFacts) []SessionLeader {
	ordered := make([]SessionLeaderFacts, 0, len(facts))
	seen := make(map[uuid.UUID]struct{}, len(facts))
	for _, fact := range facts {
		fact.DisplayName = strings.TrimSpace(fact.DisplayName)
		fact.Headline = strings.TrimSpace(fact.Headline)
		fact.AvatarURL = strings.TrimSpace(fact.AvatarURL)
		if fact.BindingID == uuid.Nil ||
			InstanceRoleLabel(fact.RoleCode) == "" ||
			fact.DisplayName == "" || fact.GrantedAt.IsZero() ||
			len([]rune(fact.DisplayName)) > maxLeaderDisplayNameRunes ||
			len([]rune(fact.Headline)) > maxLeaderHeadlineRunes ||
			len(fact.AvatarURL) > maxLeaderAvatarURLRunes {
			continue
		}
		if _, duplicate := seen[fact.BindingID]; duplicate {
			continue
		}
		seen[fact.BindingID] = struct{}{}
		ordered = append(ordered, fact)
	}
	sort.SliceStable(ordered, func(left, right int) bool {
		leftOrder := instanceRoleDisplayOrder(ordered[left].RoleCode)
		rightOrder := instanceRoleDisplayOrder(ordered[right].RoleCode)
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		if !ordered[left].GrantedAt.Equal(ordered[right].GrantedAt) {
			return ordered[left].GrantedAt.Before(ordered[right].GrantedAt)
		}
		return ordered[left].BindingID.String() < ordered[right].BindingID.String()
	})
	leaders := make([]SessionLeader, 0, min(len(ordered), MaxSessionLeaders))
	for _, fact := range ordered {
		if len(leaders) == MaxSessionLeaders {
			break
		}
		leaders = append(leaders, SessionLeader{
			RoleCode:    fact.RoleCode,
			RoleLabel:   InstanceRoleLabel(fact.RoleCode),
			DisplayName: fact.DisplayName,
			Headline:    fact.Headline,
			AvatarURL:   fact.AvatarURL,
		})
	}
	return leaders
}
