package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/linli/im/server/internal/app"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
)

const wukongPolicySecretHeader = "X-IM-Wukong-Policy-Secret"

type wukongPolicySendRequest struct {
	FromUID     string `json:"fromUid"`
	ChannelID   string `json:"channelId"`
	ChannelType uint8  `json:"channelType"`
	Payload     []byte `json:"payload"`
	DeviceID    string `json:"deviceId,omitempty"`
	DeviceFlag  uint32 `json:"deviceFlag,omitempty"`
	DeviceLevel uint32 `json:"deviceLevel,omitempty"`
}

type wukongPolicySendResponse struct {
	Allowed    bool   `json:"allowed"`
	ReasonCode uint8  `json:"reasonCode"`
	Code       string `json:"code"`
}

func (x *API) wukongSendPolicy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !x.authorizedWukongInternal(r) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", "internal endpoint")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request wukongPolicySendRequest
	if err := decoder.Decode(&request); err != nil || decodeHasTrailingJSON(decoder) {
		write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: wukong.ReasonPayloadDecodeError, Code: "INVALID_REQUEST"})
		return
	}
	request.FromUID = strings.TrimSpace(request.FromUID)
	request.ChannelID = strings.TrimSpace(request.ChannelID)
	if request.FromUID == "" || request.ChannelID == "" || len(request.FromUID) > 128 || len(request.ChannelID) > 256 || len(request.DeviceID) > 256 {
		write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: wukong.ReasonChannelIDError, Code: "INVALID_CHANNEL"})
		return
	}
	input, mediaID, reason, code := parseWukongClientPolicyPayload(request)
	if reason != wukong.ReasonSuccess {
		write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: reason, Code: code})
		return
	}
	route, err := x.app.AuthorizeWukongClientMessage(r.Context(), input)
	if err != nil {
		reason, code = wukongPolicyError(err)
		write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: reason, Code: code})
		return
	}
	if mediaID != "" {
		media, mediaErr := x.app.GetMedia(mediaID)
		if mediaErr != nil || !wukongMediaMIMEMatches(input.ContentType, media.MIME) ||
			(input.MediaMIME != "" && normalizeWukongMIME(input.MediaMIME) != normalizeWukongMIME(media.MIME)) {
			write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: wukong.ReasonNotAllowSend, Code: "MEDIA_TYPE_MISMATCH"})
			return
		}
		if input.ContentType == wukong.ContentTypeVideo {
			var video struct {
				CoverMediaID   string `json:"coverMediaId"`
				LocalPath      string `json:"localPath"`
				CoverLocalPath string `json:"coverLocalPath"`
			}
			if json.Unmarshal(request.Payload, &video) != nil || (video.CoverMediaID != "" && video.CoverMediaID != media.CoverMediaID) || video.LocalPath != "" || video.CoverLocalPath != "" {
				write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: wukong.ReasonNotAllowSend, Code: "INVALID_VIDEO_COVER"})
				return
			}
		}
		if err = x.app.BindMediaChannel(store.MediaChannelBinding{
			MediaID: mediaID, ChannelID: route.ChannelID,
			ChannelType: route.ChannelType, SenderID: request.FromUID,
		}); err != nil {
			reason, code = wukongPolicyError(err)
			write(w, http.StatusOK, wukongPolicySendResponse{ReasonCode: reason, Code: code})
			return
		}
	}
	write(w, http.StatusOK, wukongPolicySendResponse{Allowed: true, ReasonCode: wukong.ReasonSuccess, Code: "ALLOW"})
}

func (x *API) authorizedWukongInternal(r *http.Request) bool {
	return x.cfg.WukongPolicySecret != "" && internalRemote(r.RemoteAddr) && subtle.ConstantTimeCompare(
		[]byte(r.Header.Get(wukongPolicySecretHeader)), []byte(x.cfg.WukongPolicySecret),
	) == 1
}

