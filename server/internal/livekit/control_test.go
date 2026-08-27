package livekit

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	lkauth "github.com/livekit/protocol/auth"
	lkproto "github.com/livekit/protocol/livekit"
	"google.golang.org/protobuf/proto"
)

const testSecret = "livekit-test-secret-at-least-thirty-two-bytes"

func TestControlCreatesDeletesRoomAndIssuesLeastPrivilegeToken(t *testing.T) {
	var create *lkproto.CreateRoomRequest
	deleted := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifyControlGrant(t, r, r.URL.Path)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/protobuf")
		switch r.URL.Path {
		case "/twirp/livekit.RoomService/CreateRoom":
			create = &lkproto.CreateRoomRequest{}
			if err := proto.Unmarshal(body, create); err != nil {
				t.Fatal(err)
			}
			encoded, _ := proto.Marshal(&lkproto.Room{Name: create.Name})
			_, _ = w.Write(encoded)
		case "/twirp/livekit.RoomService/DeleteRoom":
			request := &lkproto.DeleteRoomRequest{}
			if err := proto.Unmarshal(body, request); err != nil {
				t.Fatal(err)
			}
			deleted = request.Room
			encoded, _ := proto.Marshal(&lkproto.DeleteRoomResponse{})
			_, _ = w.Write(encoded)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	control, err := NewControl(Config{
		URL: "wss://chat.example.test/rtc", APIURL: server.URL,
		APIKey: "devkey", APISecret: testSecret, TokenTTL: 2 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = control.EnsureCallRoom(context.Background(), "abc-1", "conversation-1", "video"); err != nil {
		t.Fatal(err)
	}
	if create == nil || create.Name != "call_abc-1" || create.MaxParticipants != 9 || create.EmptyTimeout != 60 || create.DepartureTimeout != 20 || !strings.Contains(create.Metadata, `"schemaVersion":"1"`) {
		t.Fatalf("create request=%+v", create)
	}
	session, err := control.IssueParticipant("abc-1", "user-1", "conversation-1", "video")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := lkauth.ParseAPIToken(session.Token)
	if err != nil {
		t.Fatal(err)
	}
	claims, grants, err := verifier.Verify(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if verifier.Identity() != "user-1" || grants.Video == nil || !grants.Video.RoomJoin || grants.Video.Room != "call_abc-1" || !grants.Video.GetCanPublish() || !grants.Video.GetCanSubscribe() || grants.Video.GetCanPublishData() {
		t.Fatalf("identity=%q grants=%+v", verifier.Identity(), grants.Video)
	}
	if claims.ExpiresAt == nil || time.Until(claims.ExpiresAt.Time) > 2*time.Minute+time.Second || session.RoomName != "call_abc-1" || session.URL != "wss://chat.example.test/rtc" {
		t.Fatalf("claims=%+v session=%+v", claims, session)
	}
	if err = control.DeleteCallRoom(context.Background(), "abc-1"); err != nil {
		t.Fatal(err)
	}
	if deleted != "call_abc-1" {
		t.Fatalf("deleted=%q", deleted)
	}
}

func verifyControlGrant(t *testing.T, r *http.Request, path string) {
	t.Helper()
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	verifier, err := lkauth.ParseAPIToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, grants, err := verifier.Verify(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if grants.Video == nil || !grants.Video.RoomCreate {
		t.Fatalf("path=%s grants=%+v", path, grants.Video)
	}
}

func TestNewControlRejectsUnsafeConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{},
		{URL: "https://wrong", APIURL: "http://livekit", APIKey: "key", APISecret: testSecret},
		{URL: "ws://livekit", APIURL: "tcp://wrong", APIKey: "key", APISecret: testSecret},
		{URL: "ws://livekit", APIURL: "http://livekit", APIKey: "key", APISecret: "short"},
		{URL: "ws://livekit", APIURL: "http://livekit", APIKey: "key", APISecret: testSecret, TokenTTL: 30 * time.Second},
		{URL: "ws://livekit", APIURL: "http://livekit", APIKey: "key", APISecret: testSecret, PrometheusURL: "file:///metrics"},
	} {
		if _, err := NewControl(cfg); err == nil {
			t.Fatalf("expected rejection for %+v", cfg)
		}
	}
}

func TestControlReadsLiveKitResourceMetricsFromPrometheus(t *testing.T) {
	values := map[string]string{
		`max(up{job="livekit"})`:                                             "1",
		`sum(livekit_room_total{job="livekit"})`:                             "2",
		`sum(livekit_participant_total{job="livekit"})`:                      "5",
		`sum(rate(process_cpu_seconds_total{job="livekit"}[5m])) * 100`:      "12.5",
		`sum(process_resident_memory_bytes{job="livekit"})`:                  "104857600",
		`sum(rate(process_network_receive_bytes_total{job="livekit"}[5m]))`:  "2048.5",
		`sum(rate(process_network_transmit_bytes_total{job="livekit"}[5m]))`: "4096.25",
		`100 * sum(rate(livekit_packet_loss_total{job="livekit"}[5m])) / clamp_min(sum(rate(livekit_packet_total{job="livekit"}[5m])), 1)`: "0.125",
		`sum(increase(livekit_participant_join_total{job="livekit"}[1h]))`:                                                                 "19.6",
		`sum(increase(livekit_room_duration_seconds_count{job="livekit"}[1h]))`:                                                            "7.4",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, ok := values[r.URL.Query().Get("query")]
		if r.URL.Path != "/api/v1/query" || !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,%q]}]}}`, value)
	}))
	defer server.Close()
	control, err := NewControl(Config{URL: "wss://chat.example.test/rtc", APIURL: server.URL, APIKey: "devkey", APISecret: testSecret, PrometheusURL: server.URL, TokenTTL: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := control.Metrics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !metrics.Healthy || metrics.ActiveRooms != 2 || metrics.ActiveParticipants != 5 || metrics.CPUPercent != 12.5 || metrics.ResidentMemoryBytes != 104857600 || metrics.NetworkReceiveBytesPerSec != 2048.5 || metrics.NetworkTransmitBytesPerSec != 4096.25 || metrics.PacketLossPercent != 0.125 || metrics.ParticipantJoinsLastHour != 20 || metrics.RoomsCompletedLastHour != 7 || metrics.SampledAt.IsZero() {
		t.Fatalf("metrics=%+v", metrics)
	}
}

func TestControlListsRoomsAndParticipantsAndPerformsAdminRemoval(t *testing.T) {
	removedRoom, removedIdentity, deletedRoom := "", "", ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		verifyRoomAdminGrant(t, r)
		w.Header().Set("Content-Type", "application/protobuf")
		switch r.URL.Path {
		case "/twirp/livekit.RoomService/ListRooms":
			encoded, _ := proto.Marshal(&lkproto.ListRoomsResponse{Rooms: []*lkproto.Room{{Sid: "RM_1", Name: "call_1", CreationTime: 1700000000, EmptyTimeout: 60, DepartureTimeout: 20, MaxParticipants: 9, NumParticipants: 2, NumPublishers: 1}}})
			_, _ = w.Write(encoded)
		case "/twirp/livekit.RoomService/ListParticipants":
			request := &lkproto.ListParticipantsRequest{}
			_ = proto.Unmarshal(body, request)
			if request.Room != "call_1" {
				t.Fatalf("participant room=%q", request.Room)
			}
			encoded, _ := proto.Marshal(&lkproto.ListParticipantsResponse{Participants: []*lkproto.ParticipantInfo{{
				Sid: "PA_1", Identity: "u1", State: lkproto.ParticipantInfo_ACTIVE, JoinedAt: 1700000001,
				Tracks: []*lkproto.TrackInfo{{Sid: "TR_1", Type: lkproto.TrackType_AUDIO, Source: lkproto.TrackSource_MICROPHONE, MimeType: "audio/opus"}},
			}}})
			_, _ = w.Write(encoded)
		case "/twirp/livekit.RoomService/RemoveParticipant":
			request := &lkproto.RoomParticipantIdentity{}
			_ = proto.Unmarshal(body, request)
			removedRoom, removedIdentity = request.Room, request.Identity
			encoded, _ := proto.Marshal(&lkproto.RemoveParticipantResponse{})
			_, _ = w.Write(encoded)
		case "/twirp/livekit.RoomService/DeleteRoom":
			request := &lkproto.DeleteRoomRequest{}
			_ = proto.Unmarshal(body, request)
			deletedRoom = request.Room
			encoded, _ := proto.Marshal(&lkproto.DeleteRoomResponse{})
			_, _ = w.Write(encoded)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	control, err := NewControl(Config{URL: "wss://chat.example.test/rtc", APIURL: server.URL, APIKey: "devkey", APISecret: testSecret, TokenTTL: 2 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := control.ListRooms(t.Context())
	if err != nil || len(rooms) != 1 || rooms[0].Name != "call_1" || rooms[0].ParticipantCount != 2 || rooms[0].MaxParticipants != 9 {
		t.Fatalf("rooms=%+v err=%v", rooms, err)
	}
	participants, err := control.ListParticipants(t.Context(), "call_1")
	if err != nil || len(participants) != 1 || participants[0].Identity != "u1" || len(participants[0].Tracks) != 1 || participants[0].Tracks[0].Source != "MICROPHONE" {
		t.Fatalf("participants=%+v err=%v", participants, err)
	}
	if err = control.RemoveParticipant(t.Context(), "call_1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err = control.DeleteRoom(t.Context(), "call_1"); err != nil {
		t.Fatal(err)
	}
	if removedRoom != "call_1" || removedIdentity != "u1" || deletedRoom != "call_1" {
		t.Fatalf("removed=%s/%s deleted=%s", removedRoom, removedIdentity, deletedRoom)
	}
}

func verifyRoomAdminGrant(t *testing.T, r *http.Request) {
	t.Helper()
	raw := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	verifier, err := lkauth.ParseAPIToken(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, grants, err := verifier.Verify(testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if grants.Video == nil {
		t.Fatal("missing video grant")
	}
	switch r.URL.Path {
	case "/twirp/livekit.RoomService/ListRooms":
		if !grants.Video.RoomList {
			t.Fatalf("list room grant=%+v", grants.Video)
		}
	case "/twirp/livekit.RoomService/ListParticipants", "/twirp/livekit.RoomService/RemoveParticipant":
		if !grants.Video.RoomAdmin || grants.Video.Room != "call_1" {
			t.Fatalf("room admin grant=%+v", grants.Video)
		}
	case "/twirp/livekit.RoomService/DeleteRoom":
		if !grants.Video.RoomCreate {
			t.Fatalf("delete room grant=%+v", grants.Video)
		}
	}
}
