package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	wkclient "github.com/WuKongIM/WuKongIM/pkg/client"
	"github.com/WuKongIM/WuKongIM/pkg/wkutil"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type options struct {
	apiURL        string
	managerURL    string
	tcpURL        string
	managerToken  string
	businessURL   string
	otpCode       string
	timeout       time.Duration
	redactionOnly bool
}

type report struct {
	ServerCommit  string        `json:"serverCommit"`
	Handshake     bool          `json:"handshake"`
	SendACK       bool          `json:"sendAck"`
	Receive       bool          `json:"receive"`
	MessageID     int64         `json:"messageId"`
	MessageSeq    uint32        `json:"messageSeq"`
	Latency       time.Duration `json:"latency"`
	BusinessAuth  bool          `json:"businessAuth"`
	ManagerAPI    bool          `json:"managerApi"`
	SessionSDKs   []string      `json:"sessionSdks,omitempty"`
	OfflineSync   bool          `json:"offlineSync"`
	HistorySync   bool          `json:"historySync"`
	GroupSync     bool          `json:"groupSync"`
	CallCommand   bool          `json:"callCommand"`
	LiveKitRoom   bool          `json:"liveKitRoom"`
	LiveKitToken  bool          `json:"liveKitParticipantTokens"`
	LiveKitClean  bool          `json:"liveKitRoomCleanup"`
	BusinessCMD   bool          `json:"businessCommand"`
	TypingCMD     bool          `json:"typingCommand"`
	ServerStored  bool          `json:"serverStoredMessage"`
	Scheduled     bool          `json:"scheduledStoredMessage"`
	Forwarded     bool          `json:"forwardedStoredMessage"`
	SystemStored  bool          `json:"systemStoredMessage"`
	ReactionSync  bool          `json:"reactionExtensionSync"`
	EditSync      bool          `json:"editExtensionSync"`
	PinSync       bool          `json:"pinExtensionSync"`
	FavoriteSync  bool          `json:"favoriteHydrationSync"`
	SearchSync    bool          `json:"searchFromWukongSync"`
	HistoryRoute  bool          `json:"historyRouteFromWukong"`
	ChannelInfo   bool          `json:"channelInfoDatasource"`
	MemberSync    bool          `json:"channelMemberDatasource"`
	MessageExtra  bool          `json:"messageExtraDatasource"`
	ReminderSync  bool          `json:"reminderDatasource"`
	ReadSync      bool          `json:"readStateSync"`
	RecallSync    bool          `json:"recallExtensionSync"`
	PolicyPlugin  bool          `json:"policyPluginRegistered"`
	PolicyAllow   bool          `json:"policyAllowedMember"`
	PolicyDeny    bool          `json:"policyDeniedOutsider"`
	MultiDevice   bool          `json:"multiDeviceSync"`
	DeviceQuit    bool          `json:"targetedDeviceQuit"`
	DuplicateKick bool          `json:"duplicateMasterDeviceKick"`
}

type businessSession struct {
	AccessToken string `json:"accessToken"`
	IMSession   struct {
		UID    string `json:"uid"`
		Token  string `json:"token"`
		TCPURL string `json:"tcpUrl"`
		SDK    string `json:"sdk"`
	} `json:"imSession"`
}

type friendRequestResponse struct {
	ID string `json:"id"`
}

type groupResponse struct {
	ID string `json:"id"`
}

func main() {
	var cfg options
	flag.StringVar(&cfg.apiURL, "api", "http://127.0.0.1:5001", "WuKongIM internal HTTP API")
	flag.StringVar(&cfg.managerURL, "manager-api", "", "WuKongIM Manager API; defaults to -api for local compatibility")
	flag.StringVar(&cfg.tcpURL, "tcp", "tcp://127.0.0.1:5100", "WuKongIM TCP endpoint")
	flag.StringVar(&cfg.managerToken, "manager-token", os.Getenv("IM_WUKONG_MANAGER_TOKEN"), "WuKongIM manager token")
	flag.StringVar(&cfg.businessURL, "business-api", "", "optional business API used to verify /v2 auth and ImSession")
	flag.StringVar(&cfg.otpCode, "otp", "123456", "development OTP used with -business-api")
	flag.DurationVar(&cfg.timeout, "timeout", 10*time.Second, "probe timeout")
	flag.BoolVar(&cfg.redactionOnly, "redaction-only", false, "only exercise runtime sensitive-log redaction paths")
	flag.Parse()
	if err := run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "probe failed:", err)
		os.Exit(1)
	}
}

