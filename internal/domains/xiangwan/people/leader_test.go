package people

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInstanceRoleLabelMatchesMiniProgramVocabulary(t *testing.T) {
	t.Parallel()

	// apps/miniprogram-xiangwan/features/benefits/model.js ROLE_LABELS
	want := map[InstanceRoleCode]string{
		InstanceRoleHost:             "主理人",
		InstanceRoleInvitedGuest:     "特邀嘉宾",
		InstanceRoleCourseInstructor: "课程讲师",
		InstanceRoleEventSpeaker:     "活动分享者",
	}
	for code, label := range want {
		if got := InstanceRoleLabel(code); got != label {
			t.Fatalf("InstanceRoleLabel(%q) = %q, want %q", code, got, label)
		}
	}
	if got := InstanceRoleLabel("leader"); got != "" {
		t.Fatalf("InstanceRoleLabel(unknown) = %q, want empty", got)
	}
}

func TestProjectSessionLeadersOrdersByRoleThenGrantTime(t *testing.T) {
	t.Parallel()

	granted := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	facts := []SessionLeaderFacts{
		leaderFacts(4, InstanceRoleEventSpeaker, "分享者", granted.Add(2*time.Hour)),
		leaderFacts(2, InstanceRoleInvitedGuest, "嘉宾", granted.Add(time.Hour)),
		leaderFacts(1, InstanceRoleHost, "主理", granted),
		leaderFacts(3, InstanceRoleCourseInstructor, "讲师", granted.Add(3*time.Hour)),
		leaderFacts(5, InstanceRoleInvitedGuest, "早嘉宾", granted.Add(30*time.Minute)),
	}
	leaders := ProjectSessionLeaders(facts)
	if len(leaders) != 5 {
		t.Fatalf("ProjectSessionLeaders() kept %d, want 5", len(leaders))
	}
	wantOrder := []struct {
		code  InstanceRoleCode
		label string
		name  string
	}{
		{InstanceRoleHost, "主理人", "主理"},
		{InstanceRoleInvitedGuest, "特邀嘉宾", "早嘉宾"},
		{InstanceRoleInvitedGuest, "特邀嘉宾", "嘉宾"},
		{InstanceRoleCourseInstructor, "课程讲师", "讲师"},
		{InstanceRoleEventSpeaker, "活动分享者", "分享者"},
	}
	for index, want := range wantOrder {
		leader := leaders[index]
		if leader.RoleCode != want.code || leader.RoleLabel != want.label ||
			leader.DisplayName != want.name {
			t.Fatalf("leaders[%d] = %+v, want %s/%s/%s", index, leader, want.code, want.label, want.name)
		}
	}
}

func TestProjectSessionLeadersDropsInvalidFacts(t *testing.T) {
	t.Parallel()

	granted := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	valid := leaderFacts(1, InstanceRoleHost, " 主理 ", granted)
	valid.Headline = " 行家 "
	valid.AvatarURL = " https://cdn.example.com/avatar.jpg "
	facts := []SessionLeaderFacts{
		valid,
		leaderFacts(2, "leader", "未知角色", granted),
		leaderFacts(3, InstanceRoleHost, "", granted),
		leaderFacts(4, InstanceRoleHost, "重名", time.Time{}),
		{BindingID: uuid.Nil, RoleCode: InstanceRoleHost, DisplayName: "无身份", GrantedAt: granted},
		leaderFacts(1, InstanceRoleHost, "重复绑定", granted),
	}
	leaders := ProjectSessionLeaders(facts)
	if len(leaders) != 1 {
		t.Fatalf("ProjectSessionLeaders() = %+v, want only the valid host", leaders)
	}
	leader := leaders[0]
	if leader.DisplayName != "主理" || leader.Headline != "行家" ||
		leader.AvatarURL != "https://cdn.example.com/avatar.jpg" ||
		leader.RoleLabel != "主理人" {
		t.Fatalf("leader = %+v, want trimmed valid host", leader)
	}
}

func TestProjectSessionLeadersCapsAtProductLimit(t *testing.T) {
	t.Parallel()

	granted := time.Date(2026, time.September, 1, 8, 0, 0, 0, time.UTC)
	facts := make([]SessionLeaderFacts, 0, 11)
	for index := 0; index < 11; index++ {
		facts = append(facts, leaderFacts(
			byte(index+1),
			InstanceRoleHost,
			"主理"+string(rune('A'+index)),
			granted.Add(time.Duration(index)*time.Minute),
		))
	}
	leaders := ProjectSessionLeaders(facts)
	if len(leaders) != MaxSessionLeaders {
		t.Fatalf("ProjectSessionLeaders() kept %d, want the cap %d", len(leaders), MaxSessionLeaders)
	}
	// Grant-time order is preserved up to the cap.
	if leaders[0].DisplayName != "主理A" || leaders[MaxSessionLeaders-1].DisplayName != "主理H" {
		t.Fatalf("capped leaders = %+v", leaders)
	}
}

func TestProjectSessionLeadersEmptyInput(t *testing.T) {
	t.Parallel()

	if leaders := ProjectSessionLeaders(nil); leaders == nil || len(leaders) != 0 {
		t.Fatalf("ProjectSessionLeaders(nil) = %+v, want non-nil empty", leaders)
	}
}

func leaderFacts(
	seed byte,
	code InstanceRoleCode,
	name string,
	granted time.Time,
) SessionLeaderFacts {
	return SessionLeaderFacts{
		BindingID:   uuid.UUID{seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed, seed},
		RoleCode:    code,
		DisplayName: name,
		GrantedAt:   granted,
	}
}
