package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/wukongplugin"
)

const protectedPolicyPluginNo = "wk.plugin.im-policy"

func (x *API) requirePluginLifecycle(w http.ResponseWriter) bool {
	if !x.requireWukongAdmin(w) {
		return false
	}
	if x.pluginInstaller == nil || x.pluginSetupErr != nil {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_LIFECYCLE_UNAVAILABLE", "signed WuKongIM plugin lifecycle is unavailable")
		return false
	}
	return true
}

func (x *API) installWukongPlugin(w http.ResponseWriter, r *http.Request) {
	x.stageWukongPlugin(w, r, false)
}

func (x *API) upgradeWukongPlugin(w http.ResponseWriter, r *http.Request) {
	x.stageWukongPlugin(w, r, true)
}

func (x *API) stageWukongPlugin(w http.ResponseWriter, r *http.Request, upgrade bool) {
	if !x.requirePluginLifecycle(w) {
		return
	}
	upload, err := x.parsePluginUpload(w, r)
	if err != nil {
		x.writePluginLifecycleError(w, err)
		return
	}
	defer upload.close()
	if _, err = x.app.WukongPluginReleases(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin lifecycle store is unavailable")
		return
	}
	manifest, err := x.pluginInstaller.VerifyManifest(upload.manifest, upload.signature)
	if err != nil {
		x.writePluginLifecycleError(w, err)
		return
	}
	if upgrade {
		pathPluginNo := strings.TrimSpace(r.PathValue("no"))
		if pathPluginNo == "" || pathPluginNo != manifest.PluginNo {
			writeError(w, http.StatusBadRequest, "PLUGIN_ID_MISMATCH", "upgrade path and signed manifest plugin numbers differ")
			return
		}
	}
	oldRelease, oldErr := x.app.WukongPluginRelease(r.Context(), manifest.PluginNo)
	if upgrade && oldErr != nil {
		writeError(w, http.StatusNotFound, "PLUGIN_NOT_MANAGED", "plugin has no managed release to upgrade")
		return
	}
	if !upgrade && oldErr == nil && oldRelease.Status != "uninstalled" {
		writeError(w, http.StatusConflict, "PLUGIN_ALREADY_MANAGED", "plugin already has a managed release")
		return
	}
	if oldErr != nil && !errors.Is(oldErr, store.ErrNotFound) {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin lifecycle store is unavailable")
		return
	}
	installation, err := x.pluginInstaller.Stage(upload.manifest, upload.signature, upload.bundle, upgrade)
	if err != nil {
		x.writePluginLifecycleError(w, err)
		return
	}
	rolledBack := false
	rollback := func() {
		if !rolledBack {
			_ = installation.Rollback()
			rolledBack = true
		}
	}
	defer rollback()

	action := "install"
	if upgrade {
		action = "upgrade"
	}
	now := time.Now().UTC()
	release := pluginReleaseFromInstallation(installation, upload.nodeID, "installing", uid(r), upload.reason, nil)
	if _, err = x.app.SaveWukongPluginRelease(r.Context(), release); err != nil {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin lifecycle store is unavailable")
		return
	}
	attestCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	err = x.waitForWukongPlugin(attestCtx, upload.nodeID, manifest)
	cancel()
	if err != nil {
		rollback()
		if upgrade && oldRelease != nil {
			oldRelease.Status = "active"
			oldRelease.LastActor = uid(r)
			oldRelease.LastReason = "automatic rollback after failed attestation"
			_, _ = x.app.SaveWukongPluginRelease(r.Context(), *oldRelease)
		} else {
			release.Status = "failed"
			_, _ = x.app.SaveWukongPluginRelease(r.Context(), release)
		}
		x.recordPluginLifecycle(r, manifest.PluginNo, action, "failed", upload.reason, map[string]any{"version": manifest.Version, "result": "attestation_failed"})
		writeError(w, http.StatusBadGateway, "PLUGIN_ATTESTATION_FAILED", "WuKongIM did not report the signed plugin metadata after startup; the file was rolled back")
		return
	}
	release.Status = "active"
	release.InstalledAt = &now
	saved, err := x.app.SaveWukongPluginRelease(r.Context(), release)
	if err != nil {
		rollback()
		if upgrade && oldRelease != nil {
			_, _ = x.app.SaveWukongPluginRelease(r.Context(), *oldRelease)
		} else {
			release.Status = "failed"
			_, _ = x.app.SaveWukongPluginRelease(r.Context(), release)
		}
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin activation was rolled back because lifecycle metadata could not be committed")
		return
	}
	if err = installation.Commit(); err != nil {
		rollback()
		if upgrade && oldRelease != nil {
			_, _ = x.app.SaveWukongPluginRelease(r.Context(), *oldRelease)
		} else {
			release.Status = "failed"
			_, _ = x.app.SaveWukongPluginRelease(r.Context(), release)
		}
		x.recordPluginLifecycle(r, manifest.PluginNo, action, "failed", upload.reason, map[string]any{"version": manifest.Version, "result": "commit_failed"})
		writeError(w, http.StatusInternalServerError, "PLUGIN_COMMIT_FAILED", "plugin activation could not be committed")
		return
	}
	rolledBack = true
	x.recordPluginLifecycle(r, manifest.PluginNo, action, "active", upload.reason, map[string]any{"version": manifest.Version, "sha256": manifest.SHA256, "keyId": manifest.KeyID})
	write(w, http.StatusCreated, map[string]any{"item": pluginReleaseResponse(saved)})
}

