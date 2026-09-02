package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresGroupAnnouncementAllMembersAndRepeatedPublications(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("isolated PostgreSQL required")
	}
	ctx := t.Context()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	schema := fmt.Sprintf("announcement_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, `CREATE SCHEMA `+quoted); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := conn.Exec(context.Background(), `DROP SCHEMA `+quoted+` CASCADE`); err != nil {
			t.Error(err)
		}
	}()
	sep := "?"
	if strings.Contains(url, "?") {
		sep = "&"
	}
	p, err := NewPostgres(ctx, url+sep+"search_path="+schema+",public")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i, uid := range []string{"owner", "admin", "member", "outsider"} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$1,$3,$3)`, uid, fmt.Sprintf("138%08d", i), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.CreateGroupRecord(ctx, "group", "owner", "Announcement test", []string{"admin", "member"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_members SET role='admin' WHERE user_id='admin'`); err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"member", "outsider"} {
		if _, err = p.SetGroupAnnouncement(ctx, uid, "group", "not allowed", now); !errors.Is(err, ErrForbidden) {
			t.Fatalf("%s edit: %v", uid, err)
		}
	}
	// Two consecutive updates by one actor used to collide in the CMD outbox.
	for index, actor := range []string{"owner", "owner", "admin"} {
		content := fmt.Sprintf("announcement body %d", index+1)
		at := now.Add(time.Duration(index+1) * time.Second)
		published, err := p.SetGroupAnnouncement(ctx, actor, "group", content, at)
		if err != nil || published.AnnouncementVersion != int64(index+1) {
			t.Fatalf("publish: %+v %v", published, err)
		}
		for _, uid := range []string{"owner", "admin", "member"} {
			profile, err := p.GetGroupProfile(ctx, uid, "group")
			if err != nil || profile.Announcement != content || profile.AnnouncementReadAt != nil {
				t.Fatalf("%s read: %+v %v", uid, profile, err)
			}
			readAt := at.Add(time.Millisecond)
			for _, attempt := range []time.Time{readAt, readAt.Add(time.Second)} {
				if err = p.MarkGroupAnnouncementRead(ctx, uid, "group", attempt); err != nil {
					t.Fatalf("%s idempotent read: %v", uid, err)
				}
			}
			profile, err = p.GetGroupProfile(ctx, uid, "group")
			if err != nil || profile.AnnouncementReadAt == nil || !profile.AnnouncementReadAt.Equal(readAt) {
				t.Fatalf("first read time: %+v %v", profile, err)
			}
		}
	}
	if _, err = p.GetGroupProfile(ctx, "outsider", "group"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider read: %v", err)
	}
	if err = p.MarkGroupAnnouncementRead(ctx, "outsider", "group", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider mark read: %v", err)
	}
	rows, err := p.pool.Query(ctx, `SELECT payload FROM im_wukong_outbox WHERE operation='business.event' AND payload->>'event'='group.system' AND payload->'param'->'payload'->>'event'='group.announcement.updated' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var cmd struct {
			Recipients []string `json:"recipients"`
			Param      struct {
				Payload struct {
					Data struct {
						Version int `json:"announcementVersion"`
					} `json:"data"`
				} `json:"payload"`
			} `json:"param"`
		}
		if err = json.Unmarshal(raw, &cmd); err != nil {
			t.Fatal(err)
		}
		count++
		if strings.Join(cmd.Recipients, ",") != "admin,member,owner" || cmd.Param.Payload.Data.Version != count {
			t.Fatalf("CMD: %s", raw)
		}
		if strings.Contains(string(raw), "announcement body") {
			t.Fatal("announcement body duplicated in CMD")
		}
	}
	rows.Close()
	if rows.Err() != nil || count != 3 {
		t.Fatalf("distinct publication CMDs=%d err=%v", count, rows.Err())
	}
	var stored, audits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation='message.store' AND payload->'payload'->>'event'='group.announcement.updated' AND payload->'payload'->>'digest'='群公告已更新，点击查看'`).Scan(&stored); err != nil || stored != 3 {
		t.Fatalf("stored notices=%d err=%v", stored, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='group.announcement.updated' AND metadata ? 'announcementVersion' AND NOT metadata::text LIKE '%announcement body%'`).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
}
