package store

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/wukong"
)

type wukongCommandRecipient struct {
	UserID string
}

type wukongMutationMeta struct {
	MessageID, ConversationID, SenderID, ChannelID, Role string
	ChannelType                                          uint8
	MessageSeq                                           int64
	ContentType                                          int
	MessageAt                                            time.Time
	Extension                                            map[string]any
}

func looksLikeWukongMessageID(messageID string) bool {
	parsed, err := strconv.ParseInt(strings.TrimSpace(messageID), 10, 64)
	return err == nil && parsed > 0
}

func (p *Postgres) waitForWukongMessageIndex(ctx context.Context, messageID string) (bool, error) {
	if !looksLikeWukongMessageID(messageID) {
		return false, nil
	}
	deadline := time.NewTimer(750 * time.Millisecond)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		var exists bool
		if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_wukong_message_index WHERE message_id::text=$1)`, messageID).Scan(&exists); err != nil {
			return false, err
		}
		if exists {
			return true, nil
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-deadline.C:
			return false, nil
		case <-ticker.C:
		}
	}
}

func loadWukongMutationMeta(ctx context.Context, tx pgx.Tx, userID, messageID string) (wukongMutationMeta, bool, error) {
	meta := wukongMutationMeta{}
	var extension []byte
	err := tx.QueryRow(ctx, `
		SELECT i.message_id::text,i.conversation_id,i.sender_id,i.channel_id,i.channel_type,
			i.message_seq,i.content_type,i.message_timestamp,
			COALESCE((SELECT role FROM im_members WHERE conversation_id=i.conversation_id AND user_id=$2),''),
			COALESCE(ext.payload,'{}'::jsonb)
		FROM im_wukong_message_index i
		LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id
			AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type
		WHERE i.message_id::text=$1
		FOR UPDATE OF i
	`, messageID, userID).Scan(
		&meta.MessageID, &meta.ConversationID, &meta.SenderID, &meta.ChannelID, &meta.ChannelType,
		&meta.MessageSeq, &meta.ContentType, &meta.MessageAt, &meta.Role, &extension,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return meta, false, nil
	}
	if err != nil {
		return meta, true, err
	}
	if meta.ConversationID == "" || meta.Role == "" {
		return meta, true, ErrForbidden
	}
	if err = json.Unmarshal(extension, &meta.Extension); err != nil {
		return meta, true, err
	}
	return meta, true, nil
}

func saveWukongMessageExtension(ctx context.Context, tx pgx.Tx, meta wukongMutationMeta, actor string, payload map[string]any, at time.Time) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO im_wukong_message_extensions(channel_id,channel_type,message_id,version,sync_version,payload,updated_by,updated_at)
		VALUES($1,$2,$3::bigint,1,nextval('im_wukong_message_extension_sync_version_seq'),$4,$5,$6)
		ON CONFLICT(channel_id,channel_type,message_id) DO UPDATE SET
			version=im_wukong_message_extensions.version+1,
			sync_version=nextval('im_wukong_message_extension_sync_version_seq'),payload=excluded.payload,
			updated_by=excluded.updated_by,updated_at=excluded.updated_at
	`, meta.ChannelID, meta.ChannelType, meta.MessageID, raw, actor, at)
	return err
}

func (p *Postgres) recallWukongAuthorized(ctx context.Context, uid, mid string, at time.Time, window time.Duration) (bool, string, int64, []string, error) {
	indexed, err := p.waitForWukongMessageIndex(ctx, mid)
	if err != nil {
		return true, "", 0, nil, err
	}
	if looksLikeWukongMessageID(mid) && !indexed {
		return true, "", 0, nil, ErrNotFound
	}
	if !indexed {
		return false, "", 0, nil, nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return true, "", 0, nil, err
	}
	defer tx.Rollback(ctx)
	meta, found, err := loadWukongMutationMeta(ctx, tx, uid, mid)
	if err != nil || !found {
		return found, "", 0, nil, err
	}
	if meta.SenderID != uid && meta.Role != "owner" && meta.Role != "admin" {
		return true, "", 0, nil, ErrForbidden
	}
	if meta.SenderID == uid && at.Sub(meta.MessageAt) > window {
		return true, "", 0, nil, ErrForbidden
	}
	if meta.Extension["recalledAt"] != nil {
		return true, meta.ConversationID, meta.MessageSeq, nil, tx.Commit(ctx)
	}
	meta.Extension["recalledAt"] = at.UTC().Format(time.RFC3339Nano)
	meta.Extension["revoker"] = uid
	if err = saveWukongMessageExtension(ctx, tx, meta, uid, meta.Extension, at); err != nil {
		return true, "", 0, nil, err
	}
	payload, _ := json.Marshal(map[string]any{"messageId": mid, "conversationId": meta.ConversationID, "conversationSeq": meta.MessageSeq})
	ids, err := appendMemberBusinessEvent(ctx, tx, meta.ConversationID, "message.recalled", payload, at)
	if err != nil {
		return true, "", 0, nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return true, "", 0, nil, err
	}
	return true, meta.ConversationID, meta.MessageSeq, ids, nil
}

func (p *Postgres) reactWukongMessage(ctx context.Context, uid, mid, emoji string, add bool, at time.Time) (bool, model.MessageReactionSummary, bool, error) {
	indexed, err := p.waitForWukongMessageIndex(ctx, mid)
	if err != nil {
		return true, model.MessageReactionSummary{}, false, err
	}
	if looksLikeWukongMessageID(mid) && !indexed {
		return true, model.MessageReactionSummary{}, false, ErrNotFound
	}
	if !indexed {
		return false, model.MessageReactionSummary{}, false, nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return true, model.MessageReactionSummary{}, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "wukong-reaction:"+mid+":"+emoji); err != nil {
		return true, model.MessageReactionSummary{}, false, err
	}
	meta, found, err := loadWukongMutationMeta(ctx, tx, uid, mid)
	if err != nil || !found {
		return found, model.MessageReactionSummary{}, false, err
	}
	if meta.Extension["recalledAt"] != nil {
		return true, model.MessageReactionSummary{}, false, ErrForbidden
	}
	var changed bool
	if add {
		tag, execErr := tx.Exec(ctx, `INSERT INTO im_message_reactions(message_id,user_id,emoji,created_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, mid, uid, emoji, at)
		if execErr != nil {
			return true, model.MessageReactionSummary{}, false, execErr
		}
		changed = tag.RowsAffected() == 1
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM im_message_reactions WHERE message_id=$1 AND user_id=$2 AND emoji=$3`, mid, uid, emoji)
		if execErr != nil {
			return true, model.MessageReactionSummary{}, false, execErr
		}
		changed = tag.RowsAffected() == 1
	}
	summary := model.MessageReactionSummary{Emoji: emoji, ReactedByMe: add}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_message_reactions WHERE message_id=$1 AND emoji=$2`, mid, emoji).Scan(&summary.Count); err != nil {
		return true, model.MessageReactionSummary{}, false, err
	}
	if changed {
		meta.Extension["reactionsUpdatedAt"] = at.UTC().Format(time.RFC3339Nano)
		if err = saveWukongMessageExtension(ctx, tx, meta, uid, meta.Extension, at); err != nil {
			return true, model.MessageReactionSummary{}, false, err
		}
		payload, _ := json.Marshal(map[string]any{"messageId": mid, "conversationId": meta.ConversationID, "emoji": emoji, "actorId": uid, "added": add, "count": summary.Count})
		if _, err = appendMemberBusinessEvent(ctx, tx, meta.ConversationID, "message.reaction.updated", payload, at); err != nil {
			return true, model.MessageReactionSummary{}, false, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return true, model.MessageReactionSummary{}, false, err
	}
	return true, summary, !changed, nil
}

func extensionInt(payload map[string]any, key string) int {
	switch value := payload[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func (p *Postgres) editWukongMessage(ctx context.Context, uid, mid, editID string, body, originalBody map[string]any, at time.Time, window time.Duration) (bool, *model.Message, bool, error) {
	indexed, err := p.waitForWukongMessageIndex(ctx, mid)
	if err != nil {
		return true, nil, false, err
	}
	if looksLikeWukongMessageID(mid) && !indexed {
		return true, nil, false, ErrNotFound
	}
	if !indexed {
		return false, nil, false, nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return true, nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "wukong-edit:"+mid); err != nil {
		return true, nil, false, err
	}
	meta, found, err := loadWukongMutationMeta(ctx, tx, uid, mid)
	if err != nil || !found {
		return true, nil, false, err
	}
	if meta.SenderID != uid || meta.ContentType != wukong.ContentTypeText || meta.Extension["recalledAt"] != nil || at.Sub(meta.MessageAt) > window {
		return true, nil, false, ErrForbidden
	}
	newRaw, _ := json.Marshal(body)
	var existingVersion int
	var sameRequest bool
	if err = tx.QueryRow(ctx, `SELECT version,body=$3::jsonb FROM im_message_edit_requests WHERE message_id=$1 AND edit_id=$2`, mid, editID, newRaw).Scan(&existingVersion, &sameRequest); err == nil {
		if !sameRequest {
			return true, nil, false, ErrConflict
		}
		message := &model.Message{ID: mid, ConversationID: meta.ConversationID, SenderID: meta.SenderID, Seq: meta.MessageSeq, Type: "text", Body: body, EditVersion: existingVersion, CreatedAt: meta.MessageAt}
		if value, ok := meta.Extension["editedAt"].(string); ok {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, value); parseErr == nil {
				message.EditedAt = &parsed
			}
		}
		return true, message, true, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return true, nil, false, err
	}
	text, _ := body["text"].(string)
	var denied bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sensitive_words WHERE split_part(value,'|',1)<>'' AND position(lower(split_part(value,'|',1)) in lower($1))>0)`, text).Scan(&denied); err != nil {
		return true, nil, false, err
	}
	if denied {
		return true, nil, false, ErrForbidden
	}
	var kind string
	if err = tx.QueryRow(ctx, `SELECT kind FROM im_conversations WHERE id=$1`, meta.ConversationID).Scan(&kind); err != nil {
		return true, nil, false, err
	}
	if err = validateEditMentions(ctx, tx, meta.ConversationID, uid, kind, meta.Role, body); err != nil {
		return true, nil, false, err
	}
	currentVersion := extensionInt(meta.Extension, "editVersion")
	if current, ok := meta.Extension["editedBody"].(map[string]any); ok {
		currentRaw, _ := json.Marshal(current)
		if string(currentRaw) == string(newRaw) {
			if _, err = tx.Exec(ctx, `INSERT INTO im_message_edit_requests(message_id,edit_id,body,version,created_at) VALUES($1,$2,$3,$4,$5)`, mid, editID, newRaw, currentVersion, at); err != nil {
				return true, nil, false, err
			}
			message := &model.Message{ID: mid, ConversationID: meta.ConversationID, SenderID: meta.SenderID, Seq: meta.MessageSeq, Type: "text", Body: body, EditVersion: currentVersion, CreatedAt: meta.MessageAt}
			return true, message, true, tx.Commit(ctx)
		}
	}
	newVersion := currentVersion + 1
	if currentVersion == 0 {
		if len(originalBody) == 0 {
			return true, nil, false, ErrConflict
		}
		originalRaw, marshalErr := json.Marshal(originalBody)
		if marshalErr != nil {
			return true, nil, false, marshalErr
		}
		if _, err = tx.Exec(ctx, `INSERT INTO im_message_edits(message_id,version,editor_id,body,created_at) VALUES($1,0,$2,$3,$4) ON CONFLICT DO NOTHING`, mid, meta.SenderID, originalRaw, meta.MessageAt); err != nil {
			return true, nil, false, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_message_edits(message_id,version,edit_id,editor_id,body,created_at) VALUES($1,$2,$3,$4,$5,$6)`, mid, newVersion, editID, uid, newRaw, at); err != nil {
		return true, nil, false, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_message_edit_requests(message_id,edit_id,body,version,created_at) VALUES($1,$2,$3,$4,$5)`, mid, editID, newRaw, newVersion, at); err != nil {
		return true, nil, false, err
	}
	meta.Extension["editedAt"] = at.UTC().Format(time.RFC3339Nano)
	meta.Extension["editVersion"] = newVersion
	meta.Extension["editedBody"] = body
	if err = saveWukongMessageExtension(ctx, tx, meta, uid, meta.Extension, at); err != nil {
		return true, nil, false, err
	}
	payload, _ := json.Marshal(map[string]any{"messageId": mid, "conversationId": meta.ConversationID, "conversationSeq": meta.MessageSeq, "editId": editID, "editVersion": newVersion})
	if _, err = appendMemberBusinessEvent(ctx, tx, meta.ConversationID, "message.edited", payload, at); err != nil {
		return true, nil, false, err
	}
	metadata, _ := json.Marshal(map[string]any{"editId": editID, "version": newVersion})
	if _, err = tx.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,created_at) VALUES($1,$2,'message.edited','message',$3,$4,$5)`, "aud_wukong_edit_"+strconv.FormatInt(at.UnixNano(), 36), uid, mid, metadata, at); err != nil {
		return true, nil, false, err
	}
	message := &model.Message{ID: mid, ConversationID: meta.ConversationID, SenderID: meta.SenderID, Seq: meta.MessageSeq, Type: "text", Body: body, EditVersion: newVersion, EditedAt: &at, CreatedAt: meta.MessageAt}
	if err = tx.Commit(ctx); err != nil {
		return true, nil, false, err
	}
	return true, message, false, nil
}

func (p *Postgres) listWukongMessageEdits(ctx context.Context, uid, mid string) (bool, []*model.MessageEdit, error) {
	indexed, err := p.waitForWukongMessageIndex(ctx, mid)
	if err != nil {
		return true, nil, err
	}
	if looksLikeWukongMessageID(mid) && !indexed {
		return true, nil, ErrNotFound
	}
	if !indexed {
		return false, nil, nil
	}
	var allowed bool
	var recalled bool
	err = p.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM im_wukong_message_index i JOIN im_members m ON m.conversation_id=i.conversation_id AND m.user_id=$2 WHERE i.message_id::text=$1),
			EXISTS(SELECT 1 FROM im_wukong_message_extensions e WHERE e.message_id::text=$1 AND e.payload ? 'recalledAt')
	`, mid, uid).Scan(&allowed, &recalled)
	if err != nil {
		return true, nil, err
	}
	if !allowed || recalled {
		return true, nil, ErrForbidden
	}
	rows, err := p.pool.Query(ctx, `SELECT message_id,version,editor_id,body,created_at FROM im_message_edits WHERE message_id=$1 ORDER BY version`, mid)
	if err != nil {
		return true, nil, err
	}
	defer rows.Close()
	items := []*model.MessageEdit{}
	for rows.Next() {
		item := &model.MessageEdit{}
		var raw []byte
		if err = rows.Scan(&item.MessageID, &item.Version, &item.EditorID, &raw, &item.EditedAt); err != nil {
			return true, nil, err
		}
		if err = json.Unmarshal(raw, &item.Body); err != nil {
			return true, nil, err
		}
		items = append(items, item)
	}
	return true, items, rows.Err()
}