type pluginUpload struct {
	manifest  []byte
	signature string
	reason    string
	nodeID    uint64
	bundle    multipart.File
	form      *multipart.Form
}

func (u *pluginUpload) close() {
	if u.bundle != nil {
		_ = u.bundle.Close()
	}
	if u.form != nil {
		_ = u.form.RemoveAll()
	}
}

func (x *API) parsePluginUpload(w http.ResponseWriter, r *http.Request) (*pluginUpload, error) {
	maximum := x.cfg.WukongPluginMaxBytes
	if maximum == 0 {
		maximum = wukongplugin.DefaultMaxBundleBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maximum+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		return nil, fmt.Errorf("%w: multipart upload", wukongplugin.ErrInvalidManifest)
	}
	upload := &pluginUpload{manifest: []byte(r.FormValue("manifest")), signature: strings.TrimSpace(r.FormValue("signature")), reason: strings.TrimSpace(r.FormValue("reason")), form: r.MultipartForm}
	confirmed, _ := strconv.ParseBool(r.FormValue("confirmed"))
	upload.nodeID, _ = strconv.ParseUint(strings.TrimSpace(r.FormValue("nodeId")), 10, 64)
	if !confirmedReason(confirmed, upload.reason) || upload.nodeID == 0 {
		upload.close()
		return upload, errors.New("confirmation is required")
	}
	bundle, _, err := r.FormFile("bundle")
	if err != nil {
		upload.close()
		return upload, fmt.Errorf("%w: bundle is required", wukongplugin.ErrInvalidManifest)
	}
	upload.bundle = bundle
	return upload, nil
}

func pluginReleaseFromInstallation(installation *wukongplugin.Installation, nodeID uint64, status, actor, reason string, installedAt *time.Time) store.WukongPluginRelease {
	manifest := installation.Manifest
	manifestMap := map[string]any{}
	_ = json.Unmarshal(installation.ManifestJSON, &manifestMap)
	return store.WukongPluginRelease{PluginNo: manifest.PluginNo, NodeID: nodeID, Name: manifest.Name, FileName: manifest.FileName, Version: manifest.Version, Methods: append([]string(nil), manifest.Methods...), SHA256: manifest.SHA256, SizeBytes: manifest.Size, KeyID: manifest.KeyID, Status: status, Manifest: manifestMap, LastActor: actor, LastReason: reason, InstalledAt: installedAt}
}

