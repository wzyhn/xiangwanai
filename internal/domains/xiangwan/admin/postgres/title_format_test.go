package postgres

import "testing"

func TestFormatInstanceTitleUsesPrototypeIssueFormat(t *testing.T) {
	for _, test := range []struct {
		issueNo     int
		seriesTitle string
		want        string
	}{
		{issueNo: 1, seriesTitle: "天津 AI 圆桌派", want: "第1期天津 AI 圆桌派"},
		{issueNo: 13, seriesTitle: " AI 共创夜 ", want: "第13期AI 共创夜"},
	} {
		if got := formatInstanceTitle(test.issueNo, test.seriesTitle); got != test.want {
			t.Fatalf("formatInstanceTitle(%d, %q) = %q, want %q", test.issueNo, test.seriesTitle, got, test.want)
		}
	}
}