func run(cfg options) error {
	if strings.TrimSpace(cfg.managerToken) == "" {
		return errors.New("manager token is required")
	}
	if strings.TrimSpace(cfg.managerURL) == "" {
		cfg.managerURL = cfg.apiURL
	}
	ctx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
	defer cancel()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	aliceUID, bobUID := "probe_alice_"+suffix, "probe_bob_"+suffix
	aliceToken, bobToken := "token_a_"+suffix, "token_b_"+suffix
	channelID, channelType := "probe_group_"+suffix, uint8(2)
	result := report{ServerCommit: "a888f89533d0e7d1b2030e06504ca97f1ad891d4"}
	var nodes struct {
		Total int              `json:"total"`
		Data  []map[string]any `json:"data"`
	}
	if err := getManagerJSON(ctx, cfg, "/cluster/nodes", &nodes); err != nil {
		return fmt.Errorf("manager cluster nodes: %w", err)
	}
	result.ManagerAPI = nodes.Total > 0 && len(nodes.Data) > 0
	if !result.ManagerAPI {
		return fmt.Errorf("manager cluster nodes returned no active node: %#v", nodes)
	}
	if cfg.redactionOnly {
		duplicateKick, err := runRuntimeRedactionProbe(ctx, cfg, suffix)
		if err != nil {
			return err
		}
		result.DuplicateKick = duplicateKick
		encoded, _ := json.MarshalIndent(result, "", "  ")
		fmt.Println(string(encoded))
		return nil
	}
	var aliceSession, aliceWebSession, aliceMacSession, bobSession, outsiderSession businessSession
	var businessGroupID, businessDirectID string
	if strings.TrimSpace(cfg.businessURL) != "" {
		phoneSuffix := suffix
		if len(phoneSuffix) > 8 {
			phoneSuffix = phoneSuffix[len(phoneSuffix)-8:]
		}
		var err error
		aliceSession, err = loginBusiness(ctx, cfg, "139"+phoneSuffix, "Android probe", "android")
		if err != nil {
			return fmt.Errorf("Alice business login: %w", err)
		}
		aliceWebSession, err = loginBusiness(ctx, cfg, "139"+phoneSuffix, "Web probe", "web")
		if err != nil {
			return fmt.Errorf("Alice Web business login: %w", err)
		}
		aliceMacSession, err = loginBusiness(ctx, cfg, "139"+phoneSuffix, "macOS probe", "macos")
		if err != nil {
			return fmt.Errorf("Alice macOS business login: %w", err)
		}
		if aliceWebSession.IMSession.UID != aliceSession.IMSession.UID || aliceMacSession.IMSession.UID != aliceSession.IMSession.UID {
			return errors.New("same business account returned different WuKong UIDs across platforms")
		}
		bobSession, err = loginBusiness(ctx, cfg, "138"+phoneSuffix, "iOS probe", "ios")
		if err != nil {
			return fmt.Errorf("Bob business login: %w", err)
		}
		outsiderSession, err = loginBusiness(ctx, cfg, "137"+phoneSuffix, "Outsider probe", "android")
		if err != nil {
			return fmt.Errorf("outsider business login: %w", err)
		}
		aliceUID, aliceToken = aliceSession.IMSession.UID, aliceSession.IMSession.Token
		bobUID, bobToken = bobSession.IMSession.UID, bobSession.IMSession.Token
		var friendRequest friendRequestResponse
		if err := postBusinessAuthorized(ctx, cfg, "/v2/contacts/requests", aliceSession.AccessToken, map[string]string{"userId": bobUID, "message": "protocol probe"}, &friendRequest); err != nil {
			return fmt.Errorf("friend request: %w", err)
		}
		if friendRequest.ID == "" {
			return errors.New("friend request did not return an id")
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/contacts/requests/"+friendRequest.ID+"/accept", bobSession.AccessToken, map[string]string{}, nil); err != nil {
			return fmt.Errorf("friend accept: %w", err)
		}
		var group groupResponse
		if err := postBusinessAuthorized(ctx, cfg, "/v2/channels/groups", aliceSession.AccessToken, map[string]any{"name": "Protocol probe", "memberIds": []string{bobUID}}, &group); err != nil {
			return fmt.Errorf("group create: %w", err)
		}
		if group.ID == "" {
			return errors.New("group create did not return an id")
		}
		businessGroupID = group.ID
		var direct groupResponse
		if err := postBusinessAuthorized(ctx, cfg, "/v2/channels/direct", aliceSession.AccessToken, map[string]string{"userId": bobUID}, &direct); err != nil {
			return fmt.Errorf("direct conversation: %w", err)
		}
		if direct.ID == "" {
			return errors.New("direct conversation did not return an id")
		}
		businessDirectID = direct.ID
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
		var channelInfo struct {
			Item map[string]any `json:"item"`
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/channel", aliceSession.AccessToken, map[string]any{
			"channelId": businessGroupID, "channelType": 2,
		}, &channelInfo); err != nil {
			return fmt.Errorf("channel datasource: %w", err)
		}
		result.ChannelInfo = channelInfo.Item["channel_id"] == businessGroupID && channelInfo.Item["channel_name"] == "Protocol probe"
		var members struct {
			Items []map[string]any `json:"items"`
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/members", aliceSession.AccessToken, map[string]any{
			"channelId": businessGroupID, "channelType": 2, "version": 0, "limit": 100,
		}, &members); err != nil {
			return fmt.Errorf("channel member datasource: %w", err)
		}
		memberIDs := map[string]bool{}
		for _, member := range members.Items {
			memberIDs[fmt.Sprint(member["member_uid"])] = member["version"] != nil
		}
		result.MemberSync = memberIDs[aliceUID] && memberIDs[bobUID]
		if !result.ChannelInfo || !result.MemberSync {
			return fmt.Errorf("channel datasource incomplete: info=%v members=%v", channelInfo.Item, members.Items)
		}
		if !strings.HasPrefix(aliceSession.IMSession.TCPURL, "tcp://") && !strings.HasPrefix(aliceSession.IMSession.TCPURL, "tls://") {
			return fmt.Errorf("business ImSession returned an invalid TCP endpoint: %q", aliceSession.IMSession.TCPURL)
		}
		channelID, channelType = bobUID, 1
		result.BusinessAuth = true
		result.SessionSDKs = []string{
			aliceSession.IMSession.SDK,
			bobSession.IMSession.SDK,
			aliceWebSession.IMSession.SDK,
			aliceMacSession.IMSession.SDK,
		}
		var plugins []struct {
			No      string   `json:"no"`
			Status  int      `json:"status"`
			Version string   `json:"version"`
			Methods []string `json:"methods"`
		}
		if err := getManagerJSON(ctx, cfg, "/plugins", &plugins); err != nil {
			return fmt.Errorf("policy plugin status: %w", err)
		}
		for _, plugin := range plugins {
			result.PolicyPlugin = result.PolicyPlugin || plugin.No == "wk.plugin.im-policy" && plugin.Status == 1 && plugin.Version == "1.0.0" && len(plugin.Methods) == 1 && plugin.Methods[0] == "Send"
		}
		if !result.PolicyPlugin {
			return fmt.Errorf("policy plugin is not registered: %#v", plugins)
		}
	} else {
		for _, user := range []map[string]any{
			{"uid": aliceUID, "token": aliceToken, "device_flag": 0, "device_level": 1},
			{"uid": bobUID, "token": bobToken, "device_flag": 0, "device_level": 1},
		} {
			if err := postJSON(ctx, cfg, "/user/token", user); err != nil {
				return err
			}
		}
		if err := postJSON(ctx, cfg, "/channel", map[string]any{
			"channel_id": channelID, "channel_type": channelType, "reset": 1,
			"subscribers": []string{aliceUID, bobUID},
		}); err != nil {
			return err
		}
	}

	alice := wkclient.New(cfg.tcpURL, wkclient.WithUID(aliceUID), wkclient.WithToken(aliceToken))
	bob := wkclient.New(cfg.tcpURL, wkclient.WithUID(bobUID), wkclient.WithToken(bobToken))
	defer alice.Close()
	defer bob.Close()

	recv := make(chan *wkproto.RecvPacket, 2)
	aliceRecv := make(chan *wkproto.RecvPacket, 2)
	ack := make(chan *wkproto.SendackPacket, 2)
	bobAck := make(chan *wkproto.SendackPacket, 2)
	bob.SetOnRecv(func(packet *wkproto.RecvPacket) error {
		select {
		case recv <- packet:
		default:
		}
		return nil
	})
	alice.SetOnRecv(func(packet *wkproto.RecvPacket) error {
		select {
		case aliceRecv <- packet:
		default:
		}
		return nil
	})
	alice.SetOnSendack(func(packet *wkproto.SendackPacket) { ack <- packet })
	bob.SetOnSendack(func(packet *wkproto.SendackPacket) { bobAck <- packet })

	if err := bob.Connect(); err != nil {
		return fmt.Errorf("Bob TCP handshake: %w", err)
	}
	if err := alice.Connect(); err != nil {
		return fmt.Errorf("Alice TCP handshake: %w", err)
	}
	started := time.Now()
	clientMsgNo := "probe_msg_" + suffix
	payload := []byte(`{"type":1,"content":"WK_LOG_REDACTION_MESSAGE_MARKER"}`)
	if err := alice.SendMessage(wkclient.NewChannel(channelID, channelType), payload, wkclient.SendOptionWithClientMsgNo(clientMsgNo)); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	result.Handshake = true
	select {
	case packet := <-ack:
		if packet.ReasonCode != wkproto.ReasonSuccess || packet.MessageID == 0 || packet.MessageSeq == 0 {
			return fmt.Errorf("invalid send ACK: reason=%d message_id=%d message_seq=%d", packet.ReasonCode, packet.MessageID, packet.MessageSeq)
		}
		result.SendACK, result.MessageID, result.MessageSeq = true, packet.MessageID, packet.MessageSeq
		result.PolicyAllow = result.BusinessAuth
	case <-ctx.Done():
		return errors.New("timed out waiting for send ACK")
	}
	select {
	case packet := <-recv:
		if packet.MessageID != result.MessageID || packet.MessageSeq != result.MessageSeq {
			return fmt.Errorf("received message does not match ACK: id=%d seq=%d", packet.MessageID, packet.MessageSeq)
		}
		result.Receive, result.Latency = true, time.Since(started)
	case <-ctx.Done():
		return errors.New("timed out waiting for peer receive")
	}
	if result.BusinessAuth {
		groupClientNo := "probe_group_msg_" + suffix
		groupPayload, _ := json.Marshal(map[string]any{
			"type": 1, "content": "group provision probe",
			"mention": map[string]any{"uids": []string{bobUID}},
		})
		if err := alice.SendMessage(wkclient.NewChannel(businessGroupID, 2), groupPayload, wkclient.SendOptionWithClientMsgNo(groupClientNo)); err != nil {
			return fmt.Errorf("group send: %w", err)
		}
		var groupMessageID int64
		select {
		case packet := <-ack:
			if packet.ReasonCode != wkproto.ReasonSuccess || packet.MessageID == 0 || packet.MessageSeq == 0 {
				return fmt.Errorf("invalid group send ACK: reason=%d message_id=%d message_seq=%d", packet.ReasonCode, packet.MessageID, packet.MessageSeq)
			}
			groupMessageID = packet.MessageID
		case <-ctx.Done():
			return errors.New("timed out waiting for group send ACK")
		}
		reminderUpdatedCommand := false
		groupDeadline := time.NewTimer(5 * time.Second)
		for !result.GroupSync || !reminderUpdatedCommand {
			select {
			case packet := <-recv:
				if packet.MessageID == groupMessageID && packet.ChannelType == 2 {
					result.GroupSync = true
					continue
				}
				var command struct {
					Type int    `json:"type"`
					CMD  string `json:"cmd"`
				}
				reminderUpdatedCommand = reminderUpdatedCommand ||
					json.Unmarshal(packet.Payload, &command) == nil && command.Type == 99 && command.CMD == "reminder.updated"
			case <-groupDeadline.C:
				return fmt.Errorf("group/reminder CMD timeout: group=%v reminder=%v", result.GroupSync, reminderUpdatedCommand)
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if !groupDeadline.Stop() {
			select {
			case <-groupDeadline.C:
			default:
			}
		}
		outsider := wkclient.New(cfg.tcpURL, wkclient.WithUID(outsiderSession.IMSession.UID), wkclient.WithToken(outsiderSession.IMSession.Token))
		outsiderACK := make(chan *wkproto.SendackPacket, 1)
		outsider.SetOnSendack(func(packet *wkproto.SendackPacket) { outsiderACK <- packet })
		if err := outsider.Connect(); err != nil {
			return fmt.Errorf("outsider TCP handshake: %w", err)
		}
		if err := outsider.SendMessage(wkclient.NewChannel(businessGroupID, 2), []byte(`{"type":1,"content":"policy must reject outsider"}`), wkclient.SendOptionWithClientMsgNo("probe_policy_deny_"+suffix)); err != nil {
			outsider.Close()
			return fmt.Errorf("outsider send: %w", err)
		}
		select {
		case packet := <-outsiderACK:
			result.PolicyDeny = packet.ReasonCode == wkproto.ReasonNotAllowSend && packet.MessageSeq == 0
			if !result.PolicyDeny {
				outsider.Close()
				return fmt.Errorf("policy returned unexpected outsider ACK: reason=%d message_id=%d message_seq=%d", packet.ReasonCode, packet.MessageID, packet.MessageSeq)
			}
		case <-ctx.Done():
			outsider.Close()
			return errors.New("timed out waiting for policy denial ACK")
		}
		outsider.Close()
		var reminderOutput struct {
			Items []struct {
				ID        int64  `json:"reminder_id"`
				MessageID string `json:"message_id"`
				Version   int64  `json:"version"`
				Done      int    `json:"done"`
				Publisher string `json:"publisher"`
			} `json:"items"`
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/reminders", bobSession.AccessToken,
			map[string]any{"version": 0, "limit": 500}, &reminderOutput); err != nil {
			return fmt.Errorf("sync WuKong reminder: %w", err)
		}
		var reminderID, reminderVersion int64
		for _, item := range reminderOutput.Items {
			if item.MessageID == strconv.FormatInt(groupMessageID, 10) && item.Done == 0 && item.Publisher == aliceUID {
				reminderID, reminderVersion = item.ID, item.Version
			}
		}
		if reminderID == 0 || reminderVersion == 0 {
			return fmt.Errorf("mention reminder missing: %#v", reminderOutput.Items)
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/reminders/done", bobSession.AccessToken,
			map[string]any{"reminderIds": []int64{reminderID}}, nil); err != nil {
			return fmt.Errorf("complete WuKong reminder: %w", err)
		}
		reminderDoneCommand := false
		doneDeadline := time.NewTimer(5 * time.Second)
		for !reminderDoneCommand {
			select {
			case packet := <-recv:
				var command struct {
					Type int    `json:"type"`
					CMD  string `json:"cmd"`
				}
				reminderDoneCommand = json.Unmarshal(packet.Payload, &command) == nil && command.Type == 99 && command.CMD == "reminder.done"
			case <-doneDeadline.C:
				return errors.New("timed out waiting for reminder.done CMD")
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if !doneDeadline.Stop() {
			select {
			case <-doneDeadline.C:
			default:
			}
		}
		reminderOutput.Items = nil
		if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/reminders", bobSession.AccessToken,
			map[string]any{"version": reminderVersion, "limit": 500}, &reminderOutput); err != nil {
			return fmt.Errorf("sync completed WuKong reminder: %w", err)
		}
		for _, item := range reminderOutput.Items {
			if item.ID == reminderID && item.Done == 1 && item.Version > reminderVersion {
				result.ReminderSync = true
			}
		}
		if !result.ReminderSync {
			return fmt.Errorf("completed reminder delta missing: %#v", reminderOutput.Items)
		}
		pinDeadline := time.Now().Add(5 * time.Second)
		for {
			err := putBusinessAuthorized(ctx, cfg, "/v2/messages/pins/"+fmt.Sprintf("%d", groupMessageID)+"?conversationId="+url.QueryEscape(businessGroupID), aliceSession.AccessToken, nil, nil)
			if err == nil {
				break
			}
			if time.Now().After(pinDeadline) {
				return fmt.Errorf("pin WuKong group message: %w", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		var pins struct {
			Items []struct {
				PinnedBy string `json:"pinnedBy"`
				Message  struct {
					ID   string         `json:"id"`
					Body map[string]any `json:"body"`
				} `json:"message"`
			} `json:"items"`
		}
		if err := getBusinessAuthorized(ctx, cfg, "/v2/messages/pins?conversationId="+url.QueryEscape(businessGroupID)+"&limit=50", bobSession.AccessToken, &pins); err != nil {
			return fmt.Errorf("list WuKong group pins: %w", err)
		}
		for _, item := range pins.Items {
			if item.Message.ID == fmt.Sprintf("%d", groupMessageID) && item.Message.Body["text"] == "group provision probe" && item.PinnedBy == aliceUID {
				result.PinSync = true
			}
		}
		if !result.PinSync {
			return fmt.Errorf("WuKong pinned message was not hydrated: %#v", pins.Items)
		}
		if err := putBusinessAuthorized(ctx, cfg, "/v2/messages/favorites/"+fmt.Sprintf("%d", groupMessageID), bobSession.AccessToken, nil, nil); err != nil {
			return fmt.Errorf("favorite WuKong group message: %w", err)
		}
		var favorites struct {
			Items []struct {
				ID   string         `json:"id"`
				Body map[string]any `json:"body"`
			} `json:"items"`
		}
		if err := getBusinessAuthorized(ctx, cfg, "/v2/messages/favorites?limit=50", bobSession.AccessToken, &favorites); err != nil {
			return fmt.Errorf("list WuKong favorites: %w", err)
		}
		for _, item := range favorites.Items {
			if item.ID == fmt.Sprintf("%d", groupMessageID) && item.Body["text"] == "group provision probe" {
				result.FavoriteSync = true
			}
		}
		if !result.FavoriteSync {
			return fmt.Errorf("WuKong favorite was not hydrated: %#v", favorites.Items)
		}
		if err := patchBusinessAuthorized(ctx, cfg, "/v2/channels/groups/"+businessGroupID, aliceSession.AccessToken, map[string]any{"name": "Protocol probe renamed"}, nil); err != nil {
			return fmt.Errorf("group profile system message: %w", err)
		}
		for !result.SystemStored {
			select {
			case packet := <-recv:
				var content struct {
					Type          int    `json:"type"`
					SchemaVersion int    `json:"schemaVersion"`
					Event         string `json:"event"`
				}
				result.SystemStored = json.Unmarshal(packet.Payload, &content) == nil &&
					content.Type == 1002 && content.SchemaVersion == 1 && content.Event == "group.profile.updated"
			case <-ctx.Done():
				return errors.New("timed out waiting for persistent group system message")
			}
		}
		serverClientNo := "probe_server_stored_" + suffix
		var stored struct {
			Message struct {
				ID          string `json:"id"`
				ClientMsgID string `json:"clientMsgId"`
			} `json:"message"`
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/messages/conversations/"+businessDirectID+"/send", aliceSession.AccessToken, map[string]any{
			"clientMsgId": serverClientNo, "type": "text", "body": map[string]any{"text": "server stored probe"},
		}, &stored); err != nil {
			return fmt.Errorf("server stored message: %w", err)
		}
		for !result.ServerStored {
			var serverStoredSequence int64
			select {
			case packet := <-recv:
				var content struct {
					Type    int    `json:"type"`
					Content string `json:"content"`
				}
				result.ServerStored = json.Unmarshal(packet.Payload, &content) == nil &&
					content.Type == 1 && content.Content == "server stored probe" &&
					stored.Message.ID == fmt.Sprintf("%d", packet.MessageID) &&
					stored.Message.ClientMsgID == serverClientNo
				if result.ServerStored {
					serverStoredSequence = int64(packet.MessageSeq)
				}
			case <-ctx.Done():
				return errors.New("timed out waiting for server-stored WuKong message")
			}
			if result.ServerStored {
				if err := putBusinessAuthorized(ctx, cfg, "/v2/channels/conversations/"+businessDirectID+"/read", bobSession.AccessToken, map[string]any{"seq": serverStoredSequence}, nil); err != nil {
					return fmt.Errorf("mark WuKong conversation read: %w", err)
				}
				readSynced, err := syncBusinessReadState(ctx, cfg, bobSession.AccessToken, aliceUID, serverStoredSequence)
				if err != nil || !readSynced {
					return fmt.Errorf("WuKong read state did not sync: synced=%v err=%w", readSynced, err)
				}
				result.ReadSync = true
				var historyRoute struct {
					Items []struct {
						ID   string         `json:"id"`
						Body map[string]any `json:"body"`
					} `json:"items"`
				}
				if err := getBusinessAuthorized(ctx, cfg, "/v2/messages/conversations/"+businessDirectID+"/history?limit=20", bobSession.AccessToken, &historyRoute); err != nil {
					return fmt.Errorf("WuKong history compatibility route: %w", err)
				}
				for _, item := range historyRoute.Items {
					if item.ID == stored.Message.ID && item.Body["text"] == "server stored probe" {
						result.HistoryRoute = true
					}
				}
				if !result.HistoryRoute {
					return fmt.Errorf("history route did not read WuKong: %#v", historyRoute.Items)
				}
			}
		}
		if err := putBusinessAuthorized(ctx, cfg, "/v2/messages/"+stored.Message.ID+"/reactions/"+url.PathEscape("👍"), bobSession.AccessToken, nil, nil); err != nil {
			return fmt.Errorf("WuKong reaction extension: %w", err)
		}
		reactionSynced, _, _, err := syncBusinessExtension(ctx, cfg, bobSession.AccessToken, aliceUID, stored.Message.ID)
		if err != nil || !reactionSynced {
			return fmt.Errorf("WuKong reaction extension did not sync: reaction=%v err=%w", reactionSynced, err)
		}
		result.ReactionSync = true
		forwardDeadline := time.Now().Add(5 * time.Second)
		for {
			err := postBusinessAuthorized(ctx, cfg, "/v2/messages/forward", aliceSession.AccessToken, map[string]any{
				"targetConversationId": businessDirectID, "sourceMessageIds": []string{stored.Message.ID}, "mode": "separate", "clientBatchId": "probe-forward-" + suffix,
			}, nil)
			if err == nil {
				break
			}
			if time.Now().After(forwardDeadline) {
				return fmt.Errorf("forward stored message: %w", err)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		for !result.Forwarded {
			select {
			case packet := <-recv:
				var content struct {
					Type            int    `json:"type"`
					Content         string `json:"content"`
					Forwarded       bool   `json:"forwarded"`
					SourceMessageID string `json:"sourceMessageId"`
				}
				result.Forwarded = json.Unmarshal(packet.Payload, &content) == nil && content.Type == 1 &&
					content.Content == "server stored probe" && content.Forwarded && content.SourceMessageID == stored.Message.ID
			case <-ctx.Done():
				return errors.New("timed out waiting for forwarded WuKong message")
			}
		}
		if err := patchBusinessAuthorized(ctx, cfg, "/v2/messages/"+stored.Message.ID, aliceSession.AccessToken, map[string]any{
			"editId": "probe-edit-" + suffix, "text": "server edited probe",
		}, nil); err != nil {
			return fmt.Errorf("WuKong edit extension: %w", err)
		}
		_, _, edited, err := syncBusinessExtension(ctx, cfg, bobSession.AccessToken, aliceUID, stored.Message.ID)
		if err != nil || !edited {
			return fmt.Errorf("WuKong edit extension did not sync: edited=%v err=%w", edited, err)
		}
		result.EditSync = true
		extraVersion, recalled, revoker, editedContent, err := syncBusinessMessageExtra(ctx, cfg, bobSession.AccessToken, aliceUID, stored.Message.ID, 0)
		if err != nil || extraVersion <= 0 || recalled || revoker != "" || editedContent != "server edited probe" {
			return fmt.Errorf("WuKong message extra edit sync failed: version=%d recalled=%v revoker=%q content=%q err=%w", extraVersion, recalled, revoker, editedContent, err)
		}
		var search struct {
			Items []struct {
				ID          string         `json:"id"`
				Body        map[string]any `json:"body"`
				EditVersion int            `json:"editVersion"`
			} `json:"items"`
		}
		if err := getBusinessAuthorized(ctx, cfg, "/v2/messages/search?conversationId="+url.QueryEscape(businessDirectID)+"&q="+url.QueryEscape("server edited probe")+"&limit=20", bobSession.AccessToken, &search); err != nil {
			return fmt.Errorf("search WuKong history: %w", err)
		}
		for _, item := range search.Items {
			if item.ID == stored.Message.ID && item.Body["text"] == "server edited probe" && item.EditVersion == 1 {
				result.SearchSync = true
			}
		}
		if !result.SearchSync {
			return fmt.Errorf("edited WuKong message was not searchable: %#v", search.Items)
		}
		scheduledClientNo := "probe_scheduled_" + suffix
		if err := postBusinessAuthorized(ctx, cfg, "/v2/messages/scheduled", aliceSession.AccessToken, map[string]any{
			"conversationId": businessDirectID, "clientMsgId": scheduledClientNo,
			"type": "text", "body": map[string]any{"text": "scheduled stored probe"},
			"scheduledAt": time.Now().UTC().Add(6 * time.Second),
		}, nil); err != nil {
			return fmt.Errorf("schedule stored message: %w", err)
		}
		var scheduledMessageID int64
		for scheduledMessageID == 0 {
			select {
			case packet := <-recv:
				var content struct {
					Type    int    `json:"type"`
					Content string `json:"content"`
				}
				if json.Unmarshal(packet.Payload, &content) == nil && content.Type == 1 && content.Content == "scheduled stored probe" {
					scheduledMessageID = packet.MessageID
				}
			case <-ctx.Done():
				return errors.New("timed out waiting for scheduled WuKong message")
			}
		}
		var scheduledList struct {
			Items []struct {
				ClientMsgID   string `json:"clientMsgId"`
				Status        string `json:"status"`
				SentMessageID string `json:"sentMessageId"`
			} `json:"items"`
		}
		scheduledDeadline := time.Now().Add(3 * time.Second)
		for !result.Scheduled {
			scheduledList.Items = nil
			if err := getBusinessAuthorized(ctx, cfg, "/v2/messages/scheduled?status=sent&limit=100", aliceSession.AccessToken, &scheduledList); err != nil {
				return fmt.Errorf("list scheduled messages: %w", err)
			}
			for _, item := range scheduledList.Items {
				if item.ClientMsgID == scheduledClientNo && item.Status == "sent" && item.SentMessageID == fmt.Sprintf("%d", scheduledMessageID) {
					result.Scheduled = true
				}
			}
			if !result.Scheduled {
				if time.Now().After(scheduledDeadline) {
					return errors.New("scheduled message did not persist the WuKong message id")
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(50 * time.Millisecond):
				}
			}
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/messages/"+stored.Message.ID+"/recall", aliceSession.AccessToken, map[string]any{}, nil); err != nil {
			return fmt.Errorf("WuKong recall extension: %w", err)
		}
		_, recalled, _, err = syncBusinessExtension(ctx, cfg, bobSession.AccessToken, aliceUID, stored.Message.ID)
		if err != nil || !recalled {
			return fmt.Errorf("WuKong recall extension did not sync: recalled=%v err=%w", recalled, err)
		}
		result.RecallSync = true
		recallVersion, recalled, revoker, editedContent, err := syncBusinessMessageExtra(ctx, cfg, bobSession.AccessToken, aliceUID, stored.Message.ID, extraVersion)
		if err != nil || recallVersion <= extraVersion || !recalled || revoker != aliceUID || editedContent != "server edited probe" {
			return fmt.Errorf("WuKong message extra recall sync failed: version=%d previous=%d recalled=%v revoker=%q content=%q err=%w", recallVersion, extraVersion, recalled, revoker, editedContent, err)
		}
		result.MessageExtra = true
		if err := patchBusinessAuthorized(ctx, cfg, "/v2/channels/conversations/"+businessDirectID+"/preferences", aliceSession.AccessToken, map[string]any{"notificationsMuted": true}, nil); err != nil {
			return fmt.Errorf("conversation preferences: %w", err)
		}
		for !result.BusinessCMD {
			select {
			case packet := <-aliceRecv:
				var command struct {
					Type  int            `json:"type"`
					CMD   string         `json:"cmd"`
					Param map[string]any `json:"param"`
				}
				payload, _ := command.Param["payload"].(map[string]any)
				if json.Unmarshal(packet.Payload, &command) == nil && command.Type == 99 && command.CMD == "conversation.preferences.updated" && command.Param["event"] == command.CMD && command.Param["schemaVersion"] == float64(1) {
					payload, _ = command.Param["payload"].(map[string]any)
					result.BusinessCMD = payload["conversationId"] == businessDirectID
				}
			case <-ctx.Done():
				return errors.New("timed out waiting for WuKong business command")
			}
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/channels/conversations/"+businessDirectID+"/typing", aliceSession.AccessToken, map[string]any{"typing": true}, nil); err != nil {
			return fmt.Errorf("typing event: %w", err)
		}
		for !result.TypingCMD {
			select {
			case packet := <-recv:
				var command struct {
					Type  int            `json:"type"`
					CMD   string         `json:"cmd"`
					Param map[string]any `json:"param"`
				}
				if json.Unmarshal(packet.Payload, &command) == nil && command.Type == 99 && command.CMD == "typing" && command.Param["event"] == command.CMD && command.Param["schemaVersion"] == float64(1) {
					payload, _ := command.Param["payload"].(map[string]any)
					result.TypingCMD = payload["conversationId"] == businessDirectID && payload["userId"] == aliceUID && payload["typing"] == true
				}
			case <-ctx.Done():
				return errors.New("timed out waiting for WuKong typing command")
			}
		}
		callID := "probe_call_" + suffix
		if err := postBusinessAuthorized(ctx, cfg, "/v2/calls/invite", aliceSession.AccessToken, map[string]any{
			"callId": callID, "conversationId": businessDirectID, "calleeUserId": bobUID, "mediaType": "audio",
		}, nil); err != nil {
			return fmt.Errorf("call invite: %w", err)
		}
		for !result.CallCommand {
			select {
			case packet := <-recv:
				var command struct {
					Type  int            `json:"type"`
					CMD   string         `json:"cmd"`
					Param map[string]any `json:"param"`
				}
				if json.Unmarshal(packet.Payload, &command) == nil && command.Type == 99 && command.CMD == "call.invited" && command.Param["callId"] == callID && command.Param["contentType"] == float64(1005) && command.Param["schemaVersion"] == float64(1) {
					result.CallCommand = true
				}
			case <-ctx.Done():
				return errors.New("timed out waiting for WuKong call command")
			}
		}
		var callConfig struct {
			Provider            string `json:"provider"`
			URL                 string `json:"url"`
			MaxParticipants     int    `json:"maxParticipants"`
			SupportsScreenShare bool   `json:"supportsScreenShare"`
		}
		if err := getBusinessAuthorized(ctx, cfg, "/v2/calls/config", aliceSession.AccessToken, &callConfig); err != nil {
			return fmt.Errorf("LiveKit call config: %w", err)
		}
		if callConfig.Provider != "livekit" || callConfig.URL == "" || callConfig.MaxParticipants != 9 || !callConfig.SupportsScreenShare {
			return fmt.Errorf("unexpected LiveKit call config: %#v", callConfig)
		}
		if err := postBusinessAuthorized(ctx, cfg, "/v2/calls/"+callID+"/accept", bobSession.AccessToken, map[string]any{}, nil); err != nil {
			return fmt.Errorf("LiveKit call accept/room creation: %w", err)
		}
		result.LiveKitRoom = true
		for label, session := range map[string]businessSession{"caller": aliceSession, "callee": bobSession} {
			var tokenResponse struct {
				Session struct {
					URL      string `json:"url"`
					RoomName string `json:"roomName"`
					Token    string `json:"token"`
				} `json:"session"`
			}
			if err := postBusinessAuthorized(ctx, cfg, "/v2/calls/"+callID+"/token", session.AccessToken, map[string]any{}, &tokenResponse); err != nil {
				return fmt.Errorf("LiveKit %s participant token: %w", label, err)
			}
			if tokenResponse.Session.URL != callConfig.URL || tokenResponse.Session.RoomName != "call_"+callID || tokenResponse.Session.Token == "" {
				return fmt.Errorf("incomplete LiveKit %s session: url=%q room=%q token=%v", label, tokenResponse.Session.URL, tokenResponse.Session.RoomName, tokenResponse.Session.Token != "")
			}
		}
		result.LiveKitToken = true
		if err := postBusinessAuthorized(ctx, cfg, "/v2/calls/"+callID+"/hangup", aliceSession.AccessToken, map[string]string{"reason": "probe completed"}, nil); err != nil {
			return fmt.Errorf("LiveKit call cleanup: %w", err)
		}
		result.LiveKitClean = true

		webConn, err := connectRawDevice(ctx, cfg.tcpURL, aliceUID, aliceWebSession.IMSession.Token, wkproto.WEB)
		if err != nil {
			return fmt.Errorf("Alice Web raw device connect: %w", err)
		}
		defer webConn.Close()
		macConn, err := connectRawDevice(ctx, cfg.tcpURL, aliceUID, aliceMacSession.IMSession.Token, wkproto.PC)
		if err != nil {
			return fmt.Errorf("Alice macOS raw device connect: %w", err)
		}
		defer macConn.Close()
		drainRecv(aliceRecv)
		multiClientNo := "probe_multidevice_" + suffix
		if err := bob.SendMessage(wkclient.NewChannel(aliceUID, 1), []byte(`{"type":1,"content":"multi device sync probe"}`), wkclient.SendOptionWithClientMsgNo(multiClientNo)); err != nil {
			return fmt.Errorf("multi-device send: %w", err)
		}
		multiMessageID, err := waitSendACK(ctx, bobAck, 5*time.Second)
		if err != nil {
			return fmt.Errorf("multi-device send ACK: %w", err)
		}
		appMessageID, err := waitClientMessage(ctx, aliceRecv, multiMessageID, 5*time.Second)
		if err != nil {
			return fmt.Errorf("Android multi-device receive: %w", err)
		}
		webMessageID, err := waitRawMessage(webConn, multiMessageID, 5*time.Second)
		if err != nil {
			return fmt.Errorf("Web multi-device receive: %w", err)
		}
		macMessageID, err := waitRawMessage(macConn, multiMessageID, 5*time.Second)
		if err != nil {
			return fmt.Errorf("macOS multi-device receive: %w", err)
		}
		result.MultiDevice = appMessageID == multiMessageID && webMessageID == multiMessageID && macMessageID == multiMessageID
		if !result.MultiDevice {
			return fmt.Errorf("multi-device message mismatch: ack=%d app=%d web=%d mac=%d", multiMessageID, appMessageID, webMessageID, macMessageID)
		}
		if err := postJSON(ctx, cfg, "/user/device_quit", map[string]any{"uid": aliceUID, "device_flag": int(wkproto.WEB)}); err != nil {
			return fmt.Errorf("targeted Web device quit: %w", err)
		}
		reason, err := waitRawDisconnect(webConn, 5*time.Second)
		if err != nil || reason != wkproto.ReasonConnectKick {
			return fmt.Errorf("Web device did not receive a kick disconnect: reason=%s err=%w", reason, err)
		}
		drainRecv(aliceRecv)
		postQuitClientNo := "probe_post_quit_" + suffix
		if err := bob.SendMessage(wkclient.NewChannel(aliceUID, 1), []byte(`{"type":1,"content":"targeted device quit probe"}`), wkclient.SendOptionWithClientMsgNo(postQuitClientNo)); err != nil {
			return fmt.Errorf("post-quit send: %w", err)
		}
		postQuitMessageID, err := waitSendACK(ctx, bobAck, 5*time.Second)
		if err != nil {
			return fmt.Errorf("post-quit send ACK: %w", err)
		}
		appPostQuitID, err := waitClientMessage(ctx, aliceRecv, postQuitMessageID, 5*time.Second)
		if err != nil {
			return fmt.Errorf("Android receive after Web quit: %w", err)
		}
		macPostQuitID, err := waitRawMessage(macConn, postQuitMessageID, 5*time.Second)
		if err != nil {
			return fmt.Errorf("macOS receive after Web quit: %w", err)
		}
		result.DeviceQuit = appPostQuitID == postQuitMessageID && macPostQuitID == postQuitMessageID
		if !result.DeviceQuit {
			return fmt.Errorf("targeted device quit affected another platform: ack=%d app=%d mac=%d", postQuitMessageID, appPostQuitID, macPostQuitID)
		}

		bob.Close()
		offlineClientNo := "probe_offline_" + suffix
		if err := alice.SendMessage(wkclient.NewChannel(channelID, channelType), []byte(`{"type":1,"content":"offline sync probe"}`), wkclient.SendOptionWithClientMsgNo(offlineClientNo)); err != nil {
			return fmt.Errorf("offline send: %w", err)
		}
		select {
		case packet := <-ack:
			if packet.ReasonCode != wkproto.ReasonSuccess || packet.MessageID == 0 || packet.MessageSeq == 0 {
				return fmt.Errorf("invalid offline send ACK: reason=%d message_id=%d message_seq=%d", packet.ReasonCode, packet.MessageID, packet.MessageSeq)
			}
		case <-ctx.Done():
			return errors.New("timed out waiting for offline send ACK")
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			conversationFound, historyFound, err := syncBusinessMessages(ctx, cfg, bobSession.AccessToken, aliceUID, offlineClientNo)
			if err == nil && conversationFound && historyFound {
				result.OfflineSync, result.HistorySync = true, true
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(100 * time.Millisecond):
			}
		}
		if !result.OfflineSync || !result.HistorySync {
			return errors.New("offline message was not returned by conversation and history sync")
		}
	}

	duplicateKick, err := runRuntimeRedactionProbe(ctx, cfg, suffix)
	if err != nil {
		return err
	}
	result.DuplicateKick = duplicateKick

	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
	return nil
}

func runRuntimeRedactionProbe(ctx context.Context, cfg options, suffix string) (bool, error) {
	// Exercise the real message-send, token-update, encrypted connection and
	// duplicate-master paths. These disposable canaries must never be logged.
	redactionUID := "probe_redaction_" + suffix
	redactionToken := "WK_LOG_REDACTION_TOKEN_MARKER_" + suffix
	if err := postJSON(ctx, cfg, "/user/token", map[string]any{
		"uid": redactionUID, "token": redactionToken,
		"device_flag": int(wkproto.APP), "device_level": 1,
	}); err != nil {
		return false, fmt.Errorf("provision duplicate-master redaction user: %w", err)
	}
	if err := postJSON(ctx, cfg, "/message/send", map[string]any{
		"client_msg_no": "probe_redaction_message_" + suffix,
		"from_uid":      redactionUID, "channel_id": redactionUID, "channel_type": 1,
		"payload": base64.StdEncoding.EncodeToString([]byte(`{"type":1,"content":"WK_LOG_REDACTION_MESSAGE_MARKER"}`)),
	}); err != nil {
		return false, fmt.Errorf("send message-body redaction canary: %w", err)
	}
	firstMaster, err := connectRawDevice(ctx, cfg.tcpURL, redactionUID, redactionToken, wkproto.APP)
	if err != nil {
		return false, fmt.Errorf("first duplicate-master connection: %w", err)
	}
	defer firstMaster.Close()
	secondMaster, err := connectRawDevice(ctx, cfg.tcpURL, redactionUID, redactionToken, wkproto.APP)
	if err != nil {
		return false, fmt.Errorf("second duplicate-master connection: %w", err)
	}
	defer secondMaster.Close()
	reason, err := waitRawDisconnect(firstMaster, 5*time.Second)
	if err != nil || reason != wkproto.ReasonConnectKick {
		return false, fmt.Errorf("old duplicate-master connection was not kicked: reason=%s err=%w", reason, err)
	}
	return true, nil
}

func connectRawDevice(ctx context.Context, rawAddress, uid, token string, deviceFlag wkproto.DeviceFlag) (net.Conn, error) {
	address := strings.TrimPrefix(strings.TrimSpace(rawAddress), "tcp://")
	if address == "" {
		return nil, errors.New("TCP address is required")
	}
	conn, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, err
	}
	connected := false
	defer func() {
		if !connected {
			_ = conn.Close()
		}
	}()
	if err = conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return nil, err
	}
	_, clientPublicKey := wkutil.GetCurve25519KeypPair()
	connect := &wkproto.ConnectPacket{
		Version:         wkproto.LatestVersion,
		DeviceID:        wkutil.GenUUID(),
		DeviceFlag:      deviceFlag,
		ClientKey:       base64.StdEncoding.EncodeToString(clientPublicKey[:]),
		ClientTimestamp: time.Now().Unix(),
		UID:             uid,
		Token:           token,
	}
	if err = writeRawFrame(conn, connect); err != nil {
		return nil, err
	}
	frame, err := wkproto.New().DecodePacketWithConn(conn, wkproto.LatestVersion)
	if err != nil {
		return nil, err
	}
	ack, ok := frame.(*wkproto.ConnackPacket)
	if !ok {
		return nil, fmt.Errorf("expected CONNACK, got %T", frame)
	}
	if ack.ReasonCode != wkproto.ReasonSuccess {
		return nil, fmt.Errorf("connection rejected: %s", ack.ReasonCode)
	}
	if err = conn.SetDeadline(time.Time{}); err != nil {
		return nil, err
	}
	connected = true
	return conn, nil
}

func writeRawFrame(conn net.Conn, frame wkproto.Frame) error {
	encoded, err := wkproto.New().EncodeFrame(frame, wkproto.LatestVersion)
	if err != nil {
		return err
	}
	_, err = conn.Write(encoded)
	return err
}

func waitRawMessage(conn net.Conn, expectedMessageID int64, timeout time.Duration) (int64, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return 0, err
	}
	defer conn.SetReadDeadline(time.Time{})
	protocol := wkproto.New()
	for {
		frame, err := protocol.DecodePacketWithConn(conn, wkproto.LatestVersion)
		if err != nil {
			return 0, err
		}
		switch packet := frame.(type) {
		case *wkproto.RecvPacket:
			if err = writeRawFrame(conn, &wkproto.RecvackPacket{
				Framer: packet.Framer, MessageID: packet.MessageID, MessageSeq: packet.MessageSeq,
			}); err != nil {
				return 0, err
			}
			if packet.MessageID == expectedMessageID {
				return packet.MessageID, nil
			}
		case *wkproto.PingPacket:
			if err = writeRawFrame(conn, &wkproto.PongPacket{}); err != nil {
				return 0, err
			}
		case *wkproto.DisconnectPacket:
			return 0, fmt.Errorf("device disconnected before message: %s (%s)", packet.ReasonCode, packet.Reason)
		}
	}
}

func waitRawDisconnect(conn net.Conn, timeout time.Duration) (wkproto.ReasonCode, error) {
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return wkproto.ReasonUnknown, err
	}
	defer conn.SetReadDeadline(time.Time{})
	protocol := wkproto.New()
	for {
		frame, err := protocol.DecodePacketWithConn(conn, wkproto.LatestVersion)
		if err != nil {
			return wkproto.ReasonUnknown, err
		}
		switch packet := frame.(type) {
		case *wkproto.DisconnectPacket:
			return packet.ReasonCode, nil
		case *wkproto.RecvPacket:
			if err = writeRawFrame(conn, &wkproto.RecvackPacket{
				Framer: packet.Framer, MessageID: packet.MessageID, MessageSeq: packet.MessageSeq,
			}); err != nil {
				return wkproto.ReasonUnknown, err
			}
		case *wkproto.PingPacket:
			if err = writeRawFrame(conn, &wkproto.PongPacket{}); err != nil {
				return wkproto.ReasonUnknown, err
			}
		}
	}
}

func waitSendACK(ctx context.Context, input <-chan *wkproto.SendackPacket, timeout time.Duration) (int64, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case packet := <-input:
		if packet.ReasonCode != wkproto.ReasonSuccess || packet.MessageID == 0 || packet.MessageSeq == 0 {
			return 0, fmt.Errorf("reason=%s message_id=%d message_seq=%d", packet.ReasonCode, packet.MessageID, packet.MessageSeq)
		}
		return packet.MessageID, nil
	case <-timer.C:
		return 0, errors.New("timed out")
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func waitClientMessage(ctx context.Context, input <-chan *wkproto.RecvPacket, expectedMessageID int64, timeout time.Duration) (int64, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case packet := <-input:
			if packet.MessageID == expectedMessageID {
				return packet.MessageID, nil
			}
		case <-timer.C:
			return 0, errors.New("timed out")
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func drainRecv(input <-chan *wkproto.RecvPacket) {
	for {
		select {
		case <-input:
		default:
			return
		}
	}
}

func syncBusinessMessages(ctx context.Context, cfg options, accessToken, peerUID, clientMsgNo string) (bool, bool, error) {
	var conversations struct {
		Items []struct {
			Recents []json.RawMessage `json:"recents"`
		} `json:"items"`
	}
	if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/conversations", accessToken, map[string]any{
		"version": 0, "lastMsgSeqs": "", "msgCount": 20, "onlyUnread": false,
		"excludeChannelTypes": []int{}, "page": 1, "pageSize": 100,
	}, &conversations); err != nil {
		return false, false, err
	}
	conversationFound := false
	for _, conversation := range conversations.Items {
		for _, message := range conversation.Recents {
			conversationFound = conversationFound || strings.Contains(string(message), clientMsgNo)
		}
	}
	var history struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/messages", accessToken, map[string]any{
		"channelId": peerUID, "channelType": 1, "startMessageSeq": 0,
		"endMessageSeq": 0, "limit": 100, "pullMode": 0, "eventSummaryMode": "full",
	}, &history); err != nil {
		return conversationFound, false, err
	}
	historyFound := false
	for _, message := range history.Messages {
		historyFound = historyFound || strings.Contains(string(message), clientMsgNo)
	}
	return conversationFound, historyFound, nil
}

