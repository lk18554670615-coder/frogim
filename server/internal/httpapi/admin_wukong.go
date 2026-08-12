package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukong"
	"github.com/linli/im/server/internal/wukongplugin"
)

func (x *API) requireWukongAdmin(w http.ResponseWriter) bool {
	if !x.cfg.WukongEnabled || x.wukongClient == nil || x.wukongSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "WUKONG_UNAVAILABLE", "WuKongIM management is unavailable")
		return false
	}
	return true
}

func (x *API) adminWukongOverview(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	value, err := x.wukongClient.ManagerVarz(r.Context())
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	// These values are internal credentials or addresses that have no browser
	// use. Drop them even if a future pinned server starts returning them.
	delete(value, "manager_token")
	delete(value, "manager_uid")
	write(w, http.StatusOK, value)
}

func (x *API) adminWukongSettings(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	value, err := x.wukongClient.ManagerSettings(r.Context())
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func (x *API) adminWukongNodes(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	value, err := x.wukongClient.ManagerNodes(r.Context())
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func redactWukongDeviceTokens(value map[string]any) {
	items, _ := value["data"].([]any)
	for _, value := range items {
		item, _ := value.(map[string]any)
		if item == nil {
			continue
		}
		token := strings.TrimSpace(valueString(item["token"]))
		delete(item, "token")
		item["token_on"] = token != ""
	}
}

type userIMDeviceSession struct {
	DeviceFlag      int   `json:"deviceFlag"`
	DeviceLevel     int   `json:"deviceLevel"`
	ConnectionCount int   `json:"connectionCount"`
	UpdatedAt       int64 `json:"updatedAt"`
}

func (x *API) userIMDevices(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	value, err := x.wukongClient.ManagerDevices(r.Context(), wukong.ManagerDeviceQuery{
		UID: uid(r), DeviceFlag: -1, Limit: 20,
	})
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	items, _ := value["data"].([]any)
	sessions := make([]userIMDeviceSession, 0, len(items))
	for _, value := range items {
		item, _ := value.(map[string]any)
		if item == nil || strings.TrimSpace(valueString(item["token"])) == "" {
			continue
		}
		flag := managerInt64(item["device_flag"])
		if flag < wukong.DeviceApp || flag > wukong.DeviceDesktop {
			continue
		}
		sessions = append(sessions, userIMDeviceSession{
			DeviceFlag: int(flag), DeviceLevel: int(managerInt64(item["device_level"])),
			ConnectionCount: int(managerInt64(item["conn_count"])), UpdatedAt: managerInt64(item["updated_at"]),
		})
	}
	write(w, http.StatusOK, map[string]any{"items": sessions})
}

func (x *API) quitUserIMDevice(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	deviceFlag, err := strconv.Atoi(strings.TrimSpace(r.PathValue("deviceFlag")))
	if err != nil || deviceFlag < wukong.DeviceApp || deviceFlag > wukong.DeviceDesktop {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "deviceFlag is invalid")
		return
	}
	userID := uid(r)
	if err = x.wukongClient.QuitDevice(r.Context(), userID, deviceFlag); err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	if err = x.app.InvalidateWukongCredential(r.Context(), userID, deviceFlag); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DEVICE_STATE_UNAVAILABLE", "device was disconnected; credential state could not be refreshed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func managerInt64(value any) int64 {
	switch number := value.(type) {
	case int:
		return int64(number)
	case int64:
		return number
	case float64:
		return int64(number)
	case string:
		parsed, _ := strconv.ParseInt(number, 10, 64)
		return parsed
	default:
		return 0
	}
}

func (x *API) adminWukongConnections(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	limit, ok := boundedQueryInt(r, "limit", 20, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 200")
		return
	}
	page, ok := boundedQueryInt(r, "page", 1, 1, 1000000)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "page is invalid")
		return
	}
	value, err := x.wukongClient.Connections(r.Context(), r.URL.Query().Get("uid"), (page-1)*limit, limit)
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func (x *API) adminWukongChannels(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	limit, ok := boundedQueryInt(r, "limit", 20, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 200")
		return
	}
	channelType, ok := boundedQueryInt(r, "channelType", 0, 0, 255)
	if !ok || (channelType != 0 && !wukong.SupportedChannelType(uint8(channelType))) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is unsupported")
		return
	}
	offset, ok := nonNegativeQueryInt64(r, "offsetCreatedAt")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "offsetCreatedAt is invalid")
		return
	}
	value, err := x.wukongClient.ManagerChannels(r.Context(), wukong.ManagerChannelQuery{
		ChannelID: r.URL.Query().Get("channelId"), ChannelType: uint8(channelType),
		OffsetCreated: offset, Previous: r.URL.Query().Get("pre") == "1", Limit: limit,
	})
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func (x *API) adminWukongMessages(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	limit, ok := boundedQueryInt(r, "limit", 20, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 200")
		return
	}
	channelType, ok := boundedQueryInt(r, "channelType", 0, 0, 255)
	if !ok || (channelType != 0 && !wukong.SupportedChannelType(uint8(channelType))) {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "channelType is unsupported")
		return
	}
	messageID, ok := nonNegativeQueryInt64(r, "messageId")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "messageId is invalid")
		return
	}
	offsetID, ok := nonNegativeQueryInt64(r, "offsetMessageId")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "offsetMessageId is invalid")
		return
	}
	offsetSeq, ok := nonNegativeQueryUint64(r, "offsetMessageSeq")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "offsetMessageSeq is invalid")
		return
	}
	value, err := x.wukongClient.ManagerMessages(r.Context(), wukong.ManagerMessageQuery{
		FromUID: r.URL.Query().Get("fromUid"), ChannelID: r.URL.Query().Get("channelId"),
		ChannelType: uint8(channelType), MessageID: messageID, ClientMsgNo: r.URL.Query().Get("clientMsgNo"),
		OffsetMessageID: offsetID, OffsetMessageSeq: offsetSeq, Previous: r.URL.Query().Get("pre") == "1", Limit: limit,
	})
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, value)
}

