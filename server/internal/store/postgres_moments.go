package store

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const momentVisibilitySQL = `(moment.author_id=$1 OR (moment.status='published' AND (
	moment.visibility='public' OR
	(moment.visibility IN ('friends','excluded') AND EXISTS(
		SELECT 1 FROM im_friendships friendship
		WHERE friendship.user_id=$1 AND friendship.friend_user_id=moment.author_id)
		AND (moment.visibility<>'excluded' OR NOT ($1=ANY(moment.visible_user_ids)))) OR
	(moment.visibility='selected' AND $1=ANY(moment.visible_user_ids))
)))`

const momentSelectSQL = `SELECT moment.id,moment.author_id,author.name,author.avatar_url,
	moment.content,moment.media_kind,moment.media_ids,
	ARRAY(SELECT media.mime FROM unnest(moment.media_ids) WITH ORDINALITY item(id,position)
		JOIN im_media media ON media.id=item.id ORDER BY item.position),
	moment.visibility,moment.visible_user_ids,moment.location,moment.status,moment.created_at,moment.updated_at,
	(SELECT count(*) FROM im_moment_likes likes WHERE likes.moment_id=moment.id),
	(SELECT count(*) FROM im_moment_comments comments WHERE comments.moment_id=moment.id AND comments.status='active'),
	EXISTS(SELECT 1 FROM im_moment_likes own_like WHERE own_like.moment_id=moment.id AND own_like.user_id=$1)
	FROM im_moments moment JOIN im_users author ON author.id=moment.author_id`

func cleanMomentIDs(values []string, limit int) ([]string, error) {
	clean := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if value == "" || len(value) > 128 {
			return nil, ErrConflict
		}
		if _, duplicate := seen[value]; !duplicate {
			seen[value] = struct{}{}
			clean = append(clean, value)
		}
	}
	if len(clean) > limit {
		return nil, ErrConflict
	}
	return clean, nil
}

func scanMoment(row pgx.Row) (*Moment, error) {
	item := &Moment{}
	var mediaIDs, mediaMIMEs []string
	var location []byte
	if err := row.Scan(&item.ID, &item.AuthorID, &item.AuthorName, &item.AuthorAvatarURL,
		&item.Content, &item.MediaKind, &mediaIDs, &mediaMIMEs,
		&item.Visibility, &item.VisibleUserIDs, &location, &item.Status, &item.CreatedAt, &item.UpdatedAt,
		&item.LikeCount, &item.CommentCount, &item.LikedByMe); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(location, &item.Location); err != nil {
		return nil, err
	}
	item.Media = make([]MomentMedia, 0, len(mediaIDs))
	for index, id := range mediaIDs {
		mime := ""
		if index < len(mediaMIMEs) {
			mime = mediaMIMEs[index]
		}
		item.Media = append(item.Media, MomentMedia{ID: id, MIME: mime})
	}
	return item, nil
}

func (p *Postgres) loadMoment(ctx context.Context, viewerID, momentID string) (*Moment, error) {
	item, err := scanMoment(p.pool.QueryRow(ctx, momentSelectSQL+`
		WHERE moment.id=$2 AND moment.status<>'deleted' AND `+momentVisibilitySQL, viewerID, momentID))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err = p.loadMomentComments(ctx, []*Moment{item}); err != nil {
		return nil, err
	}
	return item, nil
}

