package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	livekitcontrol "github.com/linli/im/server/internal/livekit"
)

type livekitAdminControl interface {
	ListRooms(context.Context) ([]livekitcontrol.RoomSummary, error)
	ListParticipants(context.Context, string) ([]livekitcontrol.ParticipantSummary, error)
	Metrics(context.Context) (livekitcontrol.MetricsSummary, error)
	RemoveParticipant(context.Context, string, string) error
	DeleteRoom(context.Context, string) error
}

func (x *API) requireLiveKitAdmin(w http.ResponseWriter) (livekitAdminControl, bool) {
	admin, ok := x.livekit.(livekitAdminControl)
	if !x.cfg.LiveKitEnabled || x.livekit == nil || x.livekitSetupErr != nil || !ok {
		writeError(w, http.StatusServiceUnavailable, "LIVEKIT_UNAVAILABLE", "LiveKit management is unavailable")
		return nil, false
	}
	return admin, true
}

func (x *API) adminLiveKitMetrics(w http.ResponseWriter, r *http.Request) {
	admin, ok := x.requireLiveKitAdmin(w)
	if !ok {
		return
	}
	metrics, err := admin.Metrics(r.Context())
	if err != nil {
		x.writeLiveKitAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, metrics)
}

func (x *API) adminLiveKitRooms(w http.ResponseWriter, r *http.Request) {
	admin, ok := x.requireLiveKitAdmin(w)
	if !ok {
		return
	}
	items, err := admin.ListRooms(r.Context())
	if err != nil {
		x.writeLiveKitAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "maxParticipantsPerRoom": livekitcontrol.MaxCallParticipants})
}

func (x *API) adminLiveKitParticipants(w http.ResponseWriter, r *http.Request) {
	admin, ok := x.requireLiveKitAdmin(w)
	if !ok {
		return
	}
	room := strings.TrimSpace(r.PathValue("room"))
	items, err := admin.ListParticipants(r.Context(), room)
	if err != nil {
		x.writeLiveKitAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (x *API) removeLiveKitParticipant(w http.ResponseWriter, r *http.Request) {
	admin, ok := x.requireLiveKitAdmin(w)
	if !ok {
		return
	}
	var request struct {
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil || !confirmedReason(request.Confirmed, request.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	room, identity := strings.TrimSpace(r.PathValue("room")), strings.TrimSpace(r.PathValue("identity"))
	if room == "" || identity == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "room and identity are required")
		return
	}
	if err := admin.RemoveParticipant(r.Context(), room, identity); err != nil {
		x.writeLiveKitAdminError(w, r, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "livekit.participant.remove", "livekit_participant", room+"/"+identity, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) deleteLiveKitRoom(w http.ResponseWriter, r *http.Request) {
	admin, ok := x.requireLiveKitAdmin(w)
	if !ok {
		return
	}
	var request struct {
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil || !confirmedReason(request.Confirmed, request.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "confirmed and reason are required")
		return
	}
	room := strings.TrimSpace(r.PathValue("room"))
	if !strings.HasPrefix(room, "call_") {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "only application call rooms can be deleted")
		return
	}
	if err := admin.DeleteRoom(r.Context(), room); err != nil {
		x.writeLiveKitAdminError(w, r, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "livekit.room.delete", "livekit_room", room, "success", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) writeLiveKitAdminError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("LiveKit admin request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusBadGateway, "LIVEKIT_UPSTREAM_ERROR", "LiveKit management request failed")
}
