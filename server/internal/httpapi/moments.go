package httpapi

import (
	"net/http"
	"strconv"

	"github.com/linli/im/server/internal/store"
)

func momentCommentJSON(item *store.MomentComment) map[string]any {
	return map[string]any{
		"id": item.ID, "momentId": item.MomentID,
		"authorId": item.AuthorID, "authorName": item.AuthorName, "authorAvatarUrl": item.AuthorAvatarURL,
		"parentId": item.ParentID, "replyToUserId": item.ReplyToUserID, "replyToName": item.ReplyToName,
		"content": item.Content, "createdAt": item.CreatedAt,
	}
}

func momentJSON(item *store.Moment) map[string]any {
	media := make([]map[string]any, 0, len(item.Media))
	for _, value := range item.Media {
		media = append(media, map[string]any{
			"id": value.ID, "mime": value.MIME, "url": "/v2/media/" + value.ID,
		})
	}
	comments := make([]map[string]any, 0, len(item.Comments))
	for _, value := range item.Comments {
		comments = append(comments, momentCommentJSON(value))
	}
	return map[string]any{
		"id": item.ID, "authorId": item.AuthorID, "authorName": item.AuthorName,
		"authorAvatarUrl": item.AuthorAvatarURL, "content": item.Content,
		"mediaKind": item.MediaKind, "media": media, "visibility": item.Visibility,
		"visibleUserIds": item.VisibleUserIDs, "location": item.Location,
		"likeCount": item.LikeCount, "commentCount": item.CommentCount,
		"likedByMe": item.LikedByMe, "comments": comments,
		"status": item.Status, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func (x *API) moments(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, next, err := x.app.Moments(r.Context(), uid(r), r.URL.Query().Get("authorId"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, momentJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "nextCursor": next})
}

func (x *API) createMoment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Content        string         `json:"content"`
		MediaKind      string         `json:"mediaKind"`
		MediaIDs       []string       `json:"mediaIds"`
		Visibility     string         `json:"visibility"`
		VisibleUserIDs []string       `json:"visibleUserIds"`
		Location       map[string]any `json:"location"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid moment")
		return
	}
	item, err := x.app.CreateMoment(r.Context(), uid(r), input.Content, input.MediaKind, input.Visibility, input.MediaIDs, input.VisibleUserIDs, input.Location)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"item": momentJSON(item)})
}

func (x *API) setMomentLike(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.SetMomentLike(r.Context(), uid(r), r.PathValue("id"), r.Method == http.MethodPut)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": momentJSON(item)})
}

func (x *API) createMomentComment(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ParentID string `json:"parentId"`
		Content  string `json:"content"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid moment comment")
		return
	}
	item, err := x.app.CreateMomentComment(r.Context(), uid(r), r.PathValue("id"), input.ParentID, input.Content)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusCreated, map[string]any{"item": momentCommentJSON(item)})
}

func (x *API) deleteMoment(w http.ResponseWriter, r *http.Request) {
	if err := x.app.DeleteMoment(r.Context(), uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) deleteMomentComment(w http.ResponseWriter, r *http.Request) {
	if err := x.app.DeleteMomentComment(r.Context(), uid(r), r.PathValue("id"), r.PathValue("commentId")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) momentReminders(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := x.app.MomentReminders(r.Context(), uid(r), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id": item.ID, "momentId": item.MomentID, "actorId": item.ActorID,
			"actorName": item.ActorName, "actorAvatarUrl": item.ActorAvatar,
			"type": item.Type, "commentId": item.CommentID, "momentPreview": item.MomentPreview,
			"readAt": item.ReadAt, "createdAt": item.CreatedAt,
		})
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) markMomentRemindersRead(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ReminderIDs []int64 `json:"reminderIds"`
	}
	if decode(r, &input) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid reminder ids")
		return
	}
	if err := x.app.MarkMomentRemindersRead(r.Context(), uid(r), input.ReminderIDs); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"status": "ok"})
}