func loginBusiness(ctx context.Context, cfg options, phone, name, platform string) (businessSession, error) {
	var session businessSession
	if err := postBusiness(ctx, cfg, "/v2/auth/code", platform, map[string]string{"phone": phone}, nil); err != nil {
		return session, err
	}
	if err := postBusiness(ctx, cfg, "/v2/auth/login", platform, map[string]string{"phone": phone, "code": cfg.otpCode, "name": name}, &session); err != nil {
		return session, err
	}
	if session.IMSession.UID == "" || session.IMSession.Token == "" || session.IMSession.SDK == "" {
		return session, errors.New("business API returned an incomplete ImSession")
	}
	return session, nil
}

func postBusiness(ctx context.Context, cfg options, path, platform string, input, output any) error {
	return doBusiness(ctx, cfg, path, platform, "", input, output)
}

func postBusinessAuthorized(ctx context.Context, cfg options, path, accessToken string, input, output any) error {
	return doBusiness(ctx, cfg, path, "", accessToken, input, output)
}

func patchBusinessAuthorized(ctx context.Context, cfg options, path, accessToken string, input, output any) error {
	return doBusinessMethod(ctx, cfg, http.MethodPatch, path, "", accessToken, input, output)
}

func putBusinessAuthorized(ctx context.Context, cfg options, path, accessToken string, input, output any) error {
	return doBusinessMethod(ctx, cfg, http.MethodPut, path, "", accessToken, input, output)
}

