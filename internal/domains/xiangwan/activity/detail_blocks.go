package activity

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type DetailBlockType string

const (
	DetailBlockTypeText  DetailBlockType = "text"
	DetailBlockTypeImage DetailBlockType = "image"
)

const (
	// MaxInstanceDetailBlocks caps the per-Instance content blocks so one
	// public detail page stays bounded.
	MaxInstanceDetailBlocks    = 20
	maxDetailBlockTitleRunes   = 120
	maxDetailBlockBodyRunes    = 4000
	maxDetailBlockCaptionRunes = 200
)

var ErrInvalidDetailBlocks = errors.New("invalid xiangwan Instance detail blocks")

// detailBlockCoverPathPattern pins the relative image URL shape to the public
// cover route the runtime actually serves (CoverImagePath in the api package):
// one server-generated 32-hex filename with an accepted extension.
var detailBlockCoverPathPattern = regexp.MustCompile(
	`^/api/v1/xiangwan/covers/[0-9a-f]{32}\.(jpg|png|webp)$`,
)

// DetailBlock is one public content block of an Instance detail page. Text
// blocks carry exactly {type,title,body}; image blocks carry exactly
// {type,url,caption} with caption possibly empty. MarshalJSON emits only the
// keys of the block's own type so the wire contract never sprouts empty
// foreign fields.
type DetailBlock struct {
	Type    DetailBlockType `json:"type"`
	Title   string          `json:"title,omitempty"`
	Body    string          `json:"body,omitempty"`
	URL     string          `json:"url,omitempty"`
	Caption string          `json:"caption,omitempty"`
}

func (block DetailBlock) MarshalJSON() ([]byte, error) {
	switch block.Type {
	case DetailBlockTypeText:
		return json.Marshal(struct {
			Type  DetailBlockType `json:"type"`
			Title string          `json:"title"`
			Body  string          `json:"body"`
		}{Type: block.Type, Title: block.Title, Body: block.Body})
	case DetailBlockTypeImage:
		return json.Marshal(struct {
			Type    DetailBlockType `json:"type"`
			URL     string          `json:"url"`
			Caption string          `json:"caption"`
		}{Type: block.Type, URL: block.URL, Caption: block.Caption})
	default:
		return nil, fmt.Errorf("%w: unknown block type %q", ErrInvalidDetailBlocks, block.Type)
	}
}

// ValidDetailBlock reports whether one block is complete and well-formed. The
// value must already be normalized (trimmed); use NormalizeDetailBlocks on
// untrusted input.
func ValidDetailBlock(block DetailBlock) bool {
	switch block.Type {
	case DetailBlockTypeText:
		return validDetailBlockText(block.Title, maxDetailBlockTitleRunes) &&
			validDetailBlockBody(block.Body) &&
			block.URL == "" && block.Caption == ""
	case DetailBlockTypeImage:
		return ValidDetailBlockImageURL(block.URL) &&
			validDetailBlockOptionalText(block.Caption, maxDetailBlockCaptionRunes) &&
			block.Title == "" && block.Body == ""
	default:
		return false
	}
}

// ValidDetailBlockImageURL accepts the public relative cover path the runtime
// serves or an absolute https URL without a fragment, mirroring the 783/785
// image URL contract.
func ValidDetailBlockImageURL(value string) bool {
	return detailBlockCoverPathPattern.MatchString(value) || validHTTPSHeroURL(value)
}

// NormalizeDetailBlocks trims every text field and validates the whole list
// for an administrator write: nil becomes an explicit empty list, and any
// invalid element rejects the entire command.
func NormalizeDetailBlocks(blocks []DetailBlock) ([]DetailBlock, error) {
	normalized := make([]DetailBlock, 0, len(blocks))
	for _, block := range blocks {
		block.Title = strings.TrimSpace(block.Title)
		block.Body = strings.TrimSpace(block.Body)
		block.URL = strings.TrimSpace(block.URL)
		block.Caption = strings.TrimSpace(block.Caption)
		if !ValidDetailBlock(block) {
			return nil, ErrInvalidDetailBlocks
		}
		normalized = append(normalized, block)
	}
	if len(normalized) > MaxInstanceDetailBlocks {
		return nil, ErrInvalidDetailBlocks
	}
	return normalized, nil
}

// ProjectDetailBlocks decodes the stored JSONB array and keeps only the
// elements that normalize to a valid block; invalid elements are dropped so a
// single malformed entry never takes the public detail page down. A
// top-level value that is not a JSON array is a storage conflict the 786
// CHECK constraint makes impossible, so it is an error, not an empty list.
func ProjectDetailBlocks(raw []byte) ([]DetailBlock, error) {
	blocks := make([]DetailBlock, 0)
	if len(strings.TrimSpace(string(raw))) == 0 {
		return blocks, nil
	}
	var elements []json.RawMessage
	if err := json.Unmarshal(raw, &elements); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDetailBlocks, err)
	}
	if len(elements) > MaxInstanceDetailBlocks {
		elements = elements[:MaxInstanceDetailBlocks]
	}
	for _, element := range elements {
		var block DetailBlock
		if err := json.Unmarshal(element, &block); err != nil {
			continue
		}
		normalized, err := NormalizeDetailBlocks([]DetailBlock{block})
		if err != nil {
			continue
		}
		blocks = append(blocks, normalized[0])
	}
	return blocks, nil
}

func validDetailBlockText(value string, maxRunes int) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len([]rune(value)) <= maxRunes && !containsControl(value)
}

// validDetailBlockBody mirrors the BrandIntro rule: long-form text may carry
// newlines, only trimming and the rune cap apply.
func validDetailBlockBody(value string) bool {
	return value != "" && value == strings.TrimSpace(value) &&
		len([]rune(value)) <= maxDetailBlockBodyRunes
}

func validDetailBlockOptionalText(value string, maxRunes int) bool {
	return value == strings.TrimSpace(value) &&
		len([]rune(value)) <= maxRunes && !containsControl(value)
}
