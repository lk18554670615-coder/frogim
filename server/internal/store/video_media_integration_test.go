package store

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestVideoCoverSchema(t *testing.T) {
	if schemaVersion < 61 || !strings.Contains(normalizedSchema, "cover_media_id text REFERENCES im_media(id)") {
		t.Fatal("missing cover migration")
	}
}

func TestVideoCoverPostgres(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err = p.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("video_%d", time.Now().UnixNano())
	owner, peer, other, cid := prefix+"_owner", prefix+"_peer", prefix+"_other", prefix+"_group"
	ids := []string{owner, peer, other}
	defer func() {
		p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, prefix+"_forward")
		p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
		p.pool.Exec(ctx, `DELETE FROM im_media WHERE owner_id=ANY($1::text[])`, ids)
		p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, ids)
	}()
	for _, id := range ids {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,now())`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "Video test", []string{peer}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cover, video := prefix+"_cover", prefix+"_video"
	for _, m := range []Media{{ID: cover, OwnerID: owner, MIME: "image/jpeg", Status: "pending", Size: 12, ObjectKey: cover},
		{ID: video, OwnerID: owner, MIME: "video/mp4", Status: "pending", Size: 12, ObjectKey: video}} {
		if err = p.CreateMedia(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err = p.CompleteMediaWithCover(ctx, video, owner, 12, "sum", cover); err == nil {
		t.Fatal("pending cover accepted")
	}
	if err = p.CompleteMedia(ctx, cover, owner, 12, "sum"); err != nil {
		t.Fatal(err)
	}
	if err = p.CompleteMediaWithCover(ctx, video, owner, 12, "sum", cover); err != nil {
		t.Fatal(err)
	}
	if err = p.CompleteMediaWithCover(ctx, video, owner, 12, "sum", cover); err != nil {
		t.Fatal(err)
	}
	if err = p.CompleteMediaWithCover(ctx, video, owner, 12, "sum", ""); err == nil {
		t.Fatal("ready cover replaced")
	}
	if err = p.BindMediaChannel(ctx, MediaChannelBinding{MediaID: video, SenderID: owner, ChannelID: cid, ChannelType: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,content_type,media_id,payload_sha256,message_timestamp) VALUES($1,$2,$3,$4,$3,2,1,5,$5,'hash',now())`, time.Now().UnixNano(), prefix, cid, owner, video); err != nil {
		t.Fatal(err)
	}
	if ok, e := p.CanAccessMedia(ctx, peer, cover); e != nil || !ok {
		t.Fatalf("member cover access %v %v", ok, e)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_members SET history_after_seq=2 WHERE conversation_id=$1 AND user_id=$2`, cid, peer); err != nil {
		t.Fatal(err)
	}
	if ok, _ := p.CanAccessMedia(ctx, peer, cover); ok {
		t.Fatal("cover bypassed group history")
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_groups SET history_visible_to_new_members=true WHERE conversation_id=$1`, cid); err != nil {
		t.Fatal(err)
	}
	if ok, e := p.CanAccessMedia(ctx, peer, cover); e != nil || !ok {
		t.Fatalf("open history %v %v", ok, e)
	}
	if ok, _ := p.CanAccessMedia(ctx, other, cover); ok {
		t.Fatal("outsider admitted")
	}
	if err = p.BindMediaChannel(ctx, MediaChannelBinding{MediaID: video, SenderID: peer, ChannelID: other, ChannelType: 1}); err != nil {
		t.Fatal("forward", err)
	}
	// A channel binding alone no longer grants access. The published forward
	// must have its own message index, independent of the source lifecycle.
	if _, _, err = p.GetOrCreateDirectConversation(ctx, peer, other, prefix+"_forward", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,content_type,media_id,payload_sha256,message_timestamp) VALUES($1,$2,$3,$4,$5,1,1,5,$6,'hash',now())`, time.Now().UnixNano(), prefix+"_forward", prefix+"_forward", peer, other, video); err != nil {
		t.Fatal(err)
	}
	if ok, e := p.CanAccessMedia(ctx, other, cover); e != nil || !ok {
		t.Fatalf("forward cover %v %v", ok, e)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_media SET created_at=now()-interval '3 days' WHERE id=ANY($1::text[])`, []string{cover, video}); err != nil {
		t.Fatal(err)
	}
	leased, e := p.LeaseMediaCleanup(ctx, time.Now(), time.Hour, 24*time.Hour, 10*time.Minute, 100)
	if e != nil {
		t.Fatal(e)
	}
	for _, item := range leased {
		if item.ID == cover || item.ID == video {
			t.Fatal("referenced video/cover leased for deletion")
		}
	}
}