func getBusinessAuthorized(ctx context.Context, cfg options, path, accessToken string, output any) error {
	return doBusinessMethod(ctx, cfg, http.MethodGet, path, "", accessToken, nil, output)
}

func doBusiness(ctx context.Context, cfg options, path, platform, accessToken string, input, output any) error {
	return doBusinessMethod(ctx, cfg, http.MethodPost, path, platform, accessToken, input, output)
}

func doBusinessMethod(ctx context.Context, cfg options, method, path, platform, accessToken string, input, output any) error {
	var requestBody io.Reader
	if input != nil {
		payload, err := json.Marshal(input)
		if err != nil {
			return err
		}
		requestBody = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(cfg.businessURL, "/")+path, requestBody)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if platform != "" {
		request.Header.Set("X-Client-Platform", platform)
	}
	if accessToken != "" {
		request.Header.Set("Authorization", "Bearer "+accessToken)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s returned %d: %s", path, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if output != nil && len(body) != 0 {
		if err = json.Unmarshal(body, output); err != nil {
			return err
		}
	}
	return nil
}

func syncBusinessExtension(ctx context.Context, cfg options, accessToken, peerUID, messageID string) (bool, bool, bool, error) {
	var history struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/messages", accessToken, map[string]any{
		"channelId": peerUID, "channelType": 1, "startMessageSeq": 0,
		"endMessageSeq": 0, "limit": 100, "pullMode": 0, "eventSummaryMode": "full",
	}, &history); err != nil {
		return false, false, false, err
	}
	for _, message := range history.Messages {
		if fmt.Sprint(message["message_idstr"]) != messageID {
			continue
		}
		payload, _ := message["payload"].(map[string]any)
		reactions, _ := payload["reactions"].([]any)
		reactionFound := false
		for _, raw := range reactions {
			reaction, _ := raw.(map[string]any)
			reactionFound = reactionFound || reaction["emoji"] == "👍" && reaction["count"] == float64(1) && reaction["reactedByMe"] == true
		}
		edited := payload["content"] == "server edited probe" && payload["editVersion"] == float64(1) && payload["editedAt"] != nil
		return reactionFound, payload["recalledAt"] != nil, edited, nil
	}
	return false, false, false, nil
}