func internalRemote(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

func decodeHasTrailingJSON(decoder *json.Decoder) bool {
	var trailing any
	err := decoder.Decode(&trailing)
	return !errors.Is(err, io.EOF)
}

func parseWukongClientPolicyPayload(request wukongPolicySendRequest) (store.WukongClientMessageInput, string, uint8, string) {
	input := store.WukongClientMessageInput{
		UserID: request.FromUID, ChannelID: request.ChannelID, ChannelType: request.ChannelType,
	}
	if len(request.Payload) == 0 || len(request.Payload) > 1<<20 {
		return input, "", wukong.ReasonPayloadDecodeError, "INVALID_PAYLOAD"
	}
	decoder := json.NewDecoder(strings.NewReader(string(request.Payload)))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil || decodeHasTrailingJSON(decoder) {
		return input, "", wukong.ReasonPayloadDecodeError, "INVALID_PAYLOAD"
	}
	contentNumber, ok := payload["type"].(json.Number)
	if !ok {
		return input, "", wukong.ReasonPayloadDecodeError, "INVALID_CONTENT_TYPE"
	}
	contentType64, err := contentNumber.Int64()
	if err != nil || contentType64 < 1 || contentType64 > 1<<31-1 {
		return input, "", wukong.ReasonPayloadDecodeError, "INVALID_CONTENT_TYPE"
	}
	contentType := int(contentType64)
	input.ContentType = contentType
	input.Type = map[int]string{
		wukong.ContentTypeText: "text", wukong.ContentTypeImage: "image", wukong.ContentTypeGIF: "image",
		wukong.ContentTypeVoice: "audio", wukong.ContentTypeVideo: "video", wukong.ContentTypeLocation: "location",
		wukong.ContentTypeCard: "contact", wukong.ContentTypeFile: "file",
		wukong.ContentTypeStoreSticker: "sticker", wukong.ContentTypeMomentShare: "moment",
		wukong.ContentTypeLiveEvent:  "live",
		wukong.ContentTypeScreenshot: "screenshot",
	}[contentType]
	if input.Type == "" {
		// CMD, system events and call state are authoritative server messages.
		// AI and unknown message types stay disabled at this migration stage.
		return input, "", wukong.ReasonNotAllowSend, "SERVER_OWNED_CONTENT_TYPE"
	}
	if contentType >= wukong.ContentTypeMergedHistory {
		version, versionOK := payload["schemaVersion"].(json.Number)
		parsedVersion, versionErr := version.Int64()
		if !versionOK || versionErr != nil || parsedVersion != 1 {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_SCHEMA_VERSION"
		}
	}
	if contentType == wukong.ContentTypeLiveEvent && request.ChannelType != wukong.ChannelLive {
		return input, "", wukong.ReasonNotAllowSend, "LIVE_CHANNEL_REQUIRED"
	}
	if contentType == wukong.ContentTypeLiveEvent {
		event, eventOK := payload["event"].(string)
		if !eventOK || !allowedWukongLiveEvent(event) {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_LIVE_EVENT"
		}
	}
	if contentType == wukong.ContentTypeScreenshot {
		if event, eventOK := payload["event"].(string); !eventOK || event != "screenshot.taken" {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_SCREENSHOT_EVENT"
		}
	}
	if contentType == wukong.ContentTypeText {
		text, textOK := payload["content"].(string)
		if !textOK || strings.TrimSpace(text) == "" || len([]rune(text)) > 10000 {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_TEXT"
		}
		input.Text = text
	}
	if contentType == wukong.ContentTypeLocation {
		latitude, latitudeOK := wukongJSONFloat(payload["latitude"])
		longitude, longitudeOK := wukongJSONFloat(payload["longitude"])
		if !latitudeOK || !longitudeOK || latitude < -90 || latitude > 90 || longitude < -180 || longitude > 180 {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_LOCATION"
		}
	}
	if contentType == wukong.ContentTypeCard {
		userID, userOK := payload["userId"].(string)
		userID = strings.TrimSpace(userID)
		if !userOK || userID == "" || len(userID) > 128 {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_CONTACT_CARD"
		}
	}
	if rawExpiry, exists := payload["expiresAt"]; exists {
		expiryText, expiryOK := rawExpiry.(string)
		expiresAt, expiryErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(expiryText))
		remaining := time.Until(expiresAt)
		if !expiryOK || expiryErr != nil || remaining < 30*time.Second || remaining > 30*24*time.Hour+5*time.Minute {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_EXPIRY"
		}
	}
	if contentType == wukong.ContentTypeMomentShare {
		momentID, momentOK := payload["momentId"].(string)
		input.ResourceID = strings.TrimSpace(momentID)
		if !momentOK || input.ResourceID == "" || len(input.ResourceID) > 128 {
			return input, "", wukong.ReasonNotAllowSend, "MOMENT_REFERENCE_REQUIRED"
		}
	}
	if contentType == wukong.ContentTypeStoreSticker {
		stickerID, stickerOK := payload["stickerId"].(string)
		input.ResourceID = strings.TrimSpace(stickerID)
		if !stickerOK || input.ResourceID == "" || len(input.ResourceID) > 128 {
			return input, "", wukong.ReasonNotAllowSend, "STICKER_REFERENCE_REQUIRED"
		}
	}
	if replyValue, exists := payload["reply"]; exists {
		reply, replyOK := replyValue.(map[string]any)
		messageID, messageOK := reply["message_id"].(string)
		if !replyOK || !messageOK || strings.TrimSpace(messageID) == "" || len(messageID) > 128 {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_REPLY"
		}
		input.ReplyToID = strings.TrimSpace(messageID)
	}
	if mentionValue, exists := payload["mention"]; exists {
		mention, mentionOK := mentionValue.(map[string]any)
		if !mentionOK {
			return input, "", wukong.ReasonPayloadDecodeError, "INVALID_MENTION"
		}
		if uidsValue, hasUIDs := mention["uids"]; hasUIDs {
			uids, uidsOK := uidsValue.([]any)
			if !uidsOK || len(uids) > 500 {
				return input, "", wukong.ReasonPayloadDecodeError, "INVALID_MENTION"
			}
			seen := make(map[string]struct{}, len(uids))
			for _, uidValue := range uids {
				mentionedUID, uidOK := uidValue.(string)
				mentionedUID = strings.TrimSpace(mentionedUID)
				if !uidOK || mentionedUID == "" || len(mentionedUID) > 128 {
					return input, "", wukong.ReasonPayloadDecodeError, "INVALID_MENTION"
				}
				if _, duplicate := seen[mentionedUID]; !duplicate {
					seen[mentionedUID] = struct{}{}
					input.Mentions = append(input.Mentions, mentionedUID)
				}
			}
		}
		if all, exists := mention["all"]; exists {
			switch value := all.(type) {
			case bool:
				input.MentionAll = value
			case json.Number:
				number, numberErr := value.Int64()
				if numberErr != nil || number != 1 {
					return input, "", wukong.ReasonPayloadDecodeError, "INVALID_MENTION"
				}
				input.MentionAll = true
			default:
				return input, "", wukong.ReasonPayloadDecodeError, "INVALID_MENTION"
			}
		}
	}
	mediaID := ""
	switch contentType {
	case wukong.ContentTypeImage, wukong.ContentTypeGIF, wukong.ContentTypeVoice, wukong.ContentTypeVideo, wukong.ContentTypeFile:
		mediaID, ok = payload["mediaId"].(string)
		mediaID = strings.TrimSpace(mediaID)
		if !ok || mediaID == "" || len(mediaID) > 128 {
			return input, "", wukong.ReasonNotAllowSend, "MEDIA_REFERENCE_REQUIRED"
		}
		if claimed, exists := payload["mime"]; exists {
			mime, mimeOK := claimed.(string)
			mime = normalizeWukongMIME(mime)
			if !mimeOK || mime == "" || len(mime) > 255 {
				return input, "", wukong.ReasonPayloadDecodeError, "INVALID_MEDIA_MIME"
			}
			input.MediaMIME = mime
		}
	}
	return input, mediaID, wukong.ReasonSuccess, "ALLOW"
}

