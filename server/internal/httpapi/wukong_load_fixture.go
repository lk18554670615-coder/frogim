package httpapi

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net/http"
	"strings"

	"github.com/linli/im/server/internal/wukong"
)

type wukongLoadPair struct {
	Sender   *wukong.ImSession `json:"sender"`
	Receiver *wukong.ImSession `json:"receiver"`
}

func (x *API) wukongLoadPairs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !x.cfg.DevMode || !x.authorizedWukongInternal(r) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "internal development endpoint")
		return
	}
	var request struct {
		RunID string `json:"runId"`
		Pairs int    `json:"pairs"`
	}
	if decode(r, &request) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "invalid request")
		return
	}
	request.RunID = strings.TrimSpace(request.RunID)
	if request.Pairs < 1 || request.Pairs > 50 || !validLoadRunID(request.RunID) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "runId and 1-50 pairs are required")
		return
	}
	digest := sha256.Sum256([]byte(request.RunID))
	phoneBase := binary.BigEndian.Uint32(digest[:4]) % 100000
	pairs := make([]wukongLoadPair, 0, request.Pairs)
	for pairIndex := 0; pairIndex < request.Pairs; pairIndex++ {
		tail := fmt.Sprintf("%05d%03d", phoneBase, pairIndex)
		sender, err := x.app.Login("139"+tail, fmt.Sprintf("Load policy sender %d", pairIndex))
		if err != nil {
			handleErr(w, err)
			return
		}
		receiver, err := x.app.Login("138"+tail, fmt.Sprintf("Load policy receiver %d", pairIndex))
		if err != nil {
			handleErr(w, err)
			return
		}
		friend, err := x.app.RequestFriend(sender.ID, receiver.ID, "WuKong performance policy probe")
		if err != nil {
			handleErr(w, err)
			return
		}
		if err = x.app.AcceptFriend(receiver.ID, friend.ID); err != nil {
			handleErr(w, err)
			return
		}
		if _, err = x.app.DirectConversation(sender.ID, receiver.ID); err != nil {
			handleErr(w, err)
			return
		}
		senderSession, err := x.issueIMSession(r.Context(), sender.ID, "android")
		if err != nil || senderSession == nil {
			writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "sender ImSession unavailable")
			return
		}
		receiverSession, err := x.issueIMSession(r.Context(), receiver.ID, "android")
		if err != nil || receiverSession == nil {
			writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "receiver ImSession unavailable")
			return
		}
		pairs = append(pairs, wukongLoadPair{Sender: senderSession, Receiver: receiverSession})
	}
	// The real friendship transaction above still emits the durable Outbox work
	// used in production. A load run, however, must begin from a settled fixture
	// instead of racing that asynchronous projection. Re-applying the same
	// idempotent allowlist mutations here makes the development fixture stable
	// without changing or bypassing the production mutation path.
	for _, pair := range pairs {
		if err := x.wukongClient.AddAllowlist(r.Context(), pair.Sender.UID, wukong.ChannelPerson, []string{pair.Receiver.UID}); err != nil {
			writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "sender allowlist unavailable")
			return
		}
		if err := x.wukongClient.AddAllowlist(r.Context(), pair.Receiver.UID, wukong.ChannelPerson, []string{pair.Sender.UID}); err != nil {
			writeError(w, http.StatusServiceUnavailable, "IM_UNAVAILABLE", "receiver allowlist unavailable")
			return
		}
	}
	write(w, http.StatusOK, map[string]any{"runId": request.RunID, "pairs": pairs})
}

func validLoadRunID(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}
