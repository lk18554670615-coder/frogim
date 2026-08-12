package wukong

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPatchedServerCommunityTopicAndMixedSnapshot(t *testing.T) {
	apiURL := strings.TrimSpace(os.Getenv("IM_TEST_WUKONG_PATCH_URL"))
	managerToken := os.Getenv("IM_TEST_WUKONG_MANAGER_TOKEN")
	if apiURL == "" || managerToken == "" {
		t.Skip("IM_TEST_WUKONG_PATCH_URL and IM_TEST_WUKONG_MANAGER_TOKEN are required")
	}
	client, err := NewClient(Config{APIURL: apiURL, ManagerURL: apiURL, ManagerToken: managerToken, MaxRetries: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	alice := "patch_alice_" + suffix
	bob := "patch_bob_" + suffix
	for _, user := range []UserTokenRequest{
		{UID: alice, Token: "token_" + alice, DeviceFlag: DeviceApp, DeviceLevel: 1},
		{UID: bob, Token: "token_" + bob, DeviceFlag: DeviceApp, DeviceLevel: 1},
	} {
		if err = client.ProvisionUser(ctx, user); err != nil {
			t.Fatalf("provision %s: %v", user.UID, err)
		}
	}

	communityTopicID := "community_" + suffix + "@topic_" + suffix
	if err = client.UpsertChannel(ctx, ChannelRequest{ChannelID: communityTopicID, ChannelType: ChannelCommunityTopic, Reset: 1, Subscribers: []string{alice, bob}}); err != nil {
		t.Fatalf("create type-5 channel: %v", err)
	}
	if err = client.SetSubscribers(ctx, communityTopicID, ChannelCommunityTopic, []string{alice, bob}); err != nil {
		t.Fatalf("set type-5 subscribers: %v", err)
	}
	if err = client.AddAllowlist(ctx, communityTopicID, ChannelCommunityTopic, []string{alice}); err != nil {
		t.Fatalf("add type-5 allowlist: %v", err)
	}

	streamChannelID := "patch_stream_" + suffix
	if err = client.UpsertChannel(ctx, ChannelRequest{ChannelID: streamChannelID, ChannelType: ChannelGroup, Reset: 1, Subscribers: []string{alice, bob}}); err != nil {
		t.Fatalf("create stream channel: %v", err)
	}
	clientMsgNo := "patch_stream_message_" + suffix
	anchor, err := client.SendStreamMessage(ctx, StoredMessageRequest{
		ClientMsgNo: clientMsgNo, FromUID: alice, ChannelID: streamChannelID, ChannelType: ChannelGroup,
		Payload: map[string]any{"type": ContentTypeText, "content": "stream placeholder"},
	})
	if err != nil {
		t.Fatalf("send stream anchor: %v", err)
	}
	events := []MessageEventRequest{
		{EventID: "patch_delta_before_" + suffix, EventType: MessageEventStreamDelta, Payload: map[string]any{"kind": "text", "delta": "old"}},
		{EventID: "patch_snapshot_" + suffix, EventType: MessageEventStreamSnapshot, Payload: map[string]any{"kind": "text", "text": "replacement"}},
		{EventID: "patch_delta_after_" + suffix, EventType: MessageEventStreamDelta, Payload: map[string]any{"kind": "text", "delta": " after"}},
		{EventID: "patch_close_" + suffix, EventType: MessageEventStreamClose, Payload: map[string]any{"end_reason": 0}},
	}
	for index := range events {
		events[index].ChannelID = streamChannelID
		events[index].ChannelType = ChannelGroup
		events[index].FromUID = alice
		events[index].MessageID = anchor.MessageID
		events[index].ClientMsgNo = clientMsgNo
		events[index].EventKey = "main"
		events[index].Visibility = "public"
		events[index].OccurredAt = time.Now().UnixMilli()
		if _, err = client.AppendMessageEvent(ctx, events[index]); err != nil {
			t.Fatalf("append %s: %v", events[index].EventType, err)
		}
	}

	synced, err := client.SyncMessageEvents(ctx, MessageEventSyncRequest{
		ChannelID: streamChannelID, ChannelType: ChannelGroup, FromUID: alice,
		ClientMsgNo: clientMsgNo, EventKey: "main", Limit: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range synced.Events {
		if event.EventType != MessageEventStreamClose {
			continue
		}
		snapshot := event.Payload
		if nested, ok := event.Payload["snapshot"].(map[string]any); ok {
			snapshot = nested
		}
		if snapshot["kind"] != "text" || snapshot["text"] != "replacement after" {
			t.Fatalf("terminal snapshot=%#v payload=%#v", snapshot, event.Payload)
		}
		return
	}
	t.Fatalf("stream.close not returned: %#v", synced.Events)
}
