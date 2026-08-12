package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
)

// wukongDataSourceRequest is the exact command envelope used by the pinned
// WuKongIM server datasource implementation. It is intentionally separate
// from the client-facing /v2/im/datasource APIs.
type wukongDataSourceRequest struct {
	CMD  string `json:"cmd"`
	Data struct {
		ChannelID   string `json:"channel_id"`
		ChannelType uint8  `json:"channel_type"`
	} `json:"data"`
}

func (x *API) wukongServerDataSource(w http.ResponseWriter, r *http.Request) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil || (!ip.IsPrivate() && !ip.IsLoopback()) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "internal endpoint")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request wukongDataSourceRequest
	if err = decoder.Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid datasource request")
		return
	}

	switch request.CMD {
	case "getSystemUIDs":
		ids, listErr := x.app.InternalWukongSystemUIDs(r.Context())
		if listErr != nil {
			writeError(w, http.StatusServiceUnavailable, "DATASOURCE_UNAVAILABLE", "system account datasource unavailable")
			return
		}
		write(w, http.StatusOK, ids)
	case "getSubscribers":
		if strings.TrimSpace(request.Data.ChannelID) == "" {
			write(w, http.StatusOK, []string{})
			return
		}
		if request.Data.ChannelType == 2 {
			ids, listErr := x.app.InternalConversationMemberIDs(r.Context(), request.Data.ChannelID)
			if listErr != nil {
				writeError(w, http.StatusNotFound, "CHANNEL_NOT_FOUND", "channel not found")
				return
			}
			write(w, http.StatusOK, ids)
			return
		}
		snapshot, loadErr := x.app.InternalWukongChannelSnapshot(r.Context(), request.Data.ChannelID, request.Data.ChannelType)
		if loadErr != nil {
			writeError(w, http.StatusNotFound, "CHANNEL_NOT_FOUND", "channel not found")
			return
		}
		write(w, http.StatusOK, snapshot.Subscribers)
	case "getBlacklist", "getWhitelist":
		if request.Data.ChannelType == 1 || strings.TrimSpace(request.Data.ChannelID) == "" {
			// Person-channel access is already kept in WuKong through the user
			// friendship outbox and periodic reconciler.
			write(w, http.StatusOK, []string{})
			return
		}
		snapshot, loadErr := x.app.InternalWukongChannelSnapshot(r.Context(), request.Data.ChannelID, request.Data.ChannelType)
		if loadErr != nil {
			writeError(w, http.StatusNotFound, "CHANNEL_NOT_FOUND", "channel not found")
			return
		}
		if request.CMD == "getBlacklist" {
			write(w, http.StatusOK, snapshot.Denylist)
		} else {
			write(w, http.StatusOK, snapshot.Allowlist)
		}
	case "getChannelInfo":
		snapshot, loadErr := x.app.InternalWukongChannelSnapshot(r.Context(), request.Data.ChannelID, request.Data.ChannelType)
		if loadErr != nil {
			writeError(w, http.StatusNotFound, "CHANNEL_NOT_FOUND", "channel not found")
			return
		}
		// channelInfoOn stays disabled for the pinned server because its source
		// currently discards this response. Keep the exact wire response ready.
		write(w, http.StatusOK, map[string]int{"large": snapshot.Large, "ban": snapshot.Ban, "disband": snapshot.Disband})
	default:
		writeError(w, http.StatusBadRequest, "UNSUPPORTED_COMMAND", "unsupported datasource command")
	}
}