func syncBusinessMessageExtra(ctx context.Context, cfg options, accessToken, peerUID, messageID string, version int64) (int64, bool, string, string, error) {
	var output struct {
		Items []map[string]any `json:"items"`
	}
	if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/message-extras", accessToken, map[string]any{
		"channelId": peerUID, "channelType": 1, "version": version, "limit": 500,
	}, &output); err != nil {
		return 0, false, "", "", err
	}
	for _, item := range output.Items {
		if fmt.Sprint(item["message_idstr"]) != messageID {
			continue
		}
		syncVersion, _ := item["extra_version"].(float64)
		revoke, _ := item["revoke"].(float64)
		revoker, _ := item["revoker"].(string)
		contentEdit, _ := item["content_edit"].(map[string]any)
		content, _ := contentEdit["content"].(string)
		return int64(syncVersion), revoke == 1, revoker, content, nil
	}
	return 0, false, "", "", fmt.Errorf("message %s was not returned after version %d", messageID, version)
}

func syncBusinessReadState(ctx context.Context, cfg options, accessToken, peerUID string, expectedSequence int64) (bool, error) {
	var output struct {
		Items []map[string]any `json:"items"`
	}
	if err := postBusinessAuthorized(ctx, cfg, "/v2/im/datasource/conversations", accessToken, map[string]any{
		"version": 0, "lastMsgSeqs": "", "msgCount": 1, "onlyUnread": false,
		"page": 0, "pageSize": 100,
	}, &output); err != nil {
		return false, err
	}
	for _, conversation := range output.Items {
		if fmt.Sprint(conversation["channel_id"]) != peerUID || conversation["channel_type"] != float64(1) {
			continue
		}
		unread, _ := conversation["unread"].(float64)
		readSequence, _ := conversation["readed_to_msg_seq"].(float64)
		return unread == 0 && int64(readSequence) >= expectedSequence, nil
	}
	return false, nil
}

func postJSON(ctx context.Context, cfg options, path string, input any) error {
	payload, err := json.Marshal(input)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(cfg.apiURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("token", cfg.managerToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s returned %d: %s", path, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func getManagerJSON(ctx context.Context, cfg options, path string, output any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(cfg.managerURL, "/")+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("token", cfg.managerToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("%s returned %d: %s", path, response.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}
