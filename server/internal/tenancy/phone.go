package tenancy

import (
	"bytes"
	"encoding/json"
	"net/http"
)

func (p *Platform) phoneCode(w http.ResponseWriter, r *http.Request) {
	u, e := p.authenticated(r, bearer(r), "a")
	if e != nil || u.Banned || u.Pending != "" {
		fail(w, 401, "UNAUTHENTICATED")
		return
	}
	var b struct {
		Phone string `json:"phone"`
	}
	if !decode(w, r, &b) {
		return
	}
	data, _ := json.Marshal(map[string]string{"phone": b.Phone, "purpose": "phone"})
	r.Body = httpBody(data)
	p.code(w, r)
}
func httpBody(b []byte) *bodyReader { return &bodyReader{bytes.NewReader(b)} }

type bodyReader struct{ *bytes.Reader }

func (*bodyReader) Close() error { return nil }
func (p *Platform) changePhone(w http.ResponseWriter, r *http.Request) {
	u, e := p.authenticated(r, bearer(r), "a")
	if e != nil || u.Banned || u.Pending != "" {
		fail(w, 401, "UNAUTHENTICATED")
		return
	}
	var b struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Phone == u.Phone || !p.verifyOTP(r.Context(), b.Phone, b.Code, "phone") {
		fail(w, 400, "INVALID_ARGUMENT")
		return
	}
	o, e := p.startOperation(r.Context(), u.ID, u.TenantID, "phone", "user:"+u.ID, "本人验证码修改手机号", u.Revision, OperationValues{Phone: b.Phone})
	if e != nil {
		fail(w, 409, "PHONE_CHANGE_REJECTED")
		return
	}
	if e = p.runOperation(r.Context(), o); e != nil {
		_, _ = p.DB.Exec(r.Context(), `UPDATE lp_operations SET error='phone change incomplete' WHERE id=$1`, o.ID)
		fail(w, 503, "PHONE_CHANGE_INCOMPLETE")
		return
	}
	u, e = p.user(r.Context(), u.ID)
	if e != nil {
		fail(w, 503, "DATABASE_UNAVAILABLE")
		return
	}
	raw, _ := json.Marshal(u.Profile)
	profile := map[string]any{}
	_ = json.Unmarshal(raw, &profile)
	profile["id"] = u.ID
	profile["phone"] = u.Phone
	jsonResponse(w, 200, profile)
}