func (x *API) adminWukongDevices(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	limit, ok := boundedQueryInt(r, "limit", 20, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 200")
		return
	}
	deviceFlag, ok := boundedQueryInt(r, "deviceFlag", -1, -1, wukong.DeviceDesktop)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "deviceFlag is invalid")
		return
	}
	offset, ok := nonNegativeQueryInt64(r, "offsetCreatedAt")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "offsetCreatedAt is invalid")
		return
	}
	value, err := x.wukongClient.ManagerDevices(r.Context(), wukong.ManagerDeviceQuery{
		UID: r.URL.Query().Get("uid"), DeviceFlag: deviceFlag, OffsetCreated: offset,
		Previous: r.URL.Query().Get("pre") == "1", Limit: limit,
	})
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	redactWukongDeviceTokens(value)
	write(w, http.StatusOK, value)
}

func (x *API) quitWukongDevice(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	var request struct {
		DeviceFlag int    `json:"deviceFlag"`
		Reason     string `json:"reason"`
		Confirmed  bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil || !confirmedReason(request.Confirmed, request.Reason) || request.DeviceFlag < -1 || request.DeviceFlag > wukong.DeviceDesktop {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "valid deviceFlag, confirmed and reason are required")
		return
	}
	userID := strings.TrimSpace(r.PathValue("uid"))
	if userID == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "uid is required")
		return
	}
	if err := x.wukongClient.QuitDevice(r.Context(), userID, request.DeviceFlag); err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	// The pinned server clears the token on device_quit. Forget the local
	// provision marker so the next authenticated login installs it again.
	if err := x.app.InvalidateWukongCredential(r.Context(), userID, request.DeviceFlag); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DEVICE_STATE_UNAVAILABLE", "device was disconnected; credential state could not be refreshed")
		return
	}
	x.app.RecordAdminAudit(uid(r), "wukong.device.quit", "user_device", userID, "success", x.clientIP(r), map[string]any{"deviceFlag": request.DeviceFlag, "reason": strings.TrimSpace(request.Reason)})
	w.WriteHeader(http.StatusNoContent)
}