func wukongModelMessage(meta wukongMutationMeta) *model.Message {
	messageType := map[int]string{
		wukong.ContentTypeText: "text", wukong.ContentTypeImage: "image", wukong.ContentTypeGIF: "image",
		wukong.ContentTypeVoice: "audio", wukong.ContentTypeVideo: "video", wukong.ContentTypeLocation: "location",
		wukong.ContentTypeCard: "contact", wukong.ContentTypeFile: "file", wukong.ContentTypeMergedHistory: "chat_history",
	}[meta.ContentType]
	return &model.Message{
		ID: meta.MessageID, ConversationID: meta.ConversationID, SenderID: meta.SenderID,
		Seq: meta.MessageSeq, Type: messageType, CreatedAt: meta.MessageAt,
	}
}

func (p *Postgres) setWukongGroupMessagePin(ctx context.Context, uid, cid, mid string, pin bool, at time.Time) (bool, *model.MessagePin, bool, error) {
	indexed, err := p.waitForWukongMessageIndex(ctx, mid)
	if err != nil {
		return true, nil, false, err
	}
	if looksLikeWukongMessageID(mid) && !indexed {
		return true, nil, false, ErrNotFound
	}
	if !indexed {
		return false, nil, false, nil
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return true, nil, false, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "wukong-pin:"+cid+":"+mid); err != nil {
		return true, nil, false, err
	}
	role, _, dissolved, err := groupActor(ctx, tx, cid, uid)
	if err != nil {
		return true, nil, false, err
	}
	if dissolved != nil || (role != "owner" && role != "admin") {
		return true, nil, false, ErrForbidden
	}
	meta, found, err := loadWukongMutationMeta(ctx, tx, uid, mid)
	if err != nil || !found {
		return true, nil, false, err
	}
	if meta.ConversationID != cid || meta.Extension["recalledAt"] != nil {
		return true, nil, false, ErrNotFound
	}
	changed := false
	pinnedBy, pinnedAt := uid, at
	if pin {
		tag, execErr := tx.Exec(ctx, `INSERT INTO im_group_message_pins(conversation_id,message_id,pinned_by,pinned_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, cid, mid, uid, at)
		if execErr != nil {
			return true, nil, false, execErr
		}
		changed = tag.RowsAffected() == 1
		if !changed {
			if err = tx.QueryRow(ctx, `SELECT pinned_by,pinned_at FROM im_group_message_pins WHERE conversation_id=$1 AND message_id=$2`, cid, mid).Scan(&pinnedBy, &pinnedAt); err != nil {
				return true, nil, false, err
			}
		}
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM im_group_message_pins WHERE conversation_id=$1 AND message_id=$2`, cid, mid)
		if execErr != nil {
			return true, nil, false, execErr
		}
		changed = tag.RowsAffected() == 1
	}
	if changed {
		event := "group.message.pinned"
		if pin {
			meta.Extension["isPinned"] = true
			meta.Extension["pinnedBy"] = uid
			meta.Extension["pinnedAt"] = at.UTC().Format(time.RFC3339Nano)
		} else {
			event = "group.message.unpinned"
			delete(meta.Extension, "isPinned")
			delete(meta.Extension, "pinnedBy")
			delete(meta.Extension, "pinnedAt")
		}
		if err = saveWukongMessageExtension(ctx, tx, meta, uid, meta.Extension, at); err != nil {
			return true, nil, false, err
		}
		payload, _ := json.Marshal(map[string]any{"conversationId": cid, "messageId": mid, "actorId": uid})
		if _, err = appendMemberBusinessEvent(ctx, tx, cid, event, payload, at); err != nil {
			return true, nil, false, err
		}
		if err = emitGroupSystem(ctx, tx, cid, uid, event, map[string]any{"messageId": mid}, at); err != nil {
			return true, nil, false, err
		}
	}
	item := &model.MessagePin{ConversationID: cid, Message: wukongModelMessage(meta), PinnedBy: pinnedBy, PinnedAt: pinnedAt}
	if err = tx.Commit(ctx); err != nil {
		return true, nil, false, err
	}
	return true, item, !changed, nil
}

func (p *Postgres) listWukongGroupMessagePins(ctx context.Context, uid, cid string, before int64, limit int) (bool, []*model.MessagePin, error) {
	var usesWukong bool
	if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_wukong_message_index WHERE conversation_id=$1)`, cid).Scan(&usesWukong); err != nil {
		return true, nil, err
	}
	if !usesWukong {
		return false, nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	allowed, err := p.CanAccessConversation(ctx, uid, cid)
	if err != nil {
		return true, nil, err
	}
	if !allowed {
		return true, nil, ErrForbidden
	}
	rows, err := p.pool.Query(ctx, `
		SELECT i.message_id::text,i.sender_id,i.message_seq,i.content_type,i.message_timestamp,
			pin.pinned_by,pin.pinned_at,COALESCE(ext.payload,'{}'::jsonb)
		FROM im_group_message_pins pin
		JOIN im_wukong_message_index i ON i.message_id::text=pin.message_id AND i.conversation_id=pin.conversation_id
		LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id
			AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type
		WHERE pin.conversation_id=$1 AND NOT (COALESCE(ext.payload,'{}'::jsonb) ? 'recalledAt')
			AND ($2::bigint=0 OR pin.pinned_at<to_timestamp($2::double precision/1000))
		ORDER BY pin.pinned_at DESC,pin.message_id LIMIT $3
	`, cid, before, limit)
	if err != nil {
		return true, nil, err
	}
	defer rows.Close()
	items := []*model.MessagePin{}
	for rows.Next() {
		meta := wukongMutationMeta{ConversationID: cid}
		item := &model.MessagePin{ConversationID: cid}
		var extension []byte
		if err = rows.Scan(&meta.MessageID, &meta.SenderID, &meta.MessageSeq, &meta.ContentType, &meta.MessageAt, &item.PinnedBy, &item.PinnedAt, &extension); err != nil {
			return true, nil, err
		}
		if err = json.Unmarshal(extension, &meta.Extension); err != nil {
			return true, nil, err
		}
		item.Message = wukongModelMessage(meta)
		applyWukongExtensionToModel(item.Message, meta.Extension)
		items = append(items, item)
	}
	return true, items, rows.Err()
}

func applyWukongExtensionToModel(message *model.Message, extension map[string]any) {
	if value, ok := extension["recalledAt"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			message.RecalledAt = &parsed
		}
	}
	if value, ok := extension["editedAt"].(string); ok {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			message.EditedAt = &parsed
		}
	}
	message.EditVersion = extensionInt(extension, "editVersion")
	if body, ok := extension["editedBody"].(map[string]any); ok {
		message.Body = body
	}
}

func (p *Postgres) setWukongFavorite(ctx context.Context, uid, messageID string, enabled bool) (bool, error) {
	if !looksLikeWukongMessageID(messageID) {
		return false, nil
	}
	if !enabled {
		_, err := p.pool.Exec(ctx, `DELETE FROM im_favorites WHERE user_id=$1 AND message_id=$2`, uid, messageID)
		return true, err
	}
	indexed, err := p.waitForWukongMessageIndex(ctx, messageID)
	if err != nil {
		return true, err
	}
	if !indexed {
		return true, ErrNotFound
	}
	result, err := p.pool.Exec(ctx, `
		INSERT INTO im_favorites(user_id,message_id,created_at)
		SELECT $1,i.message_id::text,now()
		FROM im_wukong_message_index i
		JOIN im_members member ON member.conversation_id=i.conversation_id AND member.user_id=$1
		WHERE i.message_id::text=$2
		ON CONFLICT(user_id,message_id) DO UPDATE SET created_at=EXCLUDED.created_at
	`, uid, messageID)
	if err != nil {
		return true, err
	}
	if result.RowsAffected() == 0 {
		return true, ErrForbidden
	}
	return true, nil
}

func (p *Postgres) listWukongFavorites(ctx context.Context, uid string, limit int) (bool, []*model.Message, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := p.pool.Query(ctx, `
		SELECT i.message_id::text,i.conversation_id,i.sender_id,i.message_seq,i.content_type,i.message_timestamp,
			COALESCE(ext.payload,'{}'::jsonb)
		FROM im_favorites favorite
		JOIN im_wukong_message_index i ON i.message_id::text=favorite.message_id
		LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id
			AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type
		WHERE favorite.user_id=$1
		ORDER BY favorite.created_at DESC LIMIT $2
	`, uid, limit)
	if err != nil {
		return true, nil, err
	}
	defer rows.Close()
	items := []*model.Message{}
	for rows.Next() {
		meta := wukongMutationMeta{}
		var extension []byte
		if err = rows.Scan(&meta.MessageID, &meta.ConversationID, &meta.SenderID, &meta.MessageSeq, &meta.ContentType, &meta.MessageAt, &extension); err != nil {
			return true, nil, err
		}
		if err = json.Unmarshal(extension, &meta.Extension); err != nil {
			return true, nil, err
		}
		message := wukongModelMessage(meta)
		applyWukongExtensionToModel(message, meta.Extension)
		items = append(items, message)
	}
	if err = rows.Err(); err != nil {
		return true, nil, err
	}
	if len(items) > 0 {
		return true, items, nil
	}
	var hasLegacy bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_favorites favorite JOIN im_messages message ON message.id=favorite.message_id WHERE favorite.user_id=$1)`, uid).Scan(&hasLegacy); err != nil {
		return true, nil, err
	}
	if hasLegacy {
		return false, nil, nil
	}
	return true, items, nil
}

