package wukong

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"
)

// CredentialProvisionStore records which deterministic WuKongIM credential
// has already been installed. WuKongIM v2.2.5 kicks every master connection
// whenever POST /user/token is called, even when the token is unchanged, so
// login and refresh must not blindly repeat that write.
type CredentialProvisionStore interface {
	WukongCredentialProvisioned(context.Context, string, int, int, string) (bool, error)
	MarkWukongCredentialProvisioned(context.Context, string, int, int, string) error
	InvalidateWukongCredential(context.Context, string, int) error
}

type ImSession struct {
	UID         string    `json:"uid"`
	Token       string    `json:"token"`
	DeviceFlag  int       `json:"deviceFlag"`
	DeviceLevel int       `json:"deviceLevel"`
	TCPURL      string    `json:"tcpUrl,omitempty"`
	WSURL       string    `json:"wsUrl,omitempty"`
	SDK         string    `json:"sdk"`
	IssuedAt    time.Time `json:"issuedAt"`
}

type SessionIssuer struct {
	client *Client
	secret []byte
	tcpURL string
	wsURL  string
	now    func() time.Time
	store  CredentialProvisionStore
	locks  [64]sync.Mutex
}

func NewSessionIssuer(client *Client, secret, tcpURL, wsURL string, stores ...CredentialProvisionStore) (*SessionIssuer, error) {
	if client == nil || len(secret) < 32 || strings.TrimSpace(tcpURL) == "" || strings.TrimSpace(wsURL) == "" {
		return nil, errors.New("WuKongIM session issuer requires a client, 32-byte secret, TCP URL and WebSocket URL")
	}
	issuer := &SessionIssuer{client: client, secret: []byte(secret), tcpURL: tcpURL, wsURL: wsURL, now: time.Now}
	if len(stores) > 0 {
		issuer.store = stores[0]
	}
	return issuer, nil
}

func (s *SessionIssuer) Issue(ctx context.Context, uid, platform string) (*ImSession, error) {
	deviceFlag, sdk, err := platformDevice(platform)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte("wukongim-token-v1\x00" + uid + "\x00" + strconv.Itoa(deviceFlag)))
	token := "wk1_" + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if err = s.ensureProvisioned(ctx, uid, token, deviceFlag, DeviceLevelMaster); err != nil {
		return nil, err
	}
	return &ImSession{UID: uid, Token: token, DeviceFlag: deviceFlag, DeviceLevel: DeviceLevelMaster, TCPURL: s.tcpURL, WSURL: s.wsURL, SDK: sdk, IssuedAt: s.now().UTC()}, nil
}

func (s *SessionIssuer) ensureProvisioned(ctx context.Context, uid, token string, deviceFlag, deviceLevel int) error {
	digestBytes := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(digestBytes[:])
	lock := &s.locks[int(digestBytes[0])%len(s.locks)]
	lock.Lock()
	defer lock.Unlock()
	if s.store != nil {
		provisioned, err := s.store.WukongCredentialProvisioned(ctx, uid, deviceFlag, deviceLevel, digest)
		if err != nil {
			return err
		}
		if provisioned {
			return nil
		}
	}
	if err := s.client.ProvisionUser(ctx, UserTokenRequest{UID: uid, Token: token, DeviceFlag: deviceFlag, DeviceLevel: deviceLevel}); err != nil {
		return err
	}
	if s.store != nil {
		return s.store.MarkWukongCredentialProvisioned(ctx, uid, deviceFlag, deviceLevel, digest)
	}
	return nil
}

func platformDevice(platform string) (int, string, error) {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "android", "ios":
		return DeviceApp, "wukongimfluttersdk", nil
	case "web":
		return DeviceWeb, "wukongimjssdk", nil
	case "macos":
		return DeviceDesktop, "wukong_easy_sdk", nil
	default:
		return 0, "", errors.New("platform must be android, ios, web or macos")
	}
}
