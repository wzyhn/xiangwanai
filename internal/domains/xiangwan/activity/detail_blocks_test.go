package activity

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeDetailBlocksTableDriven(t *testing.T) {
	t.Parallel()

	coverURL := "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.jpg"
	httpsURL := "https://cdn.example.com/detail/venue.jpg"
	tests := []struct {
		name   string
		blocks []DetailBlock
		want   []DetailBlock
		valid  bool
	}{
		{
			name:   "nil becomes explicit empty list",
			blocks: nil,
			want:   []DetailBlock{},
			valid:  true,
		},
		{
			name: "text and image blocks are trimmed",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: " 活动亮点 ", Body: " 三面环水 "},
				{Type: DetailBlockTypeImage, URL: coverURL + " ", Caption: " 现场 "},
			},
			want: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "活动亮点", Body: "三面环水"},
				{Type: DetailBlockTypeImage, URL: coverURL, Caption: "现场"},
			},
			valid: true,
		},
		{
			name: "absolute https image",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: httpsURL},
			},
			want: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: httpsURL},
			},
			valid: true,
		},
		{
			name: "unknown type rejected",
			blocks: []DetailBlock{
				{Type: "video", URL: httpsURL},
			},
			valid: false,
		},
		{
			name: "text without body rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "亮点"},
			},
			valid: false,
		},
		{
			name: "text with blank title rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "  ", Body: "正文"},
			},
			valid: false,
		},
		{
			name: "text carrying image fields rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "亮点", Body: "正文", URL: coverURL},
			},
			valid: false,
		},
		{
			name: "image without url rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, Caption: "现场"},
			},
			valid: false,
		},
		{
			name: "image with http url rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: "http://cdn.example.com/a.jpg"},
			},
			valid: false,
		},
		{
			name: "image with fragment rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: httpsURL + "#frag"},
			},
			valid: false,
		},
		{
			name: "image with unknown relative path rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: "/api/v1/xiangwan/media/a/b/c"},
			},
			valid: false,
		},
		{
			name: "image with traversal cover name rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: "/api/v1/xiangwan/covers/../secret.jpg"},
			},
			valid: false,
		},
		{
			name: "image carrying text fields rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: coverURL, Title: "不应出现"},
			},
			valid: false,
		},
		{
			name: "oversized title rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: strings.Repeat("长", 121), Body: "正文"},
			},
			valid: false,
		},
		{
			name: "oversized caption rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeImage, URL: coverURL, Caption: strings.Repeat("图", 201)},
			},
			valid: false,
		},
		{
			name: "control characters in title rejected",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "亮点\n换行", Body: "正文"},
			},
			valid: false,
		},
		{
			name: "multiline body accepted",
			blocks: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "亮点", Body: "第一段\n第二段"},
			},
			want: []DetailBlock{
				{Type: DetailBlockTypeText, Title: "亮点", Body: "第一段\n第二段"},
			},
			valid: true,
		},
		{
			name:   "too many blocks rejected",
			blocks: repeatDetailBlock(MaxInstanceDetailBlocks + 1),
			valid:  false,
		},
		{
			name:   "at the block cap accepted",
			blocks: repeatDetailBlock(MaxInstanceDetailBlocks),
			want:   repeatDetailBlock(MaxInstanceDetailBlocks),
			valid:  true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeDetailBlocks(test.blocks)
			if test.valid && err != nil {
				t.Fatalf("NormalizeDetailBlocks() error = %v", err)
			}
			if !test.valid && err == nil {
				t.Fatalf("NormalizeDetailBlocks() = %v, want rejection", got)
			}
			if test.valid && !detailBlocksEqual(got, test.want) {
				t.Fatalf("NormalizeDetailBlocks() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestProjectDetailBlocksDropsInvalidElements(t *testing.T) {
	t.Parallel()

	coverURL := "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.png"
	raw := `[
		{"type":"text","title":" 活动亮点 ","body":"三面环水"},
		{"type":"video","url":"https://cdn.example.com/v.mp4"},
		{"type":"text","title":"缺正文"},
		{"type":"image","url":"` + coverURL + `","caption":" 现场 "},
		{"type":"image","url":"javascript:alert(1)"},
		42,
		"plain string",
		null,
		{"type":"image","url":"` + coverURL + `","caption":"` + strings.Repeat("图", 201) + `"},
		["nested"]
	]`
	blocks, err := ProjectDetailBlocks([]byte(raw))
	if err != nil {
		t.Fatalf("ProjectDetailBlocks() error = %v", err)
	}
	want := []DetailBlock{
		{Type: DetailBlockTypeText, Title: "活动亮点", Body: "三面环水"},
		{Type: DetailBlockTypeImage, URL: coverURL, Caption: "现场"},
	}
	if !detailBlocksEqual(blocks, want) {
		t.Fatalf("ProjectDetailBlocks() = %+v, want %+v", blocks, want)
	}
}

func TestProjectDetailBlocksEmptyAndMalformed(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"empty bytes":      "",
		"empty array":      "[]",
		"empty with space": "  ",
	} {
		raw := raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			blocks, err := ProjectDetailBlocks([]byte(raw))
			if err != nil || len(blocks) != 0 || blocks == nil {
				t.Fatalf("ProjectDetailBlocks(%q) = %v, %v, want non-nil empty", raw, blocks, err)
			}
		})
	}
	for name, raw := range map[string]string{
		"not json":  "not-json",
		"object":    `{"type":"text"}`,
		"truncated": `[{"type":"text"`,
	} {
		raw := raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ProjectDetailBlocks([]byte(raw)); err == nil {
				t.Fatalf("ProjectDetailBlocks(%q) succeeded, want storage conflict", raw)
			}
		})
	}
}