func (p *Postgres) LoadWukongMessageExtensions(ctx context.Context, userID string, messageIDs []string) (map[string]map[string]any, error) {
	result := make(map[string]map[string]any, len(messageIDs))
	if strings.TrimSpace(userID) == "" || len(messageIDs) == 0 || len(messageIDs) > 500 {
		return result, nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT i.message_id::text,COALESCE(ext.version,0),COALESCE(ext.payload,'{}'::jsonb),
			pin.pinned_by,pin.pinned_at
		FROM im_wukong_message_index i
		JOIN im_members member ON member.conversation_id=i.conversation_id AND member.user_id=$1
		LEFT JOIN im_wukong_message_extensions ext ON ext.message_id=i.message_id AND ext.channel_id=i.channel_id AND ext.channel_type=i.channel_type
		LEFT JOIN im_group_message_pins pin ON pin.message_id=i.message_id::text AND pin.conversation_id=i.conversation_id
		WHERE i.message_id::text=ANY($2::text[])
	`, userID, messageIDs)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var messageID string
		var version int64
		var raw []byte
		var pinnedBy *string
		var pinnedAt *time.Time
		if err = rows.Scan(&messageID, &version, &raw, &pinnedBy, &pinnedAt); err != nil {
			rows.Close()
			return nil, err
		}
		extension := map[string]any{"version": version}
		if err = json.Unmarshal(raw, &extension); err != nil {
			rows.Close()
			return nil, err
		}
		extension["version"] = version
		if pinnedBy != nil {
			extension["isPinned"], extension["pinnedBy"], extension["pinnedAt"] = true, *pinnedBy, pinnedAt.UTC().Format(time.RFC3339Nano)
		}
		result[messageID] = extension
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	reactionRows, err := p.pool.Query(ctx, `
		SELECT message_id,emoji,count(*),bool_or(user_id=$1)
		FROM im_message_reactions WHERE message_id=ANY($2::text[])
		GROUP BY message_id,emoji ORDER BY message_id,emoji
	`, userID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer reactionRows.Close()
	for reactionRows.Next() {
		var messageID, emoji string
		var count int
		var reacted bool
		if err = reactionRows.Scan(&messageID, &emoji, &count, &reacted); err != nil {
			return nil, err
		}
		extension := result[messageID]
		if extension == nil {
			extension = map[string]any{"version": int64(0)}
			result[messageID] = extension
		}
		reactions, _ := extension["reactions"].([]map[string]any)
		extension["reactions"] = append(reactions, map[string]any{"emoji": emoji, "count": count, "reactedByMe": reacted})
	}
	return result, reactionRows.Err()
}

func (p *WithRedis) LoadWukongMessageExtensions(ctx context.Context, userID string, messageIDs []string) (map[string]map[string]any, error) {
	if extensions, ok := p.base.(WukongMessageExtensionStore); ok {
		return extensions.LoadWukongMessageExtensions(ctx, userID, messageIDs)
	}
	return nil, ErrUnsupported
}

func (p *Postgres) SyncWukongMessageExtras(ctx context.Context, userID, channelID string, channelType uint8, version int64, limit int) ([]WukongMessageExtra, error) {
	if version < 0 {
		return nil, ErrConflict
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	conversationID, err := p.resolveWukongConversationID(ctx, userID, channelID, channelType)
	if err != nil {
		return nil, err
	}
	rows, err := p.pool.Query(ctx, `SELECT extension.message_id::text,message_index.message_seq,
		extension.sync_version,extension.payload,
		COALESCE((SELECT count(*) FROM im_members reader WHERE reader.conversation_id=message_index.conversation_id
			AND reader.user_id<>message_index.sender_id AND reader.last_read_seq>=message_index.message_seq),0),
		GREATEST((SELECT count(*)-1 FROM im_members member_count WHERE member_count.conversation_id=message_index.conversation_id),0),
		COALESCE((SELECT own.last_read_seq>=message_index.message_seq FROM im_members own
			WHERE own.conversation_id=message_index.conversation_id AND own.user_id=$2),false)
		FROM im_wukong_message_extensions extension
		JOIN im_wukong_message_index message_index ON message_index.message_id=extension.message_id
			AND message_index.channel_id=extension.channel_id AND message_index.channel_type=extension.channel_type
		WHERE message_index.conversation_id=$1 AND extension.sync_version>$3
		ORDER BY extension.sync_version LIMIT $4`, conversationID, userID, version, limit)
	if err != nil {
		return nil, err
	}
	items := make([]WukongMessageExtra, 0, limit)
	messageIDs := make([]string, 0, limit)
	for rows.Next() {
		item := WukongMessageExtra{ChannelID: channelID, ChannelType: channelType}
		var raw []byte
		var recipientCount int
		var read bool
		if err = rows.Scan(&item.MessageID, &item.MessageSeq, &item.SyncVersion, &raw,
			&item.ReadCount, &recipientCount, &read); err != nil {
			rows.Close()
			return nil, err
		}
		if read {
			item.Read = 1
		}
		if err = json.Unmarshal(raw, &item.Extra); err != nil {
			rows.Close()
			return nil, err
		}
		item.UnreadCount = max(0, recipientCount-item.ReadCount)
		item.Recalled = item.Extra["recalledAt"] != nil
		item.Revoker = wukongStringValue(item.Extra["revoker"])
		if item.Revoker == "" {
			item.Revoker = wukongStringValue(item.Extra["moderatedBy"])
		}
		if body, ok := item.Extra["editedBody"].(map[string]any); ok {
			item.EditedBody = body
		}
		if value, ok := item.Extra["editedAt"].(string); ok {
			if parsed, parseErr := time.Parse(time.RFC3339Nano, value); parseErr == nil {
				item.EditedAt = parsed.Unix()
			}
		}
		items = append(items, item)
		messageIDs = append(messageIDs, item.MessageID)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	extensions, err := p.LoadWukongMessageExtensions(ctx, userID, messageIDs)
	if err != nil {
		return nil, err
	}
	for index := range items {
		if extension := extensions[items[index].MessageID]; extension != nil {
			items[index].Extra = extension
			items[index].Pinned, _ = extension["isPinned"].(bool)
		}
	}
	return items, nil
}

func wukongStringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func (p *WithRedis) SyncWukongMessageExtras(ctx context.Context, userID, channelID string, channelType uint8, version int64, limit int) ([]WukongMessageExtra, error) {
	if extensions, ok := p.base.(WukongMessageExtensionStore); ok {
		return extensions.SyncWukongMessageExtras(ctx, userID, channelID, channelType, version, limit)
	}
	return nil, ErrUnsupported
}

func (p *Postgres) SyncWukongReminders(ctx context.Context, userID string, version int64, limit int) ([]WukongReminder, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || version < 0 {
		return nil, ErrConflict
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id,user_id,message_id::text,message_seq,channel_id,channel_type,type,
			is_locate,text,data,version,done,need_upload,publisher
		FROM im_wukong_reminders
		WHERE user_id=$1 AND version>$2
		ORDER BY version
		LIMIT $3
	`, userID, version, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WukongReminder, 0, limit)
	for rows.Next() {
		var item WukongReminder
		var raw []byte
		var isLocate, done, needUpload bool
		if err = rows.Scan(&item.ID, &item.UserID, &item.MessageID, &item.MessageSeq,
			&item.ChannelID, &item.ChannelType, &item.Type, &isLocate, &item.Text,
			&raw, &item.Version, &done, &needUpload, &item.Publisher); err != nil {
			return nil, err
		}
		if isLocate {
			item.IsLocate = 1
		}
		if done {
			item.Done = 1
		}
		if needUpload {
			item.NeedUpload = 1
		}
		if err = json.Unmarshal(raw, &item.Data); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) DoneWukongReminders(ctx context.Context, userID string, reminderIDs []int64) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(reminderIDs) == 0 || len(reminderIDs) > 500 {
		return ErrConflict
	}
	seen := make(map[int64]struct{}, len(reminderIDs))
	ids := make([]int64, 0, len(reminderIDs))
	for _, id := range reminderIDs {
		if id <= 0 {
			return ErrConflict
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var owned int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_wukong_reminders WHERE user_id=$1 AND id=ANY($2::bigint[])`, userID, ids).Scan(&owned); err != nil {
		return err
	}
	if owned != len(ids) {
		return ErrForbidden
	}
	rows, err := tx.Query(ctx, `
		UPDATE im_wukong_reminders
		SET done=true,version=nextval('im_wukong_reminder_version_seq'),updated_at=now()
		WHERE user_id=$1 AND id=ANY($2::bigint[]) AND done=false
		RETURNING version
	`, userID, ids)
	if err != nil {
		return err
	}
	var maxVersion int64
	for rows.Next() {
		var version int64
		if err = rows.Scan(&version); err != nil {
			rows.Close()
			return err
		}
		maxVersion = max(maxVersion, version)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	if maxVersion > 0 {
		payload, marshalErr := json.Marshal(map[string]any{"reminderIds": ids})
		if marshalErr != nil {
			return marshalErr
		}
		parts := make([]string, len(ids))
		for index, id := range ids {
			parts[index] = strconv.FormatInt(id, 10)
		}
		if err = enqueueWukongBusinessEvent(ctx, tx, "reminder", strings.Join(parts, ","), "reminder.done", payload,
			[]wukongCommandRecipient{{UserID: userID}}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *WithRedis) SyncWukongReminders(ctx context.Context, userID string, version int64, limit int) ([]WukongReminder, error) {
	if reminders, ok := p.base.(WukongReminderStore); ok {
		return reminders.SyncWukongReminders(ctx, userID, version, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) DoneWukongReminders(ctx context.Context, userID string, reminderIDs []int64) error {
	if reminders, ok := p.base.(WukongReminderStore); ok {
		return reminders.DoneWukongReminders(ctx, userID, reminderIDs)
	}
	return ErrUnsupported
}

func (p *Postgres) AuthorizeWukongMessage(ctx context.Context, input WukongMessageRouteInput) (WukongMessageRoute, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	if input.UserID == "" || input.ConversationID == "" {
		return WukongMessageRoute{}, ErrForbidden
	}
	var businessType int
	businessErr := p.pool.QueryRow(ctx, `SELECT channel_type FROM im_business_channels WHERE conversation_id=$1`, input.ConversationID).Scan(&businessType)
	if businessErr == nil {
		if err := p.AuthorizeBusinessChannelSend(ctx, input.UserID, input.ConversationID, businessType, time.Now()); err != nil {
			return WukongMessageRoute{}, err
		}
		if len(input.Mentions) > 0 || input.MentionAll {
			if input.Type != "text" {
				return WukongMessageRoute{}, ErrForbidden
			}
			var role string
			if err := p.pool.QueryRow(ctx, `SELECT role FROM im_members WHERE conversation_id=$1 AND user_id=$2`, input.ConversationID, input.UserID).Scan(&role); err != nil {
				return WukongMessageRoute{}, ErrForbidden
			}
			if input.MentionAll && !businessChannelOperator(role) {
				return WukongMessageRoute{}, ErrForbidden
			}
			var mentionedMembers int
			if err := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_members WHERE conversation_id=$1 AND user_id=ANY($2::text[])`, input.ConversationID, input.Mentions).Scan(&mentionedMembers); err != nil {
				return WukongMessageRoute{}, err
			}
			if mentionedMembers != len(input.Mentions) {
				return WukongMessageRoute{}, ErrForbidden
			}
		}
		if input.Type == "text" {
			var denied bool
			if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sensitive_words WHERE split_part(value,'|',1)<>'' AND position(lower(split_part(value,'|',1)) in lower($1))>0)`, input.Text).Scan(&denied); err != nil {
				return WukongMessageRoute{}, err
			}
			if denied {
				return WukongMessageRoute{}, ErrForbidden
			}
		}
		if input.ReplyToID != "" {
			var valid bool
			if err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_wukong_message_index WHERE message_id::text=$1 AND conversation_id=$2)`, input.ReplyToID, input.ConversationID).Scan(&valid); err != nil {
				return WukongMessageRoute{}, err
			}
			if !valid {
				return WukongMessageRoute{}, ErrConflict
			}
		}
		return WukongMessageRoute{ChannelID: input.ConversationID, ChannelType: uint8(businessType)}, nil
	}
	if businessErr != nil && !errors.Is(businessErr, pgx.ErrNoRows) {
		return WukongMessageRoute{}, businessErr
	}
	var kind, role string
	var mutedUntil, allMutedUntil, dissolvedAt *time.Time
	var banned bool
	err := p.pool.QueryRow(ctx, `
		SELECT c.kind,m.role,m.muted_until,u.banned,g.all_muted_until,g.dissolved_at
		FROM im_conversations c
		JOIN im_members m ON m.conversation_id=c.id AND m.user_id=$2
		JOIN im_users u ON u.id=m.user_id
		LEFT JOIN im_groups g ON g.conversation_id=c.id
		WHERE c.id=$1
	`, input.ConversationID, input.UserID).Scan(&kind, &role, &mutedUntil, &banned, &allMutedUntil, &dissolvedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return WukongMessageRoute{}, ErrForbidden
	}
	if err != nil {
		return WukongMessageRoute{}, err
	}
	now := time.Now()
	if banned || dissolvedAt != nil || (mutedUntil != nil && mutedUntil.After(now)) ||
		(allMutedUntil != nil && allMutedUntil.After(now) && role != "owner" && role != "admin") {
		return WukongMessageRoute{}, ErrForbidden
	}
	if len(input.Mentions) > 0 || input.MentionAll {
		if input.Type != "text" || kind != "group" || (input.MentionAll && role != "owner" && role != "admin") {
			return WukongMessageRoute{}, ErrForbidden
		}
		var mentionedMembers int
		if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_members WHERE conversation_id=$1 AND user_id=ANY($2::text[])`, input.ConversationID, input.Mentions).Scan(&mentionedMembers); err != nil {
			return WukongMessageRoute{}, err
		}
		if mentionedMembers != len(input.Mentions) {
			return WukongMessageRoute{}, ErrForbidden
		}
	}
	if input.Type == "text" {
		var denied bool
		if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_sensitive_words WHERE split_part(value,'|',1)<>'' AND position(lower(split_part(value,'|',1)) in lower($1))>0)`, input.Text).Scan(&denied); err != nil {
			return WukongMessageRoute{}, err
		}
		if denied {
			return WukongMessageRoute{}, ErrForbidden
		}
	}
	if input.ReplyToID != "" {
		var valid bool
		if err = p.pool.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM im_wukong_message_index WHERE message_id::text=$1 AND conversation_id=$2
			UNION ALL
			SELECT 1 FROM im_messages WHERE id=$1 AND conversation_id=$2
		)`, input.ReplyToID, input.ConversationID).Scan(&valid); err != nil {
			return WukongMessageRoute{}, err
		}
		if !valid {
			return WukongMessageRoute{}, ErrConflict
		}
	}
	if kind == "group" {
		return WukongMessageRoute{ChannelID: input.ConversationID, ChannelType: wukong.ChannelGroup}, nil
	}
	if kind != "direct" {
		return WukongMessageRoute{}, ErrUnsupported
	}
	var otherID string
	var otherCount int
	if err = p.pool.QueryRow(ctx, `SELECT COALESCE(min(user_id),''),count(*) FROM im_members WHERE conversation_id=$1 AND user_id<>$2`, input.ConversationID, input.UserID).Scan(&otherID, &otherCount); err != nil {
		return WukongMessageRoute{}, err
	}
	if otherCount != 1 || otherID == "" {
		return WukongMessageRoute{}, ErrForbidden
	}
	var blocked bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_blocks WHERE (user_id=$1 AND blocked_user_id=$2) OR (user_id=$2 AND blocked_user_id=$1))`, input.UserID, otherID).Scan(&blocked); err != nil {
		return WukongMessageRoute{}, err
	}
	if blocked {
		return WukongMessageRoute{}, ErrForbidden
	}
	var friends bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_friendships WHERE user_id=$1 AND friend_user_id=$2)`, input.UserID, otherID).Scan(&friends); err != nil {
		return WukongMessageRoute{}, err
	}
	if !friends {
		return WukongMessageRoute{}, ErrForbidden
	}
	return WukongMessageRoute{ChannelID: otherID, ChannelType: wukong.ChannelPerson}, nil
}

func (p *WithRedis) AuthorizeWukongMessage(ctx context.Context, input WukongMessageRouteInput) (WukongMessageRoute, error) {
	if routes, ok := p.base.(WukongMessageRouteStore); ok {
		return routes.AuthorizeWukongMessage(ctx, input)
	}
	return WukongMessageRoute{}, ErrUnsupported
}

func (p *Postgres) AuthorizeWukongClientMessage(ctx context.Context, input WukongClientMessageInput) (WukongMessageRoute, error) {
	input.UserID = strings.TrimSpace(input.UserID)
	input.ChannelID = strings.TrimSpace(input.ChannelID)
	if input.UserID == "" || input.ChannelID == "" || input.UserID == input.ChannelID && input.ChannelType == wukong.ChannelPerson {
		return WukongMessageRoute{}, ErrForbidden
	}
	conversationID := input.ChannelID
	switch input.ChannelType {
	case wukong.ChannelPerson:
		err := p.pool.QueryRow(ctx, `
			SELECT conversation.id
			FROM im_conversations conversation
			JOIN im_members sender ON sender.conversation_id=conversation.id AND sender.user_id=$1
			JOIN im_members recipient ON recipient.conversation_id=conversation.id AND recipient.user_id=$2
			WHERE conversation.kind='direct'
			  AND (SELECT count(*) FROM im_members member WHERE member.conversation_id=conversation.id)=2
			ORDER BY conversation.created_at,conversation.id LIMIT 1
		`, input.UserID, input.ChannelID).Scan(&conversationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return WukongMessageRoute{}, ErrForbidden
		}
		if err != nil {
			return WukongMessageRoute{}, err
		}
	case wukong.ChannelGroup, wukong.ChannelCustomer, wukong.ChannelCommunity, wukong.ChannelCommunityTopic,
		wukong.ChannelInfo, wukong.ChannelLive, wukong.ChannelVisitor:
	default:
		return WukongMessageRoute{}, ErrUnsupported
	}
	route, err := p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{
		UserID: input.UserID, ConversationID: conversationID, Type: input.Type,
		ReplyToID: input.ReplyToID, Text: input.Text,
		Mentions: input.Mentions, MentionAll: input.MentionAll,
	})
	if err != nil {
		return WukongMessageRoute{}, err
	}
	if route.ChannelID != input.ChannelID || route.ChannelType != input.ChannelType {
		return WukongMessageRoute{}, ErrForbidden
	}
	if input.Type == "moment" {
		allowed, accessErr := p.CanAccessMoment(ctx, input.UserID, input.ResourceID)
		if accessErr != nil {
			return WukongMessageRoute{}, accessErr
		}
		if input.ResourceID == "" || !allowed {
			return WukongMessageRoute{}, ErrForbidden
		}
	}
	if input.Type == "sticker" {
		allowed, accessErr := p.CanUseSticker(ctx, input.UserID, input.ResourceID)
		if accessErr != nil {
			return WukongMessageRoute{}, accessErr
		}
		if input.ResourceID == "" || !allowed {
			return WukongMessageRoute{}, ErrForbidden
		}
	}
	return route, nil
}

func (p *WithRedis) AuthorizeWukongClientMessage(ctx context.Context, input WukongClientMessageInput) (WukongMessageRoute, error) {
	if policy, ok := p.base.(WukongClientMessagePolicyStore); ok {
		return policy.AuthorizeWukongClientMessage(ctx, input)
	}
	return WukongMessageRoute{}, ErrUnsupported
}

func (p *Postgres) ResolveWukongChannel(ctx context.Context, userID, conversationID string) (WukongMessageRoute, error) {
	userID, conversationID = strings.TrimSpace(userID), strings.TrimSpace(conversationID)
	if userID == "" || conversationID == "" {
		return WukongMessageRoute{}, ErrForbidden
	}
	var businessType uint8
	businessErr := p.pool.QueryRow(ctx, `SELECT channel.channel_type FROM im_business_channels channel
		JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$2
			AND (member.expires_at IS NULL OR member.expires_at>now())
		WHERE channel.conversation_id=$1 AND NOT channel.disband`, conversationID, userID).Scan(&businessType)
	if businessErr == nil {
		return WukongMessageRoute{ChannelID: conversationID, ChannelType: businessType}, nil
	}
	if businessErr != nil && !errors.Is(businessErr, pgx.ErrNoRows) {
		return WukongMessageRoute{}, businessErr
	}
	var kind string
	err := p.pool.QueryRow(ctx, `
		SELECT conversation.kind FROM im_conversations conversation
		JOIN im_members member ON member.conversation_id=conversation.id AND member.user_id=$2
		WHERE conversation.id=$1
	`, conversationID, userID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return WukongMessageRoute{}, ErrForbidden
	}
	if err != nil {
		return WukongMessageRoute{}, err
	}
	if kind == "group" {
		return WukongMessageRoute{ChannelID: conversationID, ChannelType: wukong.ChannelGroup}, nil
	}
	if kind != "direct" {
		return WukongMessageRoute{}, ErrUnsupported
	}
	var peerID string
	var peerCount int
	if err = p.pool.QueryRow(ctx, `SELECT COALESCE(min(user_id),''),count(*) FROM im_members WHERE conversation_id=$1 AND user_id<>$2`, conversationID, userID).Scan(&peerID, &peerCount); err != nil {
		return WukongMessageRoute{}, err
	}
	if peerCount != 1 || peerID == "" {
		return WukongMessageRoute{}, ErrForbidden
	}
	return WukongMessageRoute{ChannelID: peerID, ChannelType: wukong.ChannelPerson}, nil
}

func (p *WithRedis) ResolveWukongChannel(ctx context.Context, userID, conversationID string) (WukongMessageRoute, error) {
	if routes, ok := p.base.(WukongChannelRouteStore); ok {
		return routes.ResolveWukongChannel(ctx, userID, conversationID)
	}
	return WukongMessageRoute{}, ErrUnsupported
}

func (p *Postgres) resolveWukongConversationID(ctx context.Context, userID, channelID string, channelType uint8) (string, error) {
	userID, channelID = strings.TrimSpace(userID), strings.TrimSpace(channelID)
	if userID == "" || channelID == "" {
		return "", ErrForbidden
	}
	var conversationID string
	switch channelType {
	case wukong.ChannelGroup:
		err := p.pool.QueryRow(ctx, `SELECT conversation.id FROM im_conversations conversation
			JOIN im_members member ON member.conversation_id=conversation.id AND member.user_id=$2
			WHERE conversation.id=$1 AND conversation.kind='group'`, channelID, userID).Scan(&conversationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		return conversationID, err
	case wukong.ChannelPerson:
		if channelID == userID {
			return "", ErrForbidden
		}
		err := p.pool.QueryRow(ctx, `SELECT conversation.id FROM im_conversations conversation
			JOIN im_members own_member ON own_member.conversation_id=conversation.id AND own_member.user_id=$1
			JOIN im_members peer_member ON peer_member.conversation_id=conversation.id AND peer_member.user_id=$2
			WHERE conversation.kind='direct' AND conversation.member_count=2
			ORDER BY conversation.created_at LIMIT 1`, userID, channelID).Scan(&conversationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		return conversationID, err
	case wukong.ChannelCommunity, wukong.ChannelCommunityTopic, wukong.ChannelInfo, wukong.ChannelLive,
		wukong.ChannelCustomer, wukong.ChannelVisitor:
		err := p.pool.QueryRow(ctx, `SELECT channel.conversation_id FROM im_business_channels channel
			JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$2
				AND (member.expires_at IS NULL OR member.expires_at>now())
			WHERE channel.conversation_id=$1 AND channel.channel_type=$3 AND NOT channel.disband`, channelID, userID, channelType).Scan(&conversationID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		return conversationID, err
	default:
		return "", ErrUnsupported
	}
}

func (p *Postgres) LoadWukongChannelInfo(ctx context.Context, userID, channelID string, channelType uint8) (WukongChannelInfo, error) {
	conversationID, err := p.resolveWukongConversationID(ctx, userID, channelID, channelType)
	if err != nil {
		return WukongChannelInfo{}, err
	}
	info := WukongChannelInfo{
		ChannelID: channelID, ChannelType: channelType, Status: 1, Follow: 1,
		Save: 1, Receipt: 1, Extra: map[string]any{"conversationId": conversationID},
	}
	var notificationsMuted, pinned bool
	switch channelType {
	case wukong.ChannelPerson:
		var userUpdated, conversationUpdated, friendshipUpdated time.Time
		var banned bool
		err = p.pool.QueryRow(ctx, `SELECT target.name,target.avatar_url,target.banned,target.created_at,target.updated_at,
			friendship.remark,friendship.updated_at,conversation.updated_at,member.notifications_muted,member.pinned
			FROM im_users target
			JOIN im_friendships friendship ON friendship.user_id=$1 AND friendship.friend_user_id=target.id
			JOIN im_conversations conversation ON conversation.id=$3
			JOIN im_members member ON member.conversation_id=conversation.id AND member.user_id=$1
			WHERE target.id=$2`, userID, channelID, conversationID).Scan(
			&info.Name, &info.AvatarURL, &banned, &info.CreatedAt, &userUpdated,
			&info.Remark, &friendshipUpdated, &conversationUpdated, &notificationsMuted, &pinned,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return WukongChannelInfo{}, ErrForbidden
		}
		if err != nil {
			return WukongChannelInfo{}, err
		}
		info.UpdatedAt = userUpdated
		for _, candidate := range []time.Time{friendshipUpdated, conversationUpdated} {
			if candidate.After(info.UpdatedAt) {
				info.UpdatedAt = candidate
			}
		}
		if banned {
			info.Status = 0
			info.Forbidden = 1
		}
		var presenceOnline bool
		var presenceUpdated time.Time
		var lastOffline *time.Time
		presenceErr := p.pool.QueryRow(ctx, `SELECT online,last_offline_at,updated_at FROM im_wukong_presence WHERE user_id=$1`, channelID).Scan(
			&presenceOnline, &lastOffline, &presenceUpdated,
		)
		if presenceErr != nil && !errors.Is(presenceErr, pgx.ErrNoRows) {
			return WukongChannelInfo{}, presenceErr
		}
		if presenceErr == nil {
			if presenceOnline {
				info.Online = 1
			}
			if lastOffline != nil {
				info.LastOffline = lastOffline.Unix()
			}
			if presenceUpdated.After(info.UpdatedAt) {
				info.UpdatedAt = presenceUpdated
			}
		}
		info.Extra["userId"] = channelID
	case wukong.ChannelGroup:
		var ownerID string
		var allMutedUntil *time.Time
		var memberCount int
		err = p.pool.QueryRow(ctx, `SELECT conversation.title,conversation.avatar_url,conversation.created_at,
			GREATEST(conversation.updated_at,group_row.updated_at),group_row.owner_id,group_row.all_muted_until,
			member.notifications_muted,member.pinned,conversation.member_count
			FROM im_conversations conversation JOIN im_groups group_row ON group_row.conversation_id=conversation.id
			JOIN im_members member ON member.conversation_id=conversation.id AND member.user_id=$2
			WHERE conversation.id=$1 AND conversation.kind='group'`, conversationID, userID).Scan(
			&info.Name, &info.AvatarURL, &info.CreatedAt, &info.UpdatedAt, &ownerID, &allMutedUntil,
			&notificationsMuted, &pinned, &memberCount,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return WukongChannelInfo{}, ErrForbidden
		}
		if err != nil {
			return WukongChannelInfo{}, err
		}
		info.ShowNick = 1
		if allMutedUntil != nil && allMutedUntil.After(time.Now()) {
			info.Forbidden = 1
			info.Extra["allMutedUntil"] = allMutedUntil.UTC().Format(time.RFC3339Nano)
		}
		info.Extra["ownerId"] = ownerID
		info.Extra["memberCount"] = memberCount
	case wukong.ChannelCommunity, wukong.ChannelCommunityTopic, wukong.ChannelInfo, wukong.ChannelLive,
		wukong.ChannelCustomer, wukong.ChannelVisitor:
		var ownerID, parentID, description, visibility, joinPolicy, postingPolicy string
		var metadata []byte
		var memberCount, slowMode int
		var ban, disband, sendBan bool
		var mutedUntil *time.Time
		err = p.pool.QueryRow(ctx, `SELECT conversation.title,conversation.avatar_url,channel.category,
			channel.owner_id,COALESCE(channel.parent_id,''),channel.description,channel.visibility,
			channel.join_policy,channel.posting_policy,channel.slow_mode_seconds,
			channel.ban,channel.disband,channel.send_ban,channel.metadata,
			channel.created_at,channel.updated_at,conversation.member_count,
			member.notifications_muted,member.pinned,member.muted_until
			FROM im_business_channels channel JOIN im_conversations conversation ON conversation.id=channel.conversation_id
			JOIN im_members member ON member.conversation_id=channel.conversation_id AND member.user_id=$2
			WHERE channel.conversation_id=$1 AND channel.channel_type=$3`, conversationID, userID, channelType).Scan(
			&info.Name, &info.AvatarURL, &info.Category, &ownerID, &parentID, &description, &visibility,
			&joinPolicy, &postingPolicy, &slowMode, &ban, &disband, &sendBan, &metadata,
			&info.CreatedAt, &info.UpdatedAt, &memberCount, &notificationsMuted, &pinned, &mutedUntil)
		if errors.Is(err, pgx.ErrNoRows) {
			return WukongChannelInfo{}, ErrForbidden
		}
		if err != nil {
			return WukongChannelInfo{}, err
		}
		if ban || disband || sendBan || (mutedUntil != nil && mutedUntil.After(time.Now())) {
			info.Forbidden = 1
		}
		if disband {
			info.Status = 0
		}
		if joinPolicy == "open" {
			info.Invite = 1
		}
		info.ShowNick = 1
		info.Extra["ownerId"] = ownerID
		info.Extra["parentChannelId"] = parentID
		info.Extra["description"] = description
		info.Extra["visibility"] = visibility
		info.Extra["joinPolicy"] = joinPolicy
		info.Extra["postingPolicy"] = postingPolicy
		info.Extra["slowModeSeconds"] = slowMode
		info.Extra["memberCount"] = memberCount
		var decoded map[string]any
		if err = json.Unmarshal(metadata, &decoded); err != nil {
			return WukongChannelInfo{}, err
		}
		info.Extra["metadata"] = decoded
	}
	if notificationsMuted {
		info.Mute = 1
	}
	if pinned {
		info.Top = 1
	}
	info.Version = info.UpdatedAt.UnixMicro()
	return info, nil
}

func (p *Postgres) SyncWukongChannelMembers(ctx context.Context, userID, channelID string, channelType uint8, version int64, limit int) ([]WukongChannelMember, error) {
	if version < 0 {
		return nil, ErrConflict
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	conversationID, err := p.resolveWukongConversationID(ctx, userID, channelID, channelType)
	if err != nil {
		return nil, err
	}
	var rows pgx.Rows
	if version == 0 {
		rows, err = p.pool.Query(ctx, `SELECT member.user_id,user_row.name,member.group_nickname,user_row.avatar_url,
			member.role,user_row.banned,false,event.version,member.joined_at,
			GREATEST(user_row.updated_at,event.updated_at),member.muted_until,COALESCE(user_row.handle,'')
			FROM im_members member JOIN im_users user_row ON user_row.id=member.user_id
			JOIN LATERAL (SELECT version,updated_at FROM im_wukong_channel_member_events
				WHERE conversation_id=member.conversation_id AND user_id=member.user_id AND NOT is_deleted
				ORDER BY version DESC LIMIT 1) event ON true
			WHERE member.conversation_id=$1 AND (member.expires_at IS NULL OR member.expires_at>now())
			ORDER BY event.version LIMIT $2`, conversationID, limit)
	} else {
		rows, err = p.pool.Query(ctx, `SELECT event.user_id,user_row.name,event.group_nickname,user_row.avatar_url,
			event.role,user_row.banned,event.is_deleted,event.version,event.created_at,
			GREATEST(user_row.updated_at,event.updated_at),event.muted_until,COALESCE(user_row.handle,'')
			FROM im_wukong_channel_member_events event JOIN im_users user_row ON user_row.id=event.user_id
			WHERE event.conversation_id=$1 AND event.version>$2 ORDER BY event.version LIMIT $3`, conversationID, version, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WukongChannelMember, 0, limit)
	for rows.Next() {
		item := WukongChannelMember{ChannelID: channelID, ChannelType: channelType, Extra: map[string]any{}}
		var banned, deleted bool
		var mutedUntil *time.Time
		var handle string
		if err = rows.Scan(&item.UserID, &item.Name, &item.Remark, &item.AvatarURL, &item.Role,
			&banned, &deleted, &item.Version, &item.CreatedAt, &item.UpdatedAt, &mutedUntil, &handle); err != nil {
			return nil, err
		}
		if banned {
			item.Status = 1
		}
		if deleted {
			item.Deleted = 1
		}
		if mutedUntil != nil {
			item.Extra["mutedUntil"] = mutedUntil.UTC().Format(time.RFC3339Nano)
		}
		item.Extra["handle"] = handle
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *WithRedis) LoadWukongChannelInfo(ctx context.Context, userID, channelID string, channelType uint8) (WukongChannelInfo, error) {
	if channels, ok := p.base.(WukongChannelDataStore); ok {
		return channels.LoadWukongChannelInfo(ctx, userID, channelID, channelType)
	}
	return WukongChannelInfo{}, ErrUnsupported
}

func (p *WithRedis) SyncWukongChannelMembers(ctx context.Context, userID, channelID string, channelType uint8, version int64, limit int) ([]WukongChannelMember, error) {
	if channels, ok := p.base.(WukongChannelDataStore); ok {
		return channels.SyncWukongChannelMembers(ctx, userID, channelID, channelType, version, limit)
	}
	return nil, ErrUnsupported
}

func (p *Postgres) ListWukongForwardMessageRefs(ctx context.Context, userID string, messageIDs []string) ([]WukongMessageRef, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" || len(messageIDs) == 0 || len(messageIDs) > 100 {
		return nil, ErrForbidden
	}
	rows, err := p.pool.Query(ctx, `
		SELECT i.message_id::text,i.conversation_id,
			CASE WHEN i.channel_type=1 THEN (
				SELECT other.user_id FROM im_members other
				WHERE other.conversation_id=i.conversation_id AND other.user_id<>$1
				ORDER BY other.user_id LIMIT 1
			) ELSE i.channel_id END,
			i.channel_type
		FROM im_wukong_message_index i
		LEFT JOIN im_members allowed ON allowed.conversation_id=i.conversation_id AND allowed.user_id=$1
		WHERE i.message_id::text=ANY($2::text[])
			AND (allowed.user_id IS NOT NULL OR EXISTS(
				SELECT 1 FROM im_favorites favorite WHERE favorite.user_id=$1 AND favorite.message_id=i.message_id::text
			))
	`, userID, messageIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byID := make(map[string]WukongMessageRef, len(messageIDs))
	for rows.Next() {
		var ref WukongMessageRef
		if err = rows.Scan(&ref.MessageID, &ref.ConversationID, &ref.ChannelID, &ref.ChannelType); err != nil {
			return nil, err
		}
		if strings.TrimSpace(ref.ChannelID) == "" {
			return nil, ErrForbidden
		}
		byID[ref.MessageID] = ref
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	ordered := make([]WukongMessageRef, 0, len(messageIDs))
	for _, messageID := range messageIDs {
		ref, exists := byID[messageID]
		if !exists {
			return nil, ErrForbidden
		}
		ordered = append(ordered, ref)
	}
	return ordered, nil
}

func (p *WithRedis) ListWukongForwardMessageRefs(ctx context.Context, userID string, messageIDs []string) ([]WukongMessageRef, error) {
	if sources, ok := p.base.(WukongForwardSourceStore); ok {
		return sources.ListWukongForwardMessageRefs(ctx, userID, messageIDs)
	}
	return nil, ErrUnsupported
}

func shouldSendWukongBusinessEvent(event string) bool {
	event = strings.TrimSpace(event)
	return event != "" && event != "message.created" && !strings.HasPrefix(event, "call.")
}

func enqueueWukongBusinessEvent(ctx context.Context, tx pgx.Tx, aggregateType, aggregateID, event string, rawPayload []byte, recipients []wukongCommandRecipient) error {
	if !shouldSendWukongBusinessEvent(event) || len(recipients) == 0 {
		return nil
	}
	param := map[string]any{
		"schemaVersion": 1,
		"event":         event,
	}
	if len(rawPayload) != 0 {
		var decoded any
		if err := json.Unmarshal(rawPayload, &decoded); err != nil {
			return fmt.Errorf("decode %s event payload: %w", event, err)
		}
		param["payload"] = decoded
	}

	clean := make([]wukongCommandRecipient, 0, len(recipients))
	seen := make(map[string]struct{}, len(recipients))
	for _, recipient := range recipients {
		recipient.UserID = strings.TrimSpace(recipient.UserID)
		if recipient.UserID == "" {
			continue
		}
		if _, exists := seen[recipient.UserID]; exists {
			continue
		}
		seen[recipient.UserID] = struct{}{}
		clean = append(clean, recipient)
	}
	sort.Slice(clean, func(i, j int) bool { return clean[i].UserID < clean[j].UserID })
	payloadDigest := sha256.Sum256(rawPayload)
	for offset := 0; offset < len(clean); offset += wukong.MaxCommandRecipients {
		end := min(offset+wukong.MaxCommandRecipients, len(clean))
		chunk := clean[offset:end]
		uids := make([]string, 0, len(chunk))
		keyMaterial := strings.Builder{}
		keyMaterial.WriteString(event)
		keyMaterial.WriteByte('|')
		keyMaterial.WriteString(aggregateType)
		keyMaterial.WriteByte('|')
		keyMaterial.WriteString(aggregateID)
		fmt.Fprintf(&keyMaterial, "|%x", payloadDigest[:])
		for _, recipient := range chunk {
			uids = append(uids, recipient.UserID)
			keyMaterial.WriteByte('|')
			keyMaterial.WriteString(recipient.UserID)
		}
		digest := sha256.Sum256([]byte(keyMaterial.String()))
		if err := enqueueWukongOutbox(ctx, tx,
			fmt.Sprintf("business-event:%x", digest[:]),
			wukong.OperationBusinessEvent, aggregateType, aggregateID,
			wukong.CommandPayload{Recipients: uids, Event: event, Param: param},
		); err != nil {
			return err
		}
	}
	return nil
}

func appendAnnouncementAudienceEvent(ctx context.Context, tx pgx.Tx, announcement *model.Announcement, event string, _ time.Time) error {
	if announcement == nil || !shouldSendWukongBusinessEvent(event) {
		return nil
	}
	payload, err := json.Marshal(map[string]any{"announcement": announcement})
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		SELECT id FROM im_users
		WHERE deleted_at IS NULL AND NOT banned AND ($1='all' OR id=ANY($2::text[]))
		ORDER BY id
	`, announcement.TargetType, announcement.TargetUserIDs)
	if err != nil {
		return err
	}
	recipients := make([]wukongCommandRecipient, 0)
	for rows.Next() {
		var recipient wukongCommandRecipient
		if err = rows.Scan(&recipient.UserID); err != nil {
			rows.Close()
			return err
		}
		recipients = append(recipients, recipient)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	return enqueueWukongBusinessEvent(ctx, tx, "announcement", announcement.ID, event, payload, recipients)
}

