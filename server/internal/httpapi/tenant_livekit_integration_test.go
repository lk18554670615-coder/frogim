package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	livekitcontrol "github.com/linli/im/server/internal/livekit"
	"github.com/linli/im/server/internal/store"
	"github.com/linli/im/server/internal/tenancy"
	lkproto "github.com/livekit/protocol/livekit"
	"golang.org/x/net/websocket"
	"google.golang.org/protobuf/proto"
)

type tenantStackMedia struct {
	token, room, user, observer string
	disconnected                <-chan struct{}
	observerDisconnected        <-chan struct{}
}

type tenantStackMediaFault struct {
	*livekitcontrol.Control
	fail atomic.Bool
}

func (c *tenantStackMediaFault) RemoveParticipant(ctx context.Context, room, user string) error {
	if c.fail.Load() {
		return errors.New("isolated removal control fault")
	}
	return c.Control.RemoveParticipant(ctx, room, user)
}

func tenancyStackSocket(t *testing.T, tlsConfig *tls.Config, base, token string) <-chan struct{} {
	t.Helper()
	address := strings.Replace(base, "https://", "wss://", 1) + "/livekit/rtc?protocol=15&auto_subscribe=0&access_token=" + url.QueryEscape(token)
	wsConfig, err := websocket.NewConfig(address, "https://app.example")
	if err != nil {
		t.Fatal("invalid fixture signal address")
	}
	wsConfig.TlsConfig = tlsConfig
	dialCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	conn, err := wsConfig.DialContext(dialCtx)
	if err != nil {
		t.Fatal("real LiveKit signal handshake failed")
	} // Never print token-bearing URL errors.
	t.Cleanup(func() { _ = conn.Close() })
	if err = conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var data []byte
	err = websocket.Message.Receive(conn, &data)
	if err != nil {
		t.Fatal("LiveKit join response not received")
	}
	var message lkproto.SignalResponse
	if err = proto.Unmarshal(data, &message); err != nil || message.GetJoin() == nil {
		t.Fatal("not a real LiveKit JoinResponse")
	}
	_ = conn.SetReadDeadline(time.Time{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var data []byte
			if e := websocket.Message.Receive(conn, &data); e != nil {
				return
			}
		}
	}()
	return done
}

// Fixtures create accepted business calls, but all signaling/token issuance,
// room membership, kicking and reconnection checks use real HTTP/WSS/LiveKit.
func tenancyStackMediaJoin(t *testing.T, client *http.Client, tlsConfig *tls.Config, base string, x *API, db *store.Postgres, conn *pgx.Conn, user, businessToken, suffix string) tenantStackMedia {
	t.Helper()
	ctx := t.Context()
	control, ok := x.livekit.(*livekitcontrol.Control)
	if fault, wrapped := x.livekit.(*tenantStackMediaFault); wrapped && !fault.fail.Load() {
		control, ok = fault.Control, true
	}
	if !ok {
		t.Fatal("real LiveKit control not configured")
	}
	partner := tenancy.Identity{TenantID: x.cfg.TenantID, AccountID: "media-observer-" + suffix, LocalUserID: "media-observer-" + suffix, AssignmentVersion: 1}
	if err := db.PrepareTenantIdentity(ctx, store.TenantProvision{OperationID: "media-fixture-" + suffix, Identity: partner, Phone: "media-fixture-" + suffix, Name: "isolated media observer", Method: "transfer"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ActivateTenantIdentity(ctx, partner); err != nil {
		t.Fatal(err)
	}
	cid := "media-conv-" + suffix
	if _, err := conn.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',now(),now())`, cid); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',now()),($1,$3,'member',now())`, cid, user, partner.LocalUserID); err != nil {
		t.Fatal(err)
	}
	callID := fmt.Sprintf("media-%s-%d", suffix, time.Now().UnixNano())
	now := time.Now()
	if _, _, err := db.InviteCall(ctx, store.CallInvite{ID: callID, ConversationID: cid, CallerID: user, CalleeID: partner.LocalUserID, MediaType: "audio", InvitedAt: now, ExpiresAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.TransitionCall(ctx, callID, partner.LocalUserID, "accept", "", now); err != nil {
		t.Fatal(err)
	}
	status, response := tenancyStackRequest(t, client, "POST", base+"/v2/calls/"+callID+"/token", businessToken, map[string]any{})
	if status != 200 {
		t.Fatal("scoped LiveKit issuance failed", status, response["error"])
	}
	session := response["session"].(map[string]any)
	token := session["token"].(string)
	done := tenancyStackSocket(t, tlsConfig, base, token)
	realm, err := db.TenantRealmVersion(ctx, partner.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	observerToken, err := control.IssueTenantParticipant(callID, cid, "audio", partner, 1, realm)
	if err != nil {
		t.Fatal(err)
	}
	observerDone := tenancyStackSocket(t, tlsConfig, base, observerToken.Token)
	room := session["roomName"].(string)
	participants, err := control.ListParticipants(ctx, room)
	if err != nil || len(participants) != 2 {
		t.Fatal("real LiveKit did not register both participants", err)
	}
	if err = db.CheckTenantMediaSettled(ctx, user); err != nil {
		t.Fatal("confirmed handshake was not persisted", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = control.DeleteRoom(cleanup, room)
	})
	return tenantStackMedia{token: token, room: room, user: user, observer: partner.LocalUserID, disconnected: done, observerDisconnected: observerDone}
}

func (m tenantStackMedia) assertRevoked(t *testing.T, client *http.Client, base string, x *API) {
	t.Helper()
	select {
	case <-m.disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("revocation acknowledged with live signal connection")
	}
	select {
	case <-m.observerDisconnected:
		t.Fatal("unrelated participant disconnected")
	default:
	}
	participants, err := x.livekit.(livekitAdminControl).ListParticipants(t.Context(), m.room)
	if err != nil {
		t.Fatal(err)
	}
	if len(participants) != 1 || participants[0].Identity != m.observer {
		t.Fatal("revocation did not remove only the target")
	}
	status, _ := tenancyStackRequest(t, client, "GET", base+"/livekit/rtc/validate?access_token="+url.QueryEscape(m.token), "", nil)
	if status != 401 {
		t.Fatal("old media token can rejoin after revocation", status)
	}
}