func (p *Postgres) CanAccessMoment(ctx context.Context, viewerID, momentID string) (bool, error) {
	var allowed bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_moments moment
		WHERE moment.id=$2 AND moment.status<>'deleted' AND `+momentVisibilitySQL+`)`,
		strings.TrimSpace(viewerID), strings.TrimSpace(momentID)).Scan(&allowed)
	return allowed, err
}

func (p *Postgres) CreateMoment(ctx context.Context, input MomentCreate) (*Moment, error) {
	input.ID, input.AuthorID = strings.TrimSpace(input.ID), strings.TrimSpace(input.AuthorID)
	input.Content, input.MediaKind, input.Visibility = strings.TrimSpace(input.Content), strings.TrimSpace(input.MediaKind), strings.TrimSpace(input.Visibility)
	if input.ID == "" || input.AuthorID == "" || len([]rune(input.Content)) > 5000 || input.At.IsZero() {
		return nil, ErrConflict
	}
	mediaIDs, err := cleanMomentIDs(input.MediaIDs, 9)
	if err != nil {
		return nil, err
	}
	visibleIDs, err := cleanMomentIDs(input.VisibleUserIDs, 500)
	if err != nil {
		return nil, err
	}
	if input.MediaKind == "" {
		if len(mediaIDs) == 0 {
			input.MediaKind = "none"
		} else {
			input.MediaKind = "images"
		}
	}
	if input.Visibility == "" {
		input.Visibility = "friends"
	}
	if input.Content == "" && len(mediaIDs) == 0 ||
		(input.MediaKind == "none" && len(mediaIDs) != 0) ||
		(input.MediaKind == "images" && (len(mediaIDs) < 1 || len(mediaIDs) > 9)) ||
		(input.MediaKind == "video" && len(mediaIDs) != 1) ||
		(input.MediaKind != "none" && input.MediaKind != "images" && input.MediaKind != "video") ||
		(input.Visibility != "public" && input.Visibility != "friends" && input.Visibility != "private" && input.Visibility != "selected" && input.Visibility != "excluded") ||
		((input.Visibility == "selected" || input.Visibility == "excluded") && len(visibleIDs) == 0) ||
		(input.Visibility != "selected" && input.Visibility != "excluded" && len(visibleIDs) != 0) {
		return nil, ErrConflict
	}
	if input.Location == nil {
		input.Location = map[string]any{}
	}
	location, err := json.Marshal(input.Location)
	if err != nil || len(location) > 16<<10 {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var authorExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_users WHERE id=$1 AND deleted_at IS NULL AND NOT banned)`, input.AuthorID).Scan(&authorExists); err != nil {
		return nil, err
	}
	if !authorExists {
		return nil, ErrForbidden
	}
	if len(visibleIDs) > 0 {
		var visibleCount int
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM im_users WHERE id=ANY($1::text[]) AND id<>$2 AND deleted_at IS NULL`, visibleIDs, input.AuthorID).Scan(&visibleCount); err != nil {
			return nil, err
		}
		if visibleCount != len(visibleIDs) {
			return nil, ErrConflict
		}
	}
	if len(mediaIDs) > 0 {
		rows, queryErr := tx.Query(ctx, `SELECT id,mime FROM im_media WHERE id=ANY($1::text[]) AND owner_id=$2 AND status='ready'`, mediaIDs, input.AuthorID)
		if queryErr != nil {
			return nil, queryErr
		}
		mediaTypes := make(map[string]string, len(mediaIDs))
		for rows.Next() {
			var id, mime string
			if queryErr = rows.Scan(&id, &mime); queryErr != nil {
				rows.Close()
				return nil, queryErr
			}
			mediaTypes[id] = mime
		}
		queryErr = rows.Err()
		rows.Close()
		if queryErr != nil || len(mediaTypes) != len(mediaIDs) {
			if queryErr != nil {
				return nil, queryErr
			}
			return nil, ErrForbidden
		}
		for _, id := range mediaIDs {
			mime := mediaTypes[id]
			if input.MediaKind == "images" && !strings.HasPrefix(mime, "image/") ||
				input.MediaKind == "video" && !strings.HasPrefix(mime, "video/") {
				return nil, ErrConflict
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_moments(
		id,author_id,content,media_kind,media_ids,visibility,visible_user_ids,location,status,created_at,updated_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'published',$9,$9)`, input.ID, input.AuthorID, input.Content,
		input.MediaKind, mediaIDs, input.Visibility, visibleIDs, location, input.At); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.loadMoment(ctx, input.AuthorID, input.ID)
}

