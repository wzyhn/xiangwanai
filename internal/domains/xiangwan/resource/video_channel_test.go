package resource

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNativeVideoChannelProjectionDoesNotRequireExternalWebDomain(t *testing.T) {
	channel := ReviewVideoChannel{FinderUserName: "sphExample", FeedID: "900719925474099312345"}
	blocks := projectPublicReviewBlocks([]PublicReviewBlockFacts{{BlockID: uuid.New(), Type: PublicReviewBlockTypeLink, VideoChannel: &channel}}, ExternalDomainPolicy{})
	if len(blocks) != 1 || blocks[0].Availability != PublicReviewBlockAvailable || blocks[0].ExternalURL != "" || blocks[0].VideoChannel.FeedID != channel.FeedID {
		t.Fatalf("native projection: %+v", blocks)
	}
	for _, invalid := range []ReviewVideoChannel{{FinderUserName: "sph", FeedID: "1"}, {FinderUserName: "example", FeedID: "1"}, {FinderUserName: "sphExample", FeedID: ""}, {FinderUserName: "sphExample", FeedID: "id\nother"}, {FinderUserName: "sphExample", FeedID: strings.Repeat("a", 513)}} {
		if ValidReviewVideoChannel(invalid) {
			t.Fatalf("accepted malformed video identity: %+v", invalid)
		}
	}
	if validPublicReviewBlockFacts(PublicReviewBlockFacts{BlockID: uuid.New(), Type: PublicReviewBlockTypeLink, VideoChannel: &channel, ExternalURL: "https://feishu.cn/docx/1"}) {
		t.Fatal("mixed native and webpage destinations accepted")
	}
	if validPublicReviewBlockFacts(PublicReviewBlockFacts{BlockID: uuid.New(), Type: PublicReviewBlockTypeText, Text: "text", VideoChannel: &channel}) {
		t.Fatal("native identity accepted on text")
	}
}
