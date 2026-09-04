package app

import (
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/teststore"
	"testing"
)

func TestVideoCoverOwnershipImmutabilityAndChannelAccess(t *testing.T) {
	a, err := New(t.Context(), teststore.Memory{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := a.Login("13800220101", "Video owner")
	peer, _ := a.Login("13800220102", "Video peer")
	outsider, _ := a.Login("13800220103", "Outsider")
	for _, m := range []store.Media{
		{ID: "cover", OwnerID: owner.ID, MIME: "image/jpeg", Size: 12, Status: "ready"},
		{ID: "wrong", OwnerID: outsider.ID, MIME: "image/jpeg", Size: 12, Status: "ready"},
		{ID: "png", OwnerID: owner.ID, MIME: "image/png", Size: 12, Status: "ready"},
		{ID: "pending-cover", OwnerID: owner.ID, MIME: "image/jpeg", Size: 12, Status: "pending"},
		{ID: "video", OwnerID: owner.ID, MIME: "video/mp4", Size: 12, Status: "pending"},
	} {
		if err = a.CreateMedia(m); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"wrong", "png", "pending-cover", "video", "missing"} {
		if err = a.CompleteMediaWithCover("video", owner.ID, 12, "sum", id); err == nil {
			t.Fatalf("accepted %s", id)
		}
	}
	if err = a.CompleteMediaWithCover("video", owner.ID, 12, "sum", "cover"); err != nil {
		t.Fatal(err)
	}
	if err = a.CompleteMediaWithCover("video", owner.ID, 12, "sum", "cover"); err != nil {
		t.Fatal("idempotent", err)
	}
	if err = a.CompleteMediaWithCover("video", owner.ID, 12, "sum", ""); err == nil {
		t.Fatal("replaced ready cover")
	}
	if err = a.BindMediaChannel(store.MediaChannelBinding{MediaID: "video", SenderID: owner.ID, ChannelID: peer.ID, ChannelType: 1}); err != nil {
		t.Fatal(err)
	}
	if ok, e := a.CanAccessMedia(peer.ID, "cover"); e != nil || !ok {
		t.Fatalf("peer denied: %v", e)
	}
	if ok, _ := a.CanAccessMedia(outsider.ID, "cover"); ok {
		t.Fatal("outsider admitted")
	}
}