func enqueueWukongChannelReconcile(ctx context.Context, tx pgx.Tx, channelID, reason string, at time.Time) error {
	return enqueueWukongChannelReconcileTyped(ctx, tx, channelID, wukong.ChannelGroup, reason, at)
}

func enqueueWukongChannelReconcileTyped(ctx context.Context, tx pgx.Tx, channelID string, channelType uint8, reason string, at time.Time) error {
	return enqueueWukongOutbox(ctx, tx,
		fmt.Sprintf("channel-reconcile:%d:%s:%s:%d", channelType, channelID, reason, at.UnixNano()),
		wukong.OperationChannelReconcile, "channel", fmt.Sprintf("%d:%s", channelType, channelID),
		map[string]any{"channel_id": channelID, "channel_type": channelType},
	)
}

func enqueueWukongCallEvent(ctx context.Context, tx pgx.Tx, call *model.CallSession, event string, recipients []string) error {
	if call == nil || event == "" || len(recipients) == 0 {
		return nil
	}
	return enqueueWukongOutbox(
		ctx,
		tx,
		fmt.Sprintf("call-event:%s:%s:%d", call.ID, event, call.UpdatedAt.UnixNano()),
		wukong.OperationCallEvent,
		"call",
		call.ID,
		wukong.CallEventPayload{
			Recipients: recipients,
			Event:      event,
			Param: map[string]any{
				"schemaVersion":  1,
				"contentType":    wukong.ContentTypeCallEvent,
				"event":          event,
				"call":           call,
				"callId":         call.ID,
				"conversationId": call.ConversationID,
				"mediaType":      call.MediaType,
				"status":         call.Status,
				"endReason":      call.EndReason,
			},
		},
	)
}

