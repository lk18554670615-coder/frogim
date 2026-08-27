package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/linli/im/server/internal/store"
)

func stickerCategoryJSON(item *store.StickerCategory) map[string]any {
	return map[string]any{
		"id": item.ID, "name": item.Name, "sortOrder": item.SortOrder,
		"enabled": item.Enabled, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func (x *API) adminStickerCategories(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.StickerCategories(r.Context(), true)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, stickerCategoryJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) adminSaveStickerCategory(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		ID        string `json:"id"`
		Name      string `json:"name"`
		SortOrder int    `json:"sortOrder"`
		Enabled   *bool  `json:"enabled"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if pathID := strings.TrimSpace(r.PathValue("id")); pathID != "" {
		request.ID = pathID
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	item, err := x.app.SaveStickerCategory(r.Context(), store.StickerCategoryInput{
		ID: request.ID, Name: request.Name, SortOrder: request.SortOrder, Enabled: enabled,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "sticker.category.save", "sticker_category", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	write(w, http.StatusOK, map[string]any{"item": stickerCategoryJSON(item)})
}

func (x *API) adminSaveStickerPack(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		ID           string `json:"id"`
		CategoryID   string `json:"categoryId"`
		Name         string `json:"name"`
		Description  string `json:"description"`
		CoverMediaID string `json:"coverMediaId"`
		Status       string `json:"status"`
		SortOrder    int    `json:"sortOrder"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if pathID := strings.TrimSpace(r.PathValue("id")); pathID != "" {
		request.ID = pathID
	}
	item, err := x.app.SaveStickerPack(r.Context(), store.StickerPackInput{
		ID: request.ID, CategoryID: request.CategoryID, Name: request.Name,
		Description: request.Description, CoverMediaID: request.CoverMediaID,
		Status: request.Status, SortOrder: request.SortOrder, ActorID: uid(r),
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "sticker.pack.save", "sticker_pack", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	write(w, http.StatusOK, map[string]any{"item": stickerPackJSON(item)})
}

func (x *API) adminSaveStickerItem(w http.ResponseWriter, r *http.Request) {
	var request struct {
		adminMutationConfirmation
		ID        string         `json:"id"`
		Name      string         `json:"name"`
		MediaID   string         `json:"mediaId"`
		Emoji     string         `json:"emoji"`
		Status    string         `json:"status"`
		SortOrder int            `json:"sortOrder"`
		Metadata  map[string]any `json:"metadata"`
	}
	if decode(r, &request) != nil || !validAdminMutation(request.adminMutationConfirmation) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if pathID := strings.TrimSpace(r.PathValue("itemId")); pathID != "" {
		request.ID = pathID
	}
	item, err := x.app.SaveStickerItem(r.Context(), store.StickerItemInput{
		ID: request.ID, PackID: r.PathValue("id"), Name: request.Name,
		MediaID: request.MediaID, Emoji: request.Emoji, Status: request.Status,
		SortOrder: request.SortOrder, Metadata: request.Metadata,
	})
	if err != nil {
		handleErr(w, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "sticker.item.save", "sticker_item", item.ID, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason), "packId": item.PackID})
	write(w, http.StatusOK, map[string]any{"item": stickerItemJSON(item)})
}

func (x *API) adminMoments(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminMomentsPage(r.Context(), query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, momentJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result, "total": total, "nextCursor": next})
}

func (x *API) moderateAdminMoment(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Status    string `json:"status"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil || !request.Confirmed || strings.TrimSpace(request.Reason) == "" {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	if err := x.app.ModerateMoment(r.Context(), r.PathValue("id"), request.Status, uid(r), request.Reason); err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"id": r.PathValue("id"), "status": request.Status})
}

func (x *API) adminStickerPacks(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit, _ := strconv.Atoi(query.Get("limit"))
	items, total, next, err := x.app.AdminStickerPacksPage(r.Context(), query.Get("q"), query.Get("status"), query.Get("cursor"), limit)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		value := stickerPackJSON(item)
		value["createdBy"] = item.CreatedBy
		value["reviewedBy"] = item.ReviewedBy
		value["reviewReason"] = item.ReviewReason
		value["reviewedAt"] = item.ReviewedAt
		result = append(result, value)
	}
	write(w, http.StatusOK, map[string]any{"items": result, "total": total, "nextCursor": next})
}

func (x *API) reviewAdminStickerPack(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Status    string `json:"status"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil || !request.Confirmed || strings.TrimSpace(request.Reason) == "" {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	item, err := x.app.ReviewStickerPack(r.Context(), r.PathValue("id"), request.Status, request.Reason, uid(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	value := stickerPackJSON(item)
	value["createdBy"] = item.CreatedBy
	value["reviewedBy"] = item.ReviewedBy
	value["reviewReason"] = item.ReviewReason
	value["reviewedAt"] = item.ReviewedAt
	write(w, http.StatusOK, value)
}
