package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