func manifestFromRelease(release *store.WukongPluginRelease) wukongplugin.Manifest {
	return wukongplugin.Manifest{SchemaVersion: 1, PluginNo: release.PluginNo, Name: release.Name, Version: release.Version, Methods: append([]string(nil), release.Methods...), OS: "linux", Arch: "amd64", FileName: release.FileName, SHA256: release.SHA256, Size: release.SizeBytes, KeyID: release.KeyID}
}

func (x *API) waitForWukongPlugin(ctx context.Context, nodeID uint64, manifest wukongplugin.Manifest) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		items, err := x.wukongClient.ManagerPlugins(ctx, nodeID)
		if err == nil {
			for _, item := range items {
				if valueString(item["no"]) != manifest.PluginNo {
					continue
				}
				methods := valueStrings(item["methods"])
				sort.Strings(methods)
				expectedMethods := append([]string(nil), manifest.Methods...)
				sort.Strings(expectedMethods)
				if valueString(item["name"]) != manifest.Name || valueString(item["version"]) != manifest.Version || !equalStrings(methods, expectedMethods) || !normalPluginStatus(item["status"]) {
					continue
				}
				for _, method := range methods {
					if method == "Receive" {
						return wukongplugin.ErrAIPluginForbidden
					}
				}
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (x *API) disableWukongPlugin(w http.ResponseWriter, r *http.Request) {
	x.setWukongPluginEnabled(w, r, false)
}

func (x *API) enableWukongPlugin(w http.ResponseWriter, r *http.Request) {
	x.setWukongPluginEnabled(w, r, true)
}

func (x *API) setWukongPluginEnabled(w http.ResponseWriter, r *http.Request, enabled bool) {
	if !x.requirePluginLifecycle(w) {
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
	if !enabled && pluginNo == protectedPolicyPluginNo {
		writeError(w, http.StatusConflict, "PROTECTED_PLUGIN", "the mandatory send-policy plugin cannot be disabled")
		return
	}
	release, err := x.app.WukongPluginRelease(r.Context(), pluginNo)
	if err != nil || release.NodeID != request.NodeID {
		writeError(w, http.StatusNotFound, "PLUGIN_NOT_MANAGED", "managed plugin release was not found")
		return
	}
	action, status := "disable", "disabled"
	if enabled {
		action, status = "enable", "active"
		err = x.pluginInstaller.Enable(release.FileName)
		if err == nil {
			attestCtx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			err = x.waitForWukongPlugin(attestCtx, request.NodeID, manifestFromRelease(release))
			cancel()
			if err != nil {
				_ = x.pluginInstaller.Disable(release.FileName)
			}
		}
	} else {
		err = x.pluginInstaller.Disable(release.FileName)
	}
	if err != nil {
		x.recordPluginLifecycle(r, pluginNo, action, "failed", request.Reason, map[string]any{"version": release.Version})
		x.writePluginLifecycleError(w, err)
		return
	}
	release.Status, release.LastActor, release.LastReason = status, uid(r), strings.TrimSpace(request.Reason)
	saved, err := x.app.SaveWukongPluginRelease(r.Context(), *release)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin lifecycle store is unavailable")
		return
	}
	x.recordPluginLifecycle(r, pluginNo, action, status, request.Reason, map[string]any{"version": release.Version})
	write(w, http.StatusOK, map[string]any{"item": pluginReleaseResponse(saved)})
}

func (x *API) adminWukongPluginEvents(w http.ResponseWriter, r *http.Request) {
	limit, ok := boundedQueryInt(r, "limit", 100, 1, 200)
	if !ok {
		writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be between 1 and 200")
		return
	}
	items, err := x.app.WukongPluginEvents(r.Context(), strings.TrimSpace(r.URL.Query().Get("pluginNo")), limit)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_STORE_UNAVAILABLE", "plugin lifecycle store is unavailable")
		return
	}
	responses := make([]map[string]any, 0, len(items))
	for _, item := range items {
		responses = append(responses, map[string]any{"id": item.ID, "pluginNo": item.PluginNo, "action": item.Action, "status": item.Status, "actor": item.Actor, "reason": item.Reason, "details": item.Details, "createdAt": item.CreatedAt})
	}
	write(w, http.StatusOK, map[string]any{"items": responses})
}

func (x *API) recordPluginLifecycle(r *http.Request, pluginNo, action, status, reason string, details map[string]any) {
	_ = x.app.RecordWukongPluginEvent(r.Context(), store.WukongPluginEvent{PluginNo: pluginNo, Action: action, Status: status, Actor: uid(r), Reason: strings.TrimSpace(reason), Details: details, CreatedAt: time.Now().UTC()})
	result := "success"
	if status == "failed" {
		result = "failed"
	}
	x.app.RecordAdminAudit(uid(r), "wukong.plugin."+action, "wukong_plugin", pluginNo, result, x.clientIP(r), map[string]any{"reason": strings.TrimSpace(reason), "status": status, "details": details})
}

func pluginReleaseResponse(item *store.WukongPluginRelease) map[string]any {
	if item == nil {
		return map[string]any{}
	}
	return map[string]any{"pluginNo": item.PluginNo, "nodeId": item.NodeID, "name": item.Name, "fileName": item.FileName, "version": item.Version, "methods": item.Methods, "sha256": item.SHA256, "sizeBytes": item.SizeBytes, "keyId": item.KeyID, "status": item.Status, "lastActor": item.LastActor, "lastReason": item.LastReason, "installedAt": item.InstalledAt, "updatedAt": item.UpdatedAt}
}

func (x *API) writePluginLifecycleError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, wukongplugin.ErrAlreadyInstalled):
		writeError(w, http.StatusConflict, "PLUGIN_ALREADY_INSTALLED", err.Error())
	case errors.Is(err, wukongplugin.ErrNotInstalled):
		writeError(w, http.StatusNotFound, "PLUGIN_NOT_INSTALLED", err.Error())
	case errors.Is(err, wukongplugin.ErrUntrustedSigner), errors.Is(err, wukongplugin.ErrPluginNotAllowed), errors.Is(err, wukongplugin.ErrAIPluginForbidden):
		writeError(w, http.StatusForbidden, "PLUGIN_FORBIDDEN", err.Error())
	case errors.Is(err, wukongplugin.ErrInvalidManifest), errors.Is(err, wukongplugin.ErrInvalidSignature), errors.Is(err, wukongplugin.ErrBundleMismatch), strings.Contains(err.Error(), "confirmation"):
		writeError(w, http.StatusBadRequest, "INVALID_PLUGIN_BUNDLE", err.Error())
	case errors.Is(err, wukongplugin.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, "PLUGIN_LIFECYCLE_UNAVAILABLE", err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "PLUGIN_LIFECYCLE_FAILED", "plugin lifecycle operation failed")
	}
}

func valueString(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func valueStrings(value any) []string {
	values, ok := value.([]any)
	if !ok {
		if typed, typedOK := value.([]string); typedOK {
			return append([]string(nil), typed...)
		}
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func normalPluginStatus(value any) bool {
	return pluginStatusString(value) == "normal"
}

func pluginStatusString(value any) string {
	switch typed := value.(type) {
	case string:
		status := strings.TrimSpace(typed)
		if status == "init" || status == "normal" || status == "error" || status == "offline" || status == "disabled" {
			return status
		}
		return pluginStatusNumber(status)
	case float64:
		return pluginStatusNumber(strconv.FormatInt(int64(typed), 10))
	case json.Number:
		return pluginStatusNumber(typed.String())
	case int:
		return pluginStatusNumber(strconv.Itoa(typed))
	default:
		return ""
	}
}

func pluginStatusNumber(status string) string {
	switch status {
	case "0":
		return "init"
	case "1":
		return "normal"
	case "2":
		return "error"
	case "3":
		return "offline"
	case "4":
		return "disabled"
	default:
		return status
	}
}