func (p *Postgres) ListMoments(ctx context.Context, viewerID, authorID, after string, limit int) ([]*Moment, string, error) {
	viewerID, authorID, after = strings.TrimSpace(viewerID), strings.TrimSpace(authorID), strings.TrimSpace(after)
	if viewerID == "" {
		return nil, "", ErrForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := p.pool.Query(ctx, momentSelectSQL+`
		WHERE moment.status<>'deleted' AND `+momentVisibilitySQL+`
		AND ($2='' OR moment.author_id=$2)
		AND ($3='' OR (moment.created_at,moment.id)<(
			SELECT cursor.created_at,cursor.id FROM im_moments cursor WHERE cursor.id=$3))
		ORDER BY moment.created_at DESC,moment.id DESC LIMIT $4`, viewerID, authorID, after, limit+1)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	items := make([]*Moment, 0, limit+1)
	for rows.Next() {
		item, scanErr := scanMoment(rows)
		if scanErr != nil {
			return nil, "", scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		next = items[limit-1].ID
		items = items[:limit]
	}
	if err = p.loadMomentComments(ctx, items); err != nil {
		return nil, "", err
	}
	return items, next, nil
}

func (p *Postgres) loadMomentComments(ctx context.Context, moments []*Moment) error {
	if len(moments) == 0 {
		return nil
	}
	ids := make([]string, 0, len(moments))
	byID := make(map[string]*Moment, len(moments))
	for _, item := range moments {
		ids = append(ids, item.ID)
		byID[item.ID] = item
		item.Comments = make([]*MomentComment, 0)
	}
	rows, err := p.pool.Query(ctx, `SELECT comment.id,comment.moment_id,comment.author_id,author.name,author.avatar_url,
		COALESCE(comment.parent_id,''),COALESCE(comment.reply_to_user_id,''),COALESCE(reply_user.name,''),comment.content,comment.created_at
		FROM im_moment_comments comment JOIN im_users author ON author.id=comment.author_id
		LEFT JOIN im_users reply_user ON reply_user.id=comment.reply_to_user_id
		WHERE comment.moment_id=ANY($1::text[]) AND comment.status='active'
		ORDER BY comment.moment_id,comment.created_at,comment.id`, ids)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		comment := &MomentComment{}
		if err = rows.Scan(&comment.ID, &comment.MomentID, &comment.AuthorID, &comment.AuthorName, &comment.AuthorAvatarURL,
			&comment.ParentID, &comment.ReplyToUserID, &comment.ReplyToName, &comment.Content, &comment.CreatedAt); err != nil {
			return err
		}
		if item := byID[comment.MomentID]; item != nil {
			item.Comments = append(item.Comments, comment)
		}
	}
	return rows.Err()
}

func (p *Postgres) SetMomentLike(ctx context.Context, userID, momentID string, active bool, at time.Time) (*Moment, error) {
	userID, momentID = strings.TrimSpace(userID), strings.TrimSpace(momentID)
	if userID == "" || momentID == "" || at.IsZero() {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var authorID string
	if err = tx.QueryRow(ctx, `SELECT moment.author_id FROM im_moments moment WHERE moment.id=$2
		AND moment.status<>'deleted' AND `+momentVisibilitySQL+` FOR UPDATE`, userID, momentID).Scan(&authorID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	changed := false
	if active {
		tag, execErr := tx.Exec(ctx, `INSERT INTO im_moment_likes(moment_id,user_id,created_at) VALUES($1,$2,$3)
			ON CONFLICT(moment_id,user_id) DO NOTHING`, momentID, userID, at)
		if execErr != nil {
			return nil, execErr
		}
		changed = tag.RowsAffected() == 1
		if changed && authorID != userID {
			if _, err = tx.Exec(ctx, `INSERT INTO im_moment_reminders(user_id,moment_id,actor_id,type,created_at)
				VALUES($1,$2,$3,'like',$4) ON CONFLICT DO NOTHING`, authorID, momentID, userID, at); err != nil {
				return nil, err
			}
		}
	} else {
		tag, execErr := tx.Exec(ctx, `DELETE FROM im_moment_likes WHERE moment_id=$1 AND user_id=$2`, momentID, userID)
		if execErr != nil {
			return nil, execErr
		}
		changed = tag.RowsAffected() == 1
	}
	if changed && authorID != userID {
		payload, _ := json.Marshal(map[string]any{"momentId": momentID, "active": active})
		if err = enqueueWukongBusinessEvent(ctx, tx, "moment", momentID, "moment.like.updated", payload,
			[]wukongCommandRecipient{{UserID: authorID}}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return p.loadMoment(ctx, userID, momentID)
}

func (p *Postgres) CreateMomentComment(ctx context.Context, commentID, userID, momentID, parentID, content string, at time.Time) (*MomentComment, error) {
	commentID, userID, momentID = strings.TrimSpace(commentID), strings.TrimSpace(userID), strings.TrimSpace(momentID)
	parentID, content = strings.TrimSpace(parentID), strings.TrimSpace(content)
	if commentID == "" || userID == "" || momentID == "" || content == "" || len([]rune(content)) > 2000 || at.IsZero() {
		return nil, ErrConflict
	}
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var momentAuthorID string
	if err = tx.QueryRow(ctx, `SELECT moment.author_id FROM im_moments moment WHERE moment.id=$2
		AND moment.status<>'deleted' AND `+momentVisibilitySQL+` FOR UPDATE`, userID, momentID).Scan(&momentAuthorID); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	replyToUserID := ""
	if parentID != "" {
		if err = tx.QueryRow(ctx, `SELECT author_id FROM im_moment_comments
			WHERE id=$1 AND moment_id=$2 AND status='active'`, parentID, momentID).Scan(&replyToUserID); errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		} else if err != nil {
			return nil, err
		}
	}
	var parentValue, replyValue any
	if parentID != "" {
		parentValue = parentID
	}
	if replyToUserID != "" {
		replyValue = replyToUserID
	}
	if _, err = tx.Exec(ctx, `INSERT INTO im_moment_comments(
		id,moment_id,author_id,parent_id,reply_to_user_id,content,status,created_at
	) VALUES($1,$2,$3,$4,$5,$6,'active',$7)`, commentID, momentID, userID, parentValue, replyValue, content, at); err != nil {
		return nil, err
	}
	recipientID, reminderType := momentAuthorID, "comment"
	if replyToUserID != "" {
		recipientID, reminderType = replyToUserID, "reply"
	}
	if recipientID != userID {
		if _, err = tx.Exec(ctx, `INSERT INTO im_moment_reminders(user_id,moment_id,actor_id,type,comment_id,created_at)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, recipientID, momentID, userID, reminderType, commentID, at); err != nil {
			return nil, err
		}
		payload, _ := json.Marshal(map[string]any{"momentId": momentID, "commentId": commentID, "type": reminderType})
		if err = enqueueWukongBusinessEvent(ctx, tx, "moment", momentID, "moment.comment.created", payload,
			[]wukongCommandRecipient{{UserID: recipientID}}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	comment := &MomentComment{}
	err = p.pool.QueryRow(ctx, `SELECT comment.id,comment.moment_id,comment.author_id,author.name,author.avatar_url,
		COALESCE(comment.parent_id,''),COALESCE(comment.reply_to_user_id,''),COALESCE(reply_user.name,''),comment.content,comment.created_at
		FROM im_moment_comments comment JOIN im_users author ON author.id=comment.author_id
		LEFT JOIN im_users reply_user ON reply_user.id=comment.reply_to_user_id WHERE comment.id=$1`, commentID).Scan(
		&comment.ID, &comment.MomentID, &comment.AuthorID, &comment.AuthorName, &comment.AuthorAvatarURL,
		&comment.ParentID, &comment.ReplyToUserID, &comment.ReplyToName, &comment.Content, &comment.CreatedAt)
	return comment, err
}

func (p *Postgres) DeleteMoment(ctx context.Context, userID, momentID string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	tag, err := tx.Exec(ctx, `UPDATE im_moments SET status='deleted',deleted_at=$3,updated_at=$3
		WHERE id=$2 AND author_id=$1 AND status<>'deleted'`, strings.TrimSpace(userID), strings.TrimSpace(momentID), at)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	rows, err := tx.Query(ctx, `SELECT DISTINCT user_id FROM (
		SELECT user_id FROM im_moment_likes WHERE moment_id=$1
		UNION SELECT author_id FROM im_moment_comments WHERE moment_id=$1
	) participant WHERE user_id<>$2`, momentID, userID)
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
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	if len(recipients) > 0 {
		payload, _ := json.Marshal(map[string]any{"momentId": momentID})
		if err = enqueueWukongBusinessEvent(ctx, tx, "moment", momentID, "moment.deleted", payload, recipients); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (p *Postgres) DeleteMomentComment(ctx context.Context, userID, momentID, commentID string, at time.Time) error {
	tag, err := p.pool.Exec(ctx, `UPDATE im_moment_comments comment SET status='deleted',deleted_at=$4
		FROM im_moments moment WHERE comment.id=$3 AND comment.moment_id=$2 AND moment.id=comment.moment_id
		AND comment.status='active' AND (comment.author_id=$1 OR moment.author_id=$1)`,
		strings.TrimSpace(userID), strings.TrimSpace(momentID), strings.TrimSpace(commentID), at)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func (p *Postgres) ListMomentReminders(ctx context.Context, userID string, limit int) ([]*MomentReminder, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := p.pool.Query(ctx, `SELECT reminder.id,reminder.moment_id,reminder.actor_id,actor.name,actor.avatar_url,
		reminder.type,COALESCE(reminder.comment_id,''),left(moment.content,160),reminder.read_at,reminder.created_at
		FROM im_moment_reminders reminder JOIN im_users actor ON actor.id=reminder.actor_id
		JOIN im_moments moment ON moment.id=reminder.moment_id
		WHERE reminder.user_id=$1 ORDER BY reminder.created_at DESC,reminder.id DESC LIMIT $2`, strings.TrimSpace(userID), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*MomentReminder, 0)
	for rows.Next() {
		item := &MomentReminder{}
		if err = rows.Scan(&item.ID, &item.MomentID, &item.ActorID, &item.ActorName, &item.ActorAvatar,
			&item.Type, &item.CommentID, &item.MomentPreview, &item.ReadAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (p *Postgres) MarkMomentRemindersRead(ctx context.Context, userID string, reminderIDs []int64, at time.Time) error {
	userID = strings.TrimSpace(userID)
	if userID == "" || at.IsZero() {
		return ErrConflict
	}
	if len(reminderIDs) == 0 {
		_, err := p.pool.Exec(ctx, `UPDATE im_moment_reminders SET read_at=$2 WHERE user_id=$1 AND read_at IS NULL`, userID, at)
		return err
	}
	if len(reminderIDs) > 500 {
		return ErrConflict
	}
	sort.Slice(reminderIDs, func(i, j int) bool { return reminderIDs[i] < reminderIDs[j] })
	_, err := p.pool.Exec(ctx, `UPDATE im_moment_reminders SET read_at=$3
		WHERE user_id=$1 AND id=ANY($2::bigint[]) AND read_at IS NULL`, userID, reminderIDs, at)
	return err
}

func (p *WithRedis) CreateMoment(ctx context.Context, input MomentCreate) (*Moment, error) {
	if store, ok := p.base.(MomentStore); ok {
		return store.CreateMoment(ctx, input)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) ListMoments(ctx context.Context, viewerID, authorID, after string, limit int) ([]*Moment, string, error) {
	if store, ok := p.base.(MomentStore); ok {
		return store.ListMoments(ctx, viewerID, authorID, after, limit)
	}
	return nil, "", ErrUnsupported
}

func (p *WithRedis) SetMomentLike(ctx context.Context, userID, momentID string, active bool, at time.Time) (*Moment, error) {
	if store, ok := p.base.(MomentStore); ok {
		return store.SetMomentLike(ctx, userID, momentID, active, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) CreateMomentComment(ctx context.Context, commentID, userID, momentID, parentID, content string, at time.Time) (*MomentComment, error) {
	if store, ok := p.base.(MomentStore); ok {
		return store.CreateMomentComment(ctx, commentID, userID, momentID, parentID, content, at)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) DeleteMoment(ctx context.Context, userID, momentID string, at time.Time) error {
	if store, ok := p.base.(MomentStore); ok {
		return store.DeleteMoment(ctx, userID, momentID, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) DeleteMomentComment(ctx context.Context, userID, momentID, commentID string, at time.Time) error {
	if store, ok := p.base.(MomentStore); ok {
		return store.DeleteMomentComment(ctx, userID, momentID, commentID, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) ListMomentReminders(ctx context.Context, userID string, limit int) ([]*MomentReminder, error) {
	if store, ok := p.base.(MomentStore); ok {
		return store.ListMomentReminders(ctx, userID, limit)
	}
	return nil, ErrUnsupported
}

func (p *WithRedis) MarkMomentRemindersRead(ctx context.Context, userID string, reminderIDs []int64, at time.Time) error {
	if store, ok := p.base.(MomentStore); ok {
		return store.MarkMomentRemindersRead(ctx, userID, reminderIDs, at)
	}
	return ErrUnsupported
}

func (p *WithRedis) CanAccessMoment(ctx context.Context, viewerID, momentID string) (bool, error) {
	if store, ok := p.base.(MomentStore); ok {
		return store.CanAccessMoment(ctx, viewerID, momentID)
	}
	return false, ErrUnsupported
}