func wukongSystemUserJSON(item *store.WukongSystemUser) map[string]any {
	return map[string]any{
		"userId": item.UserID, "name": item.Name, "enabled": item.Enabled,
		"syncStatus": item.SyncStatus, "updatedBy": item.UpdatedBy,
		"reason": item.Reason, "updatedAt": item.UpdatedAt,
	}
}

func (x *API) adminWukongSystemUsers(w http.ResponseWriter, r *http.Request) {
	items, err := x.app.WukongSystemUsers(r.Context())
	if err != nil {
		handleErr(w, err)
		return
	}
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		result = append(result, wukongSystemUserJSON(item))
	}
	write(w, http.StatusOK, map[string]any{"items": result})
}

func (x *API) setWukongSystemUser(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled   bool   `json:"enabled"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	userID := strings.TrimSpace(r.PathValue("uid"))
	if decode(r, &request) != nil || userID == "" || !confirmedReason(request.Confirmed, request.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "uid, confirmed and reason are required")
		return
	}
	item, err := x.app.SetWukongSystemUser(r.Context(), userID, request.Enabled, uid(r), strings.TrimSpace(request.Reason), time.Now().UTC())
	if err != nil {
		handleErr(w, err)
		return
	}
	action := "wukong.system_user.remove"
	if request.Enabled {
		action = "wukong.system_user.add"
	}
	x.app.RecordAdminAudit(uid(r), action, "system_user", userID, "processing", x.clientIP(r), map[string]any{"reason": strings.TrimSpace(request.Reason)})
	write(w, http.StatusAccepted, map[string]any{"item": wukongSystemUserJSON(item)})
}

func (x *API) adminWukongPlugins(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	nodeID, ok := nonNegativeQueryUint64(r, "nodeId")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "nodeId is invalid")
		return
	}
	items, err := x.wukongClient.ManagerPlugins(r.Context(), nodeID)
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	releases, releaseErr := x.app.WukongPluginReleases(r.Context())
	if releaseErr != nil && !errors.Is(releaseErr, store.ErrUnsupported) {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin lifecycle store is unavailable")
		return
	}
	releaseByNo := map[string]*store.WukongPluginRelease{}
	for _, release := range releases {
		releaseByNo[release.PluginNo] = release
	}
	seen := map[string]bool{}
	for _, item := range items {
		pluginNo := valueString(item["no"])
		seen[pluginNo] = true
		item["status"] = pluginStatusString(item["status"])
		if release := releaseByNo[pluginNo]; release != nil {
			item["managed"] = true
			item["verified"] = true
			item["lifecycle_status"] = release.Status
			item["sha256"] = release.SHA256
			item["key_id"] = release.KeyID
			item["file_name"] = release.FileName
			item["installed_at"] = release.InstalledAt
			item["updated_at"] = release.UpdatedAt
			if release.Status == "disabled" || release.Status == "failed" {
				item["status"] = release.Status
			}
		} else {
			item["managed"] = false
			item["verified"] = pluginNo == protectedPolicyPluginNo
			item["built_in"] = pluginNo == protectedPolicyPluginNo
		}
	}
	for _, release := range releases {
		if seen[release.PluginNo] || release.Status == "uninstalled" {
			continue
		}
		items = append(items, map[string]any{"no": release.PluginNo, "node_id": release.NodeID, "name": release.Name, "version": release.Version, "methods": release.Methods, "status": release.Status, "managed": true, "verified": true, "lifecycle_status": release.Status, "sha256": release.SHA256, "key_id": release.KeyID, "file_name": release.FileName, "installed_at": release.InstalledAt, "updated_at": release.UpdatedAt})
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func (x *API) adminWukongPluginLogs(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	pluginNo := strings.TrimSpace(r.PathValue("no"))
	if pluginNo == "" {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "plugin number is required")
		return
	}
	nodeID, ok := nonNegativeQueryUint64(r, "nodeId")
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "nodeId is invalid")
		return
	}
	limit, ok := boundedQueryInt(r, "limit", 100, 1, 500)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 500")
		return
	}
	logs, err := x.wukongClient.ManagerPluginLogs(r.Context(), pluginNo, nodeID, limit)
	if err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	write(w, http.StatusOK, logs)
}

func (x *API) updateWukongPluginConfig(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	var request struct {
		NodeID    uint64         `json:"nodeId"`
		Config    map[string]any `json:"config"`
		Reason    string         `json:"reason"`
		Confirmed bool           `json:"confirmed"`
	}
	if decode(r, &request) != nil || request.NodeID == 0 || request.Config == nil || !confirmedReason(request.Confirmed, request.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "nodeId, config, confirmed and reason are required")
		return
	}
	pluginNo := strings.TrimSpace(r.PathValue("no"))
	if err := x.wukongClient.UpdatePluginConfig(r.Context(), pluginNo, request.NodeID, request.Config); err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	x.app.RecordAdminAudit(uid(r), "wukong.plugin.config", "wukong_plugin", pluginNo, "success", x.clientIP(r), map[string]any{"nodeId": request.NodeID, "reason": strings.TrimSpace(request.Reason)})
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) uninstallWukongPlugin(w http.ResponseWriter, r *http.Request) {
	if !x.requireWukongAdmin(w) {
		return
	}
	var request struct {
		NodeID    uint64 `json:"nodeId"`
		Reason    string `json:"reason"`
		Confirmed bool   `json:"confirmed"`
	}
	if decode(r, &request) != nil || request.NodeID == 0 || !confirmedReason(request.Confirmed, request.Reason) {
		writeError(w, http.StatusBadRequest, "CONFIRMATION_REQUIRED", "nodeId, confirmed and reason are required")
		return
	}
	pluginNo := strings.TrimSpace(r.PathValue("no"))
	if pluginNo == protectedPolicyPluginNo {
		writeError(w, http.StatusConflict, "PROTECTED_PLUGIN", "the mandatory send-policy plugin cannot be uninstalled")
		return
	}
	if err := x.wukongClient.UninstallPlugin(r.Context(), pluginNo, request.NodeID); err != nil {
		x.writeWukongAdminError(w, r, err)
		return
	}
	if release, releaseErr := x.app.WukongPluginRelease(r.Context(), pluginNo); releaseErr == nil && release.NodeID == request.NodeID && x.pluginInstaller != nil {
		removeErr := x.pluginInstaller.Remove(release.FileName)
		if removeErr != nil && !errors.Is(removeErr, wukongplugin.ErrNotInstalled) {
			writeError(w, http.StatusInternalServerError, "PLUGIN_LIFECYCLE_FAILED", "plugin metadata was removed but its managed file could not be cleaned up")
			return
		}
		release.Status, release.LastActor, release.LastReason = "uninstalled", uid(r), strings.TrimSpace(request.Reason)
		if _, saveErr := x.app.SaveWukongPluginRelease(r.Context(), *release); saveErr != nil {
			writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin was uninstalled but lifecycle metadata could not be committed")
			return
		}
		x.recordPluginLifecycle(r, pluginNo, "uninstall", "uninstalled", request.Reason, map[string]any{"nodeId": request.NodeID, "version": release.Version})
	} else {
		x.app.RecordAdminAudit(uid(r), "wukong.plugin.uninstall", "wukong_plugin", pluginNo, "success", x.clientIP(r), map[string]any{"nodeId": request.NodeID, "reason": strings.TrimSpace(request.Reason), "managed": false})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (x *API) writeWukongAdminError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("WuKongIM admin request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusBadGateway, "WUKONG_UPSTREAM_ERROR", "WuKongIM management request failed")
}

func confirmedReason(confirmed bool, reason string) bool {
	reason = strings.TrimSpace(reason)
	return confirmed && reason != "" && len([]rune(reason)) <= 500
}

func boundedQueryInt(r *http.Request, key string, fallback, minimum, maximum int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= minimum && value <= maximum
}

func nonNegativeQueryInt64(r *http.Request, key string) (int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	return value, err == nil && value >= 0
}

func nonNegativeQueryUint64(r *http.Request, key string) (uint64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0, true
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	return value, err == nil
}
