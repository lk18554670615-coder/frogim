package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/linli/im/server/internal/app"
)

func TestGroupJoinPolicyErrorsAreActionable(t *testing.T) {
	tests := []struct {
		err    error
		status int
		code   string
	}{
		{app.ErrFriendRequired, http.StatusForbidden, "FRIENDSHIP_REQUIRED"},
		{app.ErrJoinPolicy, http.StatusForbidden, "GROUP_JOIN_POLICY_RESTRICTED"},
		{app.ErrJoinRequestExpired, http.StatusConflict, "GROUP_JOIN_REQUEST_EXPIRED"},
	}
	for _, test := range tests {
		recorder := httptest.NewRecorder()
		handleErr(recorder, test.err)
		if recorder.Code != test.status {
			t.Fatalf("%s status=%d", test.code, recorder.Code)
		}
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Error.Code != test.code {
			t.Fatalf("error code=%q want=%q", body.Error.Code, test.code)
		}
	}
}