func TestDetailBlockMarshalJSONEmitsOnlyContractKeys(t *testing.T) {
	t.Parallel()

	text, err := json.Marshal(DetailBlock{
		Type: DetailBlockTypeText, Title: "亮点", Body: "正文",
	})
	if err != nil {
		t.Fatalf("marshal text block: %v", err)
	}
	if string(text) != `{"type":"text","title":"亮点","body":"正文"}` {
		t.Fatalf("text block json = %s", text)
	}
	image, err := json.Marshal(DetailBlock{
		Type: DetailBlockTypeImage,
		URL:  "/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp",
	})
	if err != nil {
		t.Fatalf("marshal image block: %v", err)
	}
	if string(image) != `{"type":"image","url":"/api/v1/xiangwan/covers/0123456789abcdef0123456789abcdef.webp","caption":""}` {
		t.Fatalf("image block json = %s", image)
	}
	if _, err := json.Marshal(DetailBlock{Type: "video"}); err == nil {
		t.Fatal("unknown block type marshaled, want error")
	}
}

func TestProjectDetailBlocksCapsOversizedArray(t *testing.T) {
	t.Parallel()

	var builder strings.Builder
	builder.WriteString("[")
	for index := 0; index < MaxInstanceDetailBlocks+5; index++ {
		if index > 0 {
			builder.WriteString(",")
		}
		builder.WriteString(`{"type":"text","title":"亮点","body":"正文"}`)
	}
	builder.WriteString("]")
	blocks, err := ProjectDetailBlocks([]byte(builder.String()))
	if err != nil {
		t.Fatalf("ProjectDetailBlocks() error = %v", err)
	}
	if len(blocks) != MaxInstanceDetailBlocks {
		t.Fatalf("ProjectDetailBlocks() kept %d blocks, want %d", len(blocks), MaxInstanceDetailBlocks)
	}
}

func repeatDetailBlock(count int) []DetailBlock {
	blocks := make([]DetailBlock, 0, count)
	for index := 0; index < count; index++ {
		blocks = append(blocks, DetailBlock{
			Type: DetailBlockTypeText, Title: "亮点", Body: "正文",
		})
	}
	return blocks
}

func detailBlocksEqual(left, right []DetailBlock) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