func enqueueWukongOutbox(ctx context.Context, tx pgx.Tx, idempotencyKey, operation, aggregateType, aggregateID string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO im_wukong_outbox(idempotency_key,operation,aggregate_type,aggregate_id,payload)
		VALUES($1,$2,$3,$4,$5)
		ON CONFLICT(idempotency_key) DO NOTHING
	`, idempotencyKey, operation, aggregateType, aggregateID, encoded)
	return err
}

func (p *Postgres) WukongCredentialProvisioned(ctx context.Context, uid string, deviceFlag, deviceLevel int, tokenDigest string) (bool, error) {
	var provisioned bool
	err := p.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM im_wukong_credentials
			WHERE user_id=$1 AND device_flag=$2 AND device_level=$3 AND token_digest=$4
		)
	`, uid, deviceFlag, deviceLevel, tokenDigest).Scan(&provisioned)
	return provisioned, err
}

func (p *Postgres) MarkWukongCredentialProvisioned(ctx context.Context, uid string, deviceFlag, deviceLevel int, tokenDigest string) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO im_wukong_credentials(user_id,device_flag,device_level,token_digest,provisioned_at)
		VALUES($1,$2,$3,$4,now())
		ON CONFLICT(user_id,device_flag) DO UPDATE SET
			device_level=excluded.device_level,
			token_digest=excluded.token_digest,
			provisioned_at=excluded.provisioned_at
	`, uid, deviceFlag, deviceLevel, tokenDigest)
	return err
}

func (p *Postgres) InvalidateWukongCredential(ctx context.Context, uid string, deviceFlag int) error {
	if deviceFlag == -1 {
		_, err := p.pool.Exec(ctx, `DELETE FROM im_wukong_credentials WHERE user_id=$1`, uid)
		return err
	}
	_, err := p.pool.Exec(ctx, `DELETE FROM im_wukong_credentials WHERE user_id=$1 AND device_flag=$2`, uid, deviceFlag)
	return err
}

func (p *WithRedis) WukongCredentialProvisioned(ctx context.Context, uid string, deviceFlag, deviceLevel int, tokenDigest string) (bool, error) {
	if credentials, ok := p.base.(wukong.CredentialProvisionStore); ok {
		return credentials.WukongCredentialProvisioned(ctx, uid, deviceFlag, deviceLevel, tokenDigest)
	}
	return false, ErrUnsupported
}

func (p *WithRedis) MarkWukongCredentialProvisioned(ctx context.Context, uid string, deviceFlag, deviceLevel int, tokenDigest string) error {
	if credentials, ok := p.base.(wukong.CredentialProvisionStore); ok {
		return credentials.MarkWukongCredentialProvisioned(ctx, uid, deviceFlag, deviceLevel, tokenDigest)
	}
	return ErrUnsupported
}

func (p *WithRedis) InvalidateWukongCredential(ctx context.Context, uid string, deviceFlag int) error {
	if credentials, ok := p.base.(wukong.CredentialProvisionStore); ok {
		return credentials.InvalidateWukongCredential(ctx, uid, deviceFlag)
	}
	return ErrUnsupported
}

func (p *Postgres) PutWukongWebhookEvent(ctx context.Context, event wukong.WebhookEvent) (bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	result, err := tx.Exec(ctx, `
		INSERT INTO im_wukong_webhook_events(id,event_type,payload,received_at)
		VALUES($1,$2,$3,$4)
		ON CONFLICT(id) DO NOTHING
	`, event.ID, event.EventType, event.Payload, event.ReceivedAt)
	if err != nil {
		return false, err
	}
	inserted := result.RowsAffected() == 1
	if !inserted {
		return false, tx.Commit(ctx)
	}
	if err = processWukongWebhookEvent(ctx, tx, event); err != nil {
		return false, err
	}
	if err = completeWukongWebhookEvent(ctx, tx, event.ID, ""); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

func processWukongWebhookEvent(ctx context.Context, tx pgx.Tx, event wukong.WebhookEvent) error {
	switch event.EventType {
	case wukong.EventMessageNotify:
		return indexWukongMessage(ctx, tx, event)
	case wukong.EventMessageOffline:
		return enqueueWukongOfflinePush(ctx, tx, event)
	case wukong.EventOnlineStatus:
		return projectWukongOnlineStatus(ctx, tx, event)
	case wukong.EventMessageStream:
		// Stream content is owned by WuKongIM's event store. The business
		// service only retains this event id as an idempotency receipt.
		return nil
	default:
		return fmt.Errorf("unsupported WuKongIM webhook event %q", event.EventType)
	}
}

func completeWukongWebhookEvent(ctx context.Context, tx pgx.Tx, eventID, lastError string) error {
	if lastError == "" {
		_, err := tx.Exec(ctx, `UPDATE im_wukong_webhook_events
			SET status='completed',payload='{}'::jsonb,completed_at=now(),locked_at=NULL,last_error=''
			WHERE id=$1`, eventID)
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE im_wukong_webhook_events
		SET status='failed',attempts=attempts+1,payload='{}'::jsonb,completed_at=now(),locked_at=NULL,last_error=left($2,500)
		WHERE id=$1`, eventID, lastError)
	return err
}

