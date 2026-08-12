package httpapi

import (
	"net/http"
	"strconv"

	"github.com/linli/im/server/internal/store"
)

func stickerItemJSON(item *store.StickerItem) map[string]any {
	return map[string]any{
		"id": item.ID, "packId": item.PackID, "name": item.Name,
		"mediaId": item.MediaID, "mime": item.MIME, "url": "/v2/media/" + item.MediaID,
		"emoji": item.Emoji, "sortOrder": item.SortOrder, "status": item.Status,
		"metadata": item.Metadata, "favorite": item.Favorite,
		"useCount": item.UseCount, "usedAt": item.UsedAt,
		"createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func stickerPackJSON(item *store.StickerPack) map[string]any {
	items := make([]map[string]any, 0, len(item.Items))
	for _, value := range item.Items {
		items = append(items, stickerItemJSON(value))
	}
	return map[string]any{
		"id": item.ID, "categoryId": item.CategoryID, "categoryName": item.CategoryName,
		"name": item.Name, "description": item.Description,
		"coverMediaId": item.CoverMediaID, "coverMime": item.CoverMIME,
		"coverUrl": "/v2/media/" + item.CoverMediaID,
		"status":   item.Status, "sortOrder": item.SortOrder, "favorite": item.Favorite,
		"items": items, "createdAt": item.CreatedAt, "updatedAt": item.UpdatedAt,
	}
}

func (x *API) stickerCategories(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.StickerCategories(r.Context(), false)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, map[string]any{
			"id": item.ID, "name": item.Name, "sortOrder": item.SortOrder,
		})
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) stickerPacks(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.StickerPacks(r.Context(), uid(r), r.URL.Query().Get("categoryId"), false)
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, stickerPackJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) stickerPack(w http.ResponseWriter, r *http.Request) {
	item, err := x.app.StickerPack(r.Context(), uid(r), r.PathValue("id"), false)
	if err != nil {
		handleErr(w, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"item": stickerPackJSON(item)})
}

func (x *API) setStickerPackFavorite(w http.ResponseWriter, r *http.Request) {
	if err := x.app.SetStickerPackFavorite(r.Context(), uid(r), r.PathValue("id"), r.Method == http.MethodPut); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) setStickerFavorite(w http.ResponseWriter, r *http.Request) {
	if err := x.app.SetStickerFavorite(r.Context(), uid(r), r.PathValue("id"), r.Method == http.MethodPut); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) recordStickerUse(w http.ResponseWriter, r *http.Request) {
	if err := x.app.RecordStickerUse(r.Context(), uid(r), r.PathValue("id")); err != nil {
		handleErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) recentStickers(w http.ResponseWriter, r *http.Request) {
	x.userStickerList(w, r, true)
}

func (x *API) favoriteStickers(w http.ResponseWriter, r *http.Request) {
	x.userStickerList(w, r, false)
}

func (x *API) userStickerList(w http.ResponseWriter, r *http.Request, recent bool) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var items []*store.StickerItem
	var err error
	if recent {
		items, err = x.app.RecentStickers(r.Context(), uid(r), limit)
	} else {
		items, err = x.app.FavoriteStickers(r.Context(), uid(r), limit)
	}
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, stickerItemJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}
