package store

import (
	"testing"

	"github.com/linli/im/server/internal/wukong"
)

func TestWukongPushMessageTypeCoversSupportedContent(t *testing.T) {
	tests := []struct {
		contentType int
		want        string
	}{
		{wukong.ContentTypeText, "text"}, {wukong.ContentTypeImage, "image"}, {wukong.ContentTypeGIF, "image"},
		{wukong.ContentTypeVoice, "audio"}, {wukong.ContentTypeVideo, "video"}, {wukong.ContentTypeLocation, "location"},
		{wukong.ContentTypeCard, "contact"}, {wukong.ContentTypeFile, "file"}, {wukong.ContentTypeMergedHistory, "chat_history"},
		{wukong.ContentTypeSystemEvent, "system"}, {wukong.ContentTypeStoreSticker, "sticker"}, {wukong.ContentTypeMomentShare, "moment"},
		{wukong.ContentTypeCallEvent, "call"}, {wukong.ContentTypeLiveEvent, "live"}, {wukong.ContentTypeSupportEvent, "support"},
		{wukong.ContentTypeScreenshot, "screenshot"},
	}
	for _, test := range tests {
		if got := wukongPushMessageType(test.contentType); got != test.want {
			t.Fatalf("contentType=%d got=%q want=%q", test.contentType, got, test.want)
		}
	}
}