func (p *Postgres) recoverWukongWebhookEvents(ctx context.Context, maximum int) error {
	if maximum <= 0 {
		return nil
	}
	for recovered := 0; recovered < maximum; {
		batch := min(200, maximum-recovered)
		tx, err := p.pool.Begin(ctx)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `
			SELECT id,event_type,payload,received_at FROM im_wukong_webhook_events
			WHERE status='pending' OR (status='processing' AND COALESCE(locked_at,received_at)<now()-interval '5 minutes')
			ORDER BY received_at,id FOR UPDATE SKIP LOCKED LIMIT $1
		`, batch)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		events := make([]wukong.WebhookEvent, 0, batch)
		for rows.Next() {
			var event wukong.WebhookEvent
			if err = rows.Scan(&event.ID, &event.EventType, &event.Payload, &event.ReceivedAt); err != nil {
				rows.Close()
				tx.Rollback(ctx)
				return err
			}
			events = append(events, event)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		for index, event := range events {
			savepoint := "webhook_recovery_" + strconv.Itoa(index)
			if _, err = tx.Exec(ctx, "SAVEPOINT "+savepoint); err != nil {
				tx.Rollback(ctx)
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE im_wukong_webhook_events SET status='processing',attempts=attempts+1,locked_at=now() WHERE id=$1`, event.ID)
			if err == nil {
				err = processWukongWebhookEvent(ctx, tx, event)
			}
			if err != nil {
				processingError := err.Error()
				if _, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+savepoint); rollbackErr != nil {
					tx.Rollback(ctx)
					return rollbackErr
				}
				if completeErr := completeWukongWebhookEvent(ctx, tx, event.ID, processingError); completeErr != nil {
					tx.Rollback(ctx)
					return completeErr
				}
			} else if err = completeWukongWebhookEvent(ctx, tx, event.ID, ""); err != nil {
				tx.Rollback(ctx)
				return err
			}
			if _, err = tx.Exec(ctx, "RELEASE SAVEPOINT "+savepoint); err != nil {
				tx.Rollback(ctx)
				return err
			}
		}
		if err = tx.Commit(ctx); err != nil {
			return err
		}
		recovered += len(events)
		if len(events) < batch {
			return nil
		}
	}
	return nil
}

type wukongMessageNotification struct {
	Header struct {
		NoPersist int `json:"no_persist"`
		RedDot    int `json:"red_dot"`
		SyncOnce  int `json:"sync_once"`
	} `json:"header"`
	MessageID    int64  `json:"message_id"`
	MessageIDStr string `json:"message_idstr"`
	ClientMsgNo  string `json:"client_msg_no"`
	MessageSeq   uint64 `json:"message_seq"`
	FromUID      string `json:"from_uid"`
	ChannelID    string `json:"channel_id"`
	ChannelType  uint8  `json:"channel_type"`
	Topic        string `json:"topic"`
	Expire       uint32 `json:"expire"`
	Timestamp    int64  `json:"timestamp"`
	Payload      []byte `json:"payload"`
}

type wukongOfflineNotification struct {
	wukongMessageNotification
	ToUIDs           []string `json:"to_uids"`
	Compress         string   `json:"compress"`
	CompressedToUIDs []byte   `json:"compress_to_uids"`
}

func enqueueWukongOfflinePush(ctx context.Context, tx pgx.Tx, event wukong.WebhookEvent) error {
	var message wukongOfflineNotification
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return fmt.Errorf("decode WuKongIM msg.offline: %w", err)
	}
	if message.Header.NoPersist == 1 || message.Header.SyncOnce == 1 || message.Header.RedDot != 1 {
		return nil
	}
	if message.MessageID == 0 || strings.TrimSpace(message.FromUID) == "" ||
		strings.TrimSpace(message.ChannelID) == "" || !json.Valid(message.Payload) {
		return errors.New("WuKongIM msg.offline is incomplete")
	}
	var content struct {
		Type int `json:"type"`
	}
	if err := json.Unmarshal(message.Payload, &content); err != nil || content.Type <= 0 {
		return errors.New("WuKongIM msg.offline content type is invalid")
	}
	recipients, err := wukongOfflineRecipients(message)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return nil
	}
	messageID := strings.TrimSpace(message.MessageIDStr)
	if messageID == "" {
		messageID = strconv.FormatInt(message.MessageID, 10)
	}
	messageType := wukongPushMessageType(content.Type)
	if message.ChannelType == wukong.ChannelPerson {
		_, err = tx.Exec(ctx, `
			WITH recipients(uid) AS (SELECT DISTINCT unnest($1::text[])), routes AS (
				SELECT recipient.uid,conversation.id AS conversation_id
				FROM recipients recipient
				JOIN im_users target ON target.id=recipient.uid AND NOT target.banned
				JOIN LATERAL (
					SELECT c.id FROM im_conversations c
					JOIN im_members sender ON sender.conversation_id=c.id AND sender.user_id=$2
					JOIN im_members receiver ON receiver.conversation_id=c.id AND receiver.user_id=recipient.uid
					WHERE c.kind='direct' AND c.member_count=2 AND NOT receiver.notifications_muted
					ORDER BY c.created_at LIMIT 1
				) conversation ON true
			)
			INSERT INTO im_push_outbox(user_id,event_type,payload)
			SELECT uid,'message.created',jsonb_build_object('message',jsonb_build_object(
				'id',$3::text,'conversationId',conversation_id,'type',$4::text))
			FROM routes
		`, recipients, message.FromUID, messageID, messageType)
		return err
	}
	if !wukong.SupportedChannelType(message.ChannelType) {
		return errors.New("WuKongIM msg.offline channel type is invalid")
	}
	_, err = tx.Exec(ctx, `
		WITH recipients(uid) AS (SELECT DISTINCT unnest($1::text[]))
		INSERT INTO im_push_outbox(user_id,event_type,payload)
		SELECT recipient.uid,'message.created',jsonb_build_object('message',jsonb_build_object(
			'id',$3::text,'conversationId',$2::text,'type',$4::text))
		FROM recipients recipient
		JOIN im_users target ON target.id=recipient.uid AND NOT target.banned
		JOIN im_conversations conversation ON conversation.id=$2
		JOIN im_members member ON member.conversation_id=conversation.id AND member.user_id=recipient.uid
			AND NOT member.notifications_muted AND (member.expires_at IS NULL OR member.expires_at>now())
	`, recipients, message.ChannelID, messageID, messageType)
	return err
}

func wukongOfflineRecipients(message wukongOfflineNotification) ([]string, error) {
	recipients := message.ToUIDs
	switch strings.TrimSpace(message.Compress) {
	case "":
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(message.CompressedToUIDs))
		if err != nil {
			return nil, fmt.Errorf("open WuKongIM compressed offline recipients: %w", err)
		}
		decoded, readErr := io.ReadAll(io.LimitReader(reader, 8<<20))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read WuKongIM compressed offline recipients: %w", readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close WuKongIM compressed offline recipients: %w", closeErr)
		}
		if err = json.Unmarshal(decoded, &recipients); err != nil {
			return nil, fmt.Errorf("decode WuKongIM compressed offline recipients: %w", err)
		}
	default:
		return nil, errors.New("unsupported WuKongIM offline recipient compression")
	}
	seen := make(map[string]struct{}, len(recipients))
	clean := make([]string, 0, len(recipients))
	for _, raw := range recipients {
		uid := strings.TrimSpace(raw)
		if uid == "" || uid == message.FromUID {
			continue
		}
		if _, exists := seen[uid]; exists {
			continue
		}
		seen[uid] = struct{}{}
		clean = append(clean, uid)
		if len(clean) > 10000 {
			return nil, errors.New("WuKongIM offline recipient count exceeds the product limit")
		}
	}
	return clean, nil
}

func wukongPushMessageType(contentType int) string {
	switch contentType {
	case wukong.ContentTypeImage, wukong.ContentTypeGIF, wukong.ContentTypeStoreSticker:
		return "image"
	case wukong.ContentTypeVoice:
		return "audio"
	case wukong.ContentTypeVideo:
		return "video"
	case wukong.ContentTypeLocation:
		return "location"
	case wukong.ContentTypeCard:
		return "contact"
	case wukong.ContentTypeFile:
		return "file"
	default:
		return "text"
	}
}

func projectWukongOnlineStatus(ctx context.Context, tx pgx.Tx, event wukong.WebhookEvent) error {
	var encoded string
	if err := json.Unmarshal(event.Payload, &encoded); err != nil {
		return fmt.Errorf("decode WuKongIM user.onlinestatus: %w", err)
	}
	parts := strings.Split(strings.TrimSpace(encoded), "-")
	if len(parts) < 6 {
		return errors.New("WuKongIM user.onlinestatus is incomplete")
	}
	base := len(parts) - 5
	uid := strings.TrimSpace(strings.Join(parts[:base], "-"))
	deviceFlag, flagErr := strconv.Atoi(parts[base])
	online, onlineErr := strconv.Atoi(parts[base+1])
	connID, connErr := strconv.ParseInt(parts[base+2], 10, 64)
	deviceCount, deviceErr := strconv.Atoi(parts[base+3])
	totalCount, totalErr := strconv.Atoi(parts[base+4])
	if uid == "" || flagErr != nil || onlineErr != nil || connErr != nil || deviceErr != nil || totalErr != nil ||
		deviceFlag < wukong.DeviceApp || deviceFlag > wukong.DeviceDesktop || (online != 0 && online != 1) ||
		deviceCount < 0 || totalCount < 0 {
		return errors.New("WuKongIM user.onlinestatus values are invalid")
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO im_wukong_presence(
			user_id,online,total_online_count,device_flag,device_online_count,conn_id,last_offline_at,updated_at
		) VALUES($1,$2,$3,$4,$5,$6,CASE WHEN $3=0 THEN $7::timestamptz ELSE NULL END,$7::timestamptz)
		ON CONFLICT(user_id) DO UPDATE SET
			online=excluded.online,total_online_count=excluded.total_online_count,
			device_flag=excluded.device_flag,device_online_count=excluded.device_online_count,
			conn_id=excluded.conn_id,
			last_offline_at=CASE WHEN excluded.total_online_count=0 THEN excluded.updated_at ELSE im_wukong_presence.last_offline_at END,
			updated_at=excluded.updated_at
		WHERE excluded.updated_at>=im_wukong_presence.updated_at
	`, uid, totalCount > 0, totalCount, deviceFlag, deviceCount, connID, event.ReceivedAt)
	return err
}

func indexWukongMessage(ctx context.Context, tx pgx.Tx, event wukong.WebhookEvent) error {
	var message wukongMessageNotification
	if err := json.Unmarshal(event.Payload, &message); err != nil {
		return fmt.Errorf("decode WuKongIM msg.notify: %w", err)
	}
	if message.Header.NoPersist == 1 {
		return nil
	}
	if message.MessageID == 0 || message.MessageSeq == 0 || message.MessageSeq > uint64(1<<63-1) ||
		strings.TrimSpace(message.ClientMsgNo) == "" || strings.TrimSpace(message.ChannelID) == "" || !json.Valid(message.Payload) {
		return errors.New("WuKongIM msg.notify is incomplete")
	}
	var content struct {
		Type      int    `json:"type"`
		MediaID   string `json:"mediaId"`
		ExpiresAt string `json:"expiresAt"`
		Mention   struct {
			UIDs []string `json:"uids"`
			All  int      `json:"all"`
		} `json:"mention"`
	}
	if err := json.Unmarshal(message.Payload, &content); err != nil || content.Type <= 0 {
		return errors.New("WuKongIM msg.notify content type is invalid")
	}
	conversationID := ""
	switch message.ChannelType {
	case wukong.ChannelGroup:
		_ = tx.QueryRow(ctx, `SELECT id FROM im_conversations WHERE id=$1 AND kind='group'`, message.ChannelID).Scan(&conversationID)
	case wukong.ChannelPerson:
		if message.FromUID != "" {
			_ = tx.QueryRow(ctx, `
				SELECT c.id FROM im_conversations c
				WHERE c.kind='direct'
				  AND EXISTS(SELECT 1 FROM im_members m WHERE m.conversation_id=c.id AND m.user_id=$1)
				  AND EXISTS(SELECT 1 FROM im_members m WHERE m.conversation_id=c.id AND m.user_id=$2)
				ORDER BY c.created_at LIMIT 1
			`, message.FromUID, message.ChannelID).Scan(&conversationID)
		}
	case wukong.ChannelCommunity, wukong.ChannelCommunityTopic, wukong.ChannelInfo, wukong.ChannelLive,
		wukong.ChannelCustomer, wukong.ChannelVisitor:
		_ = tx.QueryRow(ctx, `SELECT conversation_id FROM im_business_channels WHERE conversation_id=$1 AND channel_type=$2`, message.ChannelID, message.ChannelType).Scan(&conversationID)
	}
	digest := sha256.Sum256(message.Payload)
	messageAt := event.ReceivedAt
	if message.Timestamp > 0 {
		messageAt = time.Unix(message.Timestamp, 0).UTC()
	}
	var conversation any
	if conversationID != "" {
		conversation = conversationID
	}
	var expiresAt any
	if message.Expire > 0 {
		expiresAt = messageAt.Add(time.Duration(message.Expire) * time.Second)
	} else if portableExpiry, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(content.ExpiresAt)); parseErr == nil && portableExpiry.After(messageAt) && portableExpiry.Before(messageAt.Add(31*24*time.Hour)) {
		expiresAt = portableExpiry.UTC()
	}
	inserted, err := tx.Exec(ctx, `
		INSERT INTO im_wukong_message_index(
			message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,
			topic,message_seq,content_type,expire_seconds,media_id,expires_at,payload_sha256,message_timestamp,indexed_at
		) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,now())
		ON CONFLICT DO NOTHING
	`, message.MessageID, message.ClientMsgNo, conversation, message.FromUID, message.ChannelID, message.ChannelType,
		message.Topic, int64(message.MessageSeq), content.Type, int64(message.Expire), strings.TrimSpace(content.MediaID), expiresAt, fmt.Sprintf("%x", digest[:]), messageAt)
	if err != nil {
		return err
	}
	if inserted.RowsAffected() == 0 {
		// A retry can repeat the same WuKong message ID. A buggy or stale
		// sender can also cause WuKong to persist two message IDs for the same
		// sender/client message number. Keep the first canonical index row and
		// acknowledge the later webhook so one poison item cannot block the
		// entire WuKong webhook batch forever.
		var canonicalMessageID int64
		var canonicalConversationID *string
		err = tx.QueryRow(ctx, `
			SELECT message_id,conversation_id FROM im_wukong_message_index
			WHERE message_id=$1 OR (sender_id=$2 AND client_msg_no=$3)
			ORDER BY (message_id=$1) DESC LIMIT 1
		`, message.MessageID, message.FromUID, message.ClientMsgNo).Scan(&canonicalMessageID, &canonicalConversationID)
		if err != nil {
			return err
		}
		if canonicalMessageID != message.MessageID {
			return nil
		}
		if canonicalConversationID == nil && conversationID != "" {
			if _, err = tx.Exec(ctx, `UPDATE im_wukong_message_index SET conversation_id=$2,indexed_at=now() WHERE message_id=$1 AND conversation_id IS NULL`, message.MessageID, conversationID); err != nil {
				return err
			}
		}
	}
	if conversationID == "" {
		return nil
	}
	// Keep business conversation ordering and hide/read compatibility current
	// without storing the WuKong payload in PostgreSQL.
	_, err = tx.Exec(ctx, `UPDATE im_conversations SET
		current_seq=GREATEST(current_seq,$2),last_message_seq=GREATEST(last_message_seq,$2),
		updated_at=GREATEST(updated_at,$3) WHERE id=$1`, conversationID, int64(message.MessageSeq), messageAt)
	if err != nil {
		return err
	}
	if (message.ChannelType == wukong.ChannelGroup || message.ChannelType == wukong.ChannelCommunity ||
		message.ChannelType == wukong.ChannelCommunityTopic || message.ChannelType == wukong.ChannelLive) && content.Type == wukong.ContentTypeText {
		return indexWukongMentionReminders(ctx, tx, conversationID, message, messageAt, content.Mention.UIDs, content.Mention.All == 1)
	}
	return nil
}

func indexWukongMentionReminders(ctx context.Context, tx pgx.Tx, conversationID string, message wukongMessageNotification, messageAt time.Time, rawUIDs []string, mentionAll bool) error {
	seen := make(map[string]struct{}, len(rawUIDs))
	uids := make([]string, 0, len(rawUIDs))
	for _, raw := range rawUIDs {
		uid := strings.TrimSpace(raw)
		if uid == "" || uid == message.FromUID {
			continue
		}
		if _, duplicate := seen[uid]; duplicate {
			continue
		}
		seen[uid] = struct{}{}
		uids = append(uids, uid)
	}
	if !mentionAll && len(uids) == 0 {
		return nil
	}
	text := "[有人@你]"
	if mentionAll {
		text = "[有人@所有人]"
	}
	data, err := json.Marshal(map[string]any{"mentionAll": mentionAll, "uids": uids})
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		INSERT INTO im_wukong_reminders(
			user_id,conversation_id,message_id,message_seq,channel_id,channel_type,
			type,is_locate,text,data,done,need_upload,publisher,created_at,updated_at
		)
		SELECT member.user_id,$1,$2,$3,$1,$4,1,true,$5,$6,false,false,$7,$8,$8
		FROM im_members member
		WHERE member.conversation_id=$1 AND member.user_id<>$7
		  AND ($9 OR member.user_id=ANY($10::text[]))
		ON CONFLICT(user_id,message_id,type) DO NOTHING
		RETURNING user_id
	`, conversationID, message.MessageID, int64(message.MessageSeq), message.ChannelType,
		text, data, message.FromUID, messageAt, mentionAll, uids)
	if err != nil {
		return err
	}
	recipients := make([]wukongCommandRecipient, 0)
	for rows.Next() {
		var recipient wukongCommandRecipient
		if err = rows.Scan(&recipient.UserID); err != nil {
			rows.Close()
			return err
		}
		recipients = append(recipients, recipient)
	}
	rows.Close()
	if err = rows.Err(); err != nil || len(recipients) == 0 {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"channelId": conversationID, "channelType": message.ChannelType,
		"messageId": strconv.FormatInt(message.MessageID, 10),
	})
	if err != nil {
		return err
	}
	return enqueueWukongBusinessEvent(ctx, tx, "reminder", strconv.FormatInt(message.MessageID, 10), "reminder.updated", payload, recipients)
}

func (p *WithRedis) PutWukongWebhookEvent(ctx context.Context, event wukong.WebhookEvent) (bool, error) {
	if store, ok := p.base.(wukong.WebhookEventStore); ok {
		return store.PutWukongWebhookEvent(ctx, event)
	}
	return false, ErrUnsupported
}

func (p *Postgres) ClaimWukongOutbox(ctx context.Context, limit int) ([]wukong.OutboxItem, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `
		WITH picked AS (
			SELECT id FROM im_wukong_outbox
			WHERE (status='pending' AND available_at<=now())
			   OR (status='processing' AND locked_at<now()-interval '1 minute')
			ORDER BY id
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		), updated AS (
			UPDATE im_wukong_outbox o
			SET status='processing',attempts=o.attempts+1,locked_at=now()
			FROM picked WHERE o.id=picked.id
			RETURNING o.id,o.operation,o.aggregate_type,o.aggregate_id,o.payload,o.attempts
		)
		SELECT id,operation,aggregate_type,aggregate_id,payload,attempts FROM updated ORDER BY id
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]wukong.OutboxItem, 0, limit)
	for rows.Next() {
		var item wukong.OutboxItem
		if err = rows.Scan(&item.ID, &item.Operation, &item.AggregateType, &item.AggregateID, &item.Payload, &item.Attempts); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) CompleteWukongOutbox(ctx context.Context, id int64) error {
	_, err := p.pool.Exec(ctx, `UPDATE im_wukong_outbox SET status='completed',completed_at=now(),locked_at=NULL,last_error='' WHERE id=$1`, id)
	return err
}

