package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/linli/im/server/internal/app"
)

func TestWriteInviteErrorUsesStableClientCodes(t *testing.T) {
	for _, test := range []struct {
		err    error
		status int
		code   string
	}{
		{app.ErrInviteRequired, http.StatusBadRequest, "INVITE_CODE_REQUIRED"},
		{app.ErrInviteRelationCycle, http.StatusConflict, "INVITE_RELATION_CYCLE"},
		{app.ErrInviteRelationStale, http.StatusConflict, "INVITE_RELATION_CHANGED"},
		{app.ErrInviteInvalid, http.StatusBadRequest, "INVITE_CODE_INVALID"},
		{app.ErrInviteDisabled, http.StatusConflict, "INVITE_CODE_STATUS_DISABLED"},
		{app.ErrInviteChangeUsed, http.StatusConflict, "INVITE_CODE_CHANGE_USED"},
		{app.ErrConflict, http.StatusConflict, "INVITE_CODE_DUPLICATE"},
	} {
		recorder := httptest.NewRecorder()
		writeInviteError(recorder, test.err)
		if recorder.Code != test.status {
			t.Fatalf("%v status=%d want=%d", test.err, recorder.Code, test.status)
		}
		var payload struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Error.Code != test.code {
			t.Fatalf("%v code=%q want=%q", test.err, payload.Error.Code, test.code)
		}
	}
}

func TestAdminInviteRelationRequiresVersionAndConfirmation(t *testing.T) {
	x := &API{}
	for _, body := range []string{
		`{}`,
		`{"inviteCode":"ABCDEF88","reason":"test","confirmed":false,"expectedVersion":0}`,
		`{"inviteCode":"ABCDEF88","reason":"","confirmed":true,"expectedVersion":0}`,
		`{"inviteCode":"ABCDEF88","reason":"test","confirmed":true}`,
		`{"inviteCode":"ABCDEF88","reason":"test","confirmed":true,"expectedVersion":null}`,
		`{"inviteCode":"ABCDEF88","reason":"test","confirmed":true,"expectedVersion":-1}`,
	} {
		r := httptest.NewRequest(http.MethodPut, "/v2/admin/users/u/invite-relation", strings.NewReader(body))
		w := httptest.NewRecorder()
		x.adminSetUserInviteRelation(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d", body, w.Code)
		}
	}
}