func allowedWukongLiveEvent(event string) bool {
	switch event {
	case "live.like", "live.applause", "live.follow":
		return true
	default:
		return false
	}
}

func wukongMediaMIMEMatches(contentType int, rawMIME string) bool {
	mime := normalizeWukongMIME(rawMIME)
	switch contentType {
	case wukong.ContentTypeImage:
		return strings.HasPrefix(mime, "image/") && mime != "image/gif"
	case wukong.ContentTypeGIF:
		return mime == "image/gif"
	case wukong.ContentTypeVoice:
		return strings.HasPrefix(mime, "audio/")
	case wukong.ContentTypeVideo:
		return strings.HasPrefix(mime, "video/")
	case wukong.ContentTypeFile:
		// The upload service already limits file messages to its allow-list.
		// A generic file intentionally remains capable of carrying an image,
		// audio, video, PDF or opaque binary attachment.
		return mime != ""
	default:
		return false
	}
}

func normalizeWukongMIME(raw string) string {
	return strings.ToLower(strings.TrimSpace(strings.SplitN(raw, ";", 2)[0]))
}

func wukongJSONFloat(value any) (float64, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	parsed, err := number.Float64()
	return parsed, err == nil
}

func wukongPolicyError(err error) (uint8, string) {
	switch {
	case errors.Is(err, app.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return wukong.ReasonChannelNotExist, "CHANNEL_NOT_FOUND"
	case errors.Is(err, store.ErrUnsupported):
		return wukong.ReasonNotSupportChannelType, "UNSUPPORTED_CHANNEL_TYPE"
	case errors.Is(err, app.ErrForbidden), errors.Is(err, app.ErrConflict), errors.Is(err, store.ErrForbidden), errors.Is(err, store.ErrConflict):
		return wukong.ReasonNotAllowSend, "NOT_ALLOWED"
	default:
		return wukong.ReasonSystemError, "POLICY_UNAVAILABLE"
	}
}