func (p *Postgres) FailWukongOutbox(ctx context.Context, id int64, message string, retry bool) error {
	if retry {
		_, err := p.pool.Exec(ctx, `UPDATE im_wukong_outbox SET status='pending',available_at=now()+(LEAST(300,power(2,LEAST(attempts,8)))::text||' seconds')::interval,locked_at=NULL,last_error=left($2,2000) WHERE id=$1`, id, message)
		return err
	}
	_, err := p.pool.Exec(ctx, `UPDATE im_wukong_outbox SET status='failed',locked_at=NULL,last_error=left($2,2000) WHERE id=$1`, id, message)
	return err
}

func (p *WithRedis) ClaimWukongOutbox(ctx context.Context, limit int) ([]wukong.OutboxItem, error) {
	if store, ok := p.base.(wukong.OutboxStore); ok {
		return store.ClaimWukongOutbox(ctx, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) CompleteWukongOutbox(ctx context.Context, id int64) error {
	if store, ok := p.base.(wukong.OutboxStore); ok {
		return store.CompleteWukongOutbox(ctx, id)
	}
	return ErrUnsupported
}

func (p *WithRedis) FailWukongOutbox(ctx context.Context, id int64, message string, retry bool) error {
	if store, ok := p.base.(wukong.OutboxStore); ok {
		return store.FailWukongOutbox(ctx, id, message, retry)
	}
	return ErrUnsupported
}

func (p *Postgres) LoadWukongChannelSnapshot(ctx context.Context, channelID string, channelType uint8) (wukong.ChannelSnapshot, error) {
	if channelType != wukong.ChannelGroup {
		return p.loadBusinessChannelSnapshot(ctx, channelID, channelType)
	}
	var snapshot wukong.ChannelSnapshot
	var dissolvedAt *time.Time
	var memberCount int
	err := p.pool.QueryRow(ctx, `
		SELECT c.id,c.member_count,g.dissolved_at
		FROM im_conversations c JOIN im_groups g ON g.conversation_id=c.id
		WHERE c.id=$1
	`, channelID).Scan(&snapshot.ChannelID, &memberCount, &dissolvedAt)
	if err != nil {
		return snapshot, err
	}
	snapshot.ChannelType = wukong.ChannelGroup
	snapshot.Cursor = fmt.Sprintf("%02d:%s", snapshot.ChannelType, snapshot.ChannelID)
	if memberCount >= 1000 {
		snapshot.Large = 1
	}
	if dissolvedAt != nil {
		snapshot.Disband = 1
	}
	rows, err := p.pool.Query(ctx, `SELECT user_id FROM im_members WHERE conversation_id=$1 ORDER BY joined_at,user_id`, channelID)
	if err != nil {
		return snapshot, err
	}
	defer rows.Close()
	for rows.Next() {
		var uid string
		if err = rows.Scan(&uid); err != nil {
			return snapshot, err
		}
		snapshot.Subscribers = append(snapshot.Subscribers, uid)
	}
	return snapshot, rows.Err()
}

func (p *WithRedis) LoadWukongChannelSnapshot(ctx context.Context, channelID string, channelType uint8) (wukong.ChannelSnapshot, error) {
	if store, ok := p.base.(wukong.ChannelSnapshotStore); ok {
		return store.LoadWukongChannelSnapshot(ctx, channelID, channelType)
	}
	return wukong.ChannelSnapshot{}, ErrUnsupported
}

func (p *Postgres) ListWukongUserAccess(ctx context.Context, after string, limit int) ([]wukong.UserAccessSnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `
		SELECT u.id,
		       ARRAY(SELECT f.friend_user_id FROM im_friendships f WHERE f.user_id=u.id ORDER BY f.friend_user_id),
		       ARRAY(SELECT b.blocked_user_id FROM im_blocks b WHERE b.user_id=u.id ORDER BY b.blocked_user_id)
		FROM im_users u
		WHERE u.id>$1 AND u.deleted_at IS NULL
		ORDER BY u.id LIMIT $2
	`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]wukong.UserAccessSnapshot, 0, limit)
	for rows.Next() {
		var item wukong.UserAccessSnapshot
		if err = rows.Scan(&item.UID, &item.Allowlist, &item.Denylist); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) ListWukongChannels(ctx context.Context, after string, limit int) ([]wukong.ChannelSnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `
		SELECT item.cursor,item.channel_id,item.channel_type,item.member_count,item.ban,item.disband,item.send_ban,item.allow_stranger,
			item.subscribers,item.allowlist,item.denylist
		FROM (
			SELECT '02:'||c.id AS cursor,c.id AS channel_id,2::smallint AS channel_type,c.member_count,
				false AS ban,(g.dissolved_at IS NOT NULL) AS disband,false AS send_ban,false AS allow_stranger,
				ARRAY(SELECT m.user_id FROM im_members m WHERE m.conversation_id=c.id ORDER BY m.joined_at,m.user_id) AS subscribers,
				ARRAY[]::text[] AS allowlist,ARRAY[]::text[] AS denylist
			FROM im_conversations c JOIN im_groups g ON g.conversation_id=c.id
			UNION ALL
			SELECT lpad(channel.channel_type::text,2,'0')||':'||channel.conversation_id,
				channel.conversation_id,channel.channel_type,
				(SELECT count(*) FROM im_members active WHERE active.conversation_id=channel.conversation_id AND (active.expires_at IS NULL OR active.expires_at>now())),
				channel.ban,channel.disband,channel.send_ban,channel.allow_stranger,
				ARRAY(SELECT member.user_id FROM im_members member WHERE member.conversation_id=channel.conversation_id AND (member.expires_at IS NULL OR member.expires_at>now()) ORDER BY member.joined_at,member.user_id),
				ARRAY(SELECT access.user_id FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='allow' ORDER BY access.user_id),
				ARRAY(SELECT access.user_id FROM im_business_channel_access access WHERE access.conversation_id=channel.conversation_id AND access.access_type='deny' ORDER BY access.user_id)
			FROM im_business_channels channel JOIN im_conversations conversation ON conversation.id=channel.conversation_id
		) item WHERE item.cursor>$1 ORDER BY item.cursor LIMIT $2
	`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]wukong.ChannelSnapshot, 0, limit)
	for rows.Next() {
		var item wukong.ChannelSnapshot
		var memberCount int
		var ban, disband, sendBan, allowStranger bool
		if err = rows.Scan(&item.Cursor, &item.ChannelID, &item.ChannelType, &memberCount,
			&ban, &disband, &sendBan, &allowStranger, &item.Subscribers, &item.Allowlist, &item.Denylist); err != nil {
			return nil, err
		}
		if memberCount >= 1000 {
			item.Large = 1
		}
		if ban {
			item.Ban = 1
		}
		if disband {
			item.Disband = 1
		}
		if sendBan {
			item.SendBan = 1
		}
		if allowStranger {
			item.AllowStranger = 1
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *WithRedis) ListWukongUserAccess(ctx context.Context, after string, limit int) ([]wukong.UserAccessSnapshot, error) {
	if store, ok := p.base.(wukong.ReconcileStore); ok {
		return store.ListWukongUserAccess(ctx, after, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListWukongChannels(ctx context.Context, after string, limit int) ([]wukong.ChannelSnapshot, error) {
	if store, ok := p.base.(wukong.ReconcileStore); ok {
		return store.ListWukongChannels(ctx, after, limit)
	}
	return nil, ErrUnsupported
}
