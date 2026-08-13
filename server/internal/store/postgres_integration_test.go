package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linli/im/server/internal/model"
	"github.com/linli/im/server/internal/wukong"
)

func insertTestWukongMessage(t *testing.T, p *Postgres, ctx context.Context, messageID int64, clientMsgNo, conversationID, senderID string, messageSeq int64, contentType int, expiresAt *time.Time, mediaID string, at time.Time) *model.Message {
	t.Helper()
	channelID := conversationID
	channelType := wukong.ChannelGroup
	var kind string
	if err := p.pool.QueryRow(ctx, `SELECT kind FROM im_conversations WHERE id=$1`, conversationID).Scan(&kind); err != nil {
		t.Fatal(err)
	}
	if kind == "direct" {
		channelType = wukong.ChannelPerson
		if err := p.pool.QueryRow(ctx, `SELECT user_id FROM im_members WHERE conversation_id=$1 AND user_id<>$2 ORDER BY user_id LIMIT 1`, conversationID, senderID).Scan(&channelID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(
		message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,
		content_type,media_id,expires_at,payload_sha256,message_timestamp,indexed_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)`, messageID, clientMsgNo,
		conversationID, senderID, channelID, channelType, messageSeq, contentType, mediaID, expiresAt,
		strings.Repeat("f", 64), at); err != nil {
		t.Fatal(err)
	}
	messageType := "text"
	if contentType == wukong.ContentTypeImage {
		messageType = "image"
	}
	return &model.Message{ID: strconv.FormatInt(messageID, 10), ClientMsgID: clientMsgNo, ConversationID: conversationID, SenderID: senderID, Seq: messageSeq, Type: messageType, ExpiresAt: expiresAt, CreatedAt: at}
}

func TestAdminStatsExposeRealDashboardSeries(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	suffix := strconv.FormatInt(time.Now().UnixNano(), 36)
	userID := "dashboard_user_" + suffix
	directID := "dashboard_direct_" + suffix
	groupID := "dashboard_group_" + suffix
	auditID := "dashboard_audit_" + suffix
	now := time.Now().UTC().Truncate(time.Second)
	messageIDs := []int64{now.UnixNano(), now.UnixNano() + 1}
	defer func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE message_id=ANY($1::bigint[])`, messageIDs)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_audits WHERE id=$1`, auditID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=ANY($1::text[])`, []string{directID, groupID})
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=$1`, userID)
	}()
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,'Dashboard user',$3,$3)`, userID, "dashboard_phone_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES
		($1,'direct','Dashboard direct',$3,$3),($2,'group','Dashboard group',$3,$3)`, directID, groupID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(
		message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,
		content_type,payload_sha256,message_timestamp,indexed_at) VALUES
		($1,$2,$3,$7,$7,1,1,1,$8,$9,$9),
		($4,$5,$6,$7,$6,2,1,1,$8,$9,$9)`,
		messageIDs[0], "dashboard-direct-"+suffix, directID,
		messageIDs[1], "dashboard-group-"+suffix, groupID,
		userID, strings.Repeat("d", 64), now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_audits(id,actor_id,action,target_type,target_id,metadata,result,ip,created_at)
		VALUES($1,$2,'dashboard.test','dashboard',$3,'{}'::jsonb,'success','127.0.0.1',$4)`, auditID, userID, groupID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	stats, err := p.AdminStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trend, ok := stats["messageTrend"].([]map[string]any)
	if !ok || len(trend) != 12 {
		t.Fatalf("message trend=%#v", stats["messageTrend"])
	}
	if count, ok := trend[len(trend)-1]["count"].(int64); !ok || count < 2 {
		t.Fatalf("latest trend bucket=%#v", trend[len(trend)-1])
	}
	mix, ok := stats["channelMix"].([]map[string]any)
	if !ok {
		t.Fatalf("channel mix=%#v", stats["channelMix"])
	}
	counts := map[string]int64{}
	for _, item := range mix {
		kind, _ := item["kind"].(string)
		count, _ := item["count"].(int64)
		counts[kind] = count
	}
	if counts["direct"] < 1 || counts["group"] < 1 {
		t.Fatalf("channel counts=%#v", counts)
	}
	activity, ok := stats["activity"].([]map[string]any)
	if !ok || len(activity) == 0 || activity[0]["id"] != auditID {
		t.Fatalf("activity=%#v", stats["activity"])
	}
}

func TestSupportWorkflowUsesExactWukongVisitorAndCustomerChannels(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	visitor1, visitor2, visitor3 := "support_visitor_a_"+suffix, "support_visitor_b_"+suffix, "support_visitor_c_"+suffix
	visitor4, visitor5 := "support_visitor_d_"+suffix, "support_visitor_e_"+suffix
	agent1, agent2, outsider := "support_agent_a_"+suffix, "support_agent_b_"+suffix, "support_outsider_"+suffix
	users := []string{visitor1, visitor2, visitor3, visitor4, visitor5, agent1, agent2, outsider}
	for index, uid := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$3,now(),now())`,
			uid, fmt.Sprintf("177%08d", time.Now().UnixNano()%100000000+int64(index)), "Support "+uid); err != nil {
			t.Fatal(err)
		}
	}
	skillID := "support_skill_" + suffix
	skill, err := p.SaveSupportSkillGroup(ctx, SupportSkillGroupInput{
		ID: skillID, Name: "售后支持", Description: "订单与售后", RoutingStrategy: "least_active",
		MaxConcurrentPerAgent: 3, Enabled: true, ActorID: "admin-test",
	}, time.Now())
	if err != nil || !skill.Enabled || skill.ID != skillID {
		t.Fatalf("save skill: item=%#v err=%v", skill, err)
	}
	if _, err = p.SaveSupportAgent(ctx, SupportAgentInput{UserID: agent1, Status: "available", MaxConcurrent: 3, SkillGroupIDs: []string{skillID}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.SaveSupportAgent(ctx, SupportAgentInput{UserID: agent2, Status: "busy", MaxConcurrent: 3, SkillGroupIDs: []string{skillID}}, time.Now()); err != nil {
		t.Fatal(err)
	}

	session1ID := "support_session_a_" + suffix
	session1, created, err := p.CreateSupportSession(ctx, SupportSessionCreate{
		ID: session1ID, VisitorID: visitor1, SkillGroupID: skillID, Subject: "需要退款",
		ChannelType: int(wukong.ChannelVisitor), Metadata: map[string]any{"orderId": "order-a"}, At: time.Now(),
	})
	if err != nil || !created || session1.Status != "active" || session1.AssignedAgentID != agent1 ||
		session1.ChannelID != visitor1 || session1.ChannelType != int(wukong.ChannelVisitor) {
		t.Fatalf("create visitor session: item=%#v created=%v err=%v", session1, created, err)
	}
	for _, sender := range []string{visitor1, agent1} {
		route, policyErr := p.AuthorizeWukongClientMessage(ctx, WukongClientMessageInput{
			UserID: sender, ChannelID: visitor1, ChannelType: wukong.ChannelVisitor, Type: "text", Text: "hello",
		})
		if policyErr != nil || route.ChannelID != visitor1 || route.ChannelType != wukong.ChannelVisitor {
			t.Fatalf("visitor policy sender=%s route=%#v err=%v", sender, route, policyErr)
		}
	}
	if _, err = p.AuthorizeWukongClientMessage(ctx, WukongClientMessageInput{
		UserID: outsider, ChannelID: visitor1, ChannelType: wukong.ChannelVisitor, Type: "text", Text: "forged",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("outsider visitor policy err=%v", err)
	}
	var operations []string
	if err = p.pool.QueryRow(ctx, `SELECT array_agg(operation ORDER BY id) FROM im_wukong_outbox WHERE aggregate_id=$1`, session1ID).Scan(&operations); err != nil {
		t.Fatal(err)
	}
	if len(operations) != 1 || operations[0] != wukong.OperationStoredMessage {
		t.Fatalf("support event operations=%v", operations)
	}
	var reconcileBeforeEvent bool
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM im_wukong_outbox reconcile JOIN im_wukong_outbox event ON event.aggregate_id=$1
		WHERE reconcile.operation=$2 AND reconcile.aggregate_id=$3 AND event.operation=$4 AND reconcile.id<event.id)`,
		session1ID, wukong.OperationChannelReconcile, fmt.Sprintf("%02d:%s", wukong.ChannelVisitor, visitor1), wukong.OperationStoredMessage).Scan(&reconcileBeforeEvent); err != nil || !reconcileBeforeEvent {
		t.Fatalf("initial reconcile ordering=%v err=%v", reconcileBeforeEvent, err)
	}

	transferred, err := p.TransferSupportSession(ctx, agent1, session1ID, agent2, time.Now())
	if err != nil || transferred.AssignedAgentID != agent2 || transferred.TransferCount != 1 {
		t.Fatalf("transfer session: item=%#v err=%v", transferred, err)
	}
	var oldMember, newMember bool
	if err = p.pool.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2),
		EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$3)`, visitor1, agent1, agent2).Scan(&oldMember, &newMember); err != nil || oldMember || !newMember {
		t.Fatalf("transfer membership old=%v new=%v err=%v", oldMember, newMember, err)
	}
	ended, err := p.EndSupportSession(ctx, agent2, session1ID, time.Now())
	if err != nil || ended.Status != "ended" || ended.EndedAt == nil || ended.EndedBy != agent2 {
		t.Fatalf("end session: item=%#v err=%v", ended, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM im_members WHERE conversation_id=$1 AND user_id=$2)`, visitor1, agent2).Scan(&newMember); err != nil || newMember {
		t.Fatalf("ended agent remains subscribed=%v err=%v", newMember, err)
	}
	if _, err = p.AuthorizeWukongClientMessage(ctx, WukongClientMessageInput{
		UserID: visitor1, ChannelID: visitor1, ChannelType: wukong.ChannelVisitor, Type: "text", Text: "after end",
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("ended visitor session still accepts messages err=%v", err)
	}
	rated, err := p.RateSupportSession(ctx, visitor1, session1ID, 5, "解决很快", time.Now())
	if err != nil || rated.Rating != 5 || rated.RatingComment != "解决很快" || rated.RatedAt == nil {
		t.Fatalf("rate session: item=%#v err=%v", rated, err)
	}
	for _, expected := range []struct {
		event      string
		recipients []string
	}{
		{event: "support.session.transferred", recipients: []string{visitor1, agent1, agent2}},
		{event: "support.session.ended", recipients: []string{visitor1, agent2}},
		{event: "support.session.rated", recipients: []string{agent2}},
	} {
		recipientsJSON, _ := json.Marshal(expected.recipients)
		var commands int
		if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox
			WHERE operation=$1 AND aggregate_type='support_session' AND aggregate_id=$2
			AND payload->>'event'=$3 AND payload->'recipients' @> $4::jsonb`,
			wukong.OperationBusinessEvent, session1ID, expected.event, recipientsJSON).Scan(&commands); err != nil || commands != 1 {
			t.Fatalf("support CMD event=%s recipients=%v count=%d err=%v", expected.event, expected.recipients, commands, err)
		}
	}

	session2ID := "support_session_b_" + suffix
	session2, created, err := p.CreateSupportSession(ctx, SupportSessionCreate{
		ID: session2ID, VisitorID: visitor2, SkillGroupID: skillID, Subject: "旧版客服",
		ChannelType: int(wukong.ChannelCustomer), At: time.Now(),
	})
	if err != nil || !created || session2.ChannelID != visitor2+"|"+skillID || session2.ChannelType != int(wukong.ChannelCustomer) || session2.AssignedAgentID != agent1 {
		t.Fatalf("create customer session: item=%#v created=%v err=%v", session2, created, err)
	}
	for _, sender := range []string{visitor2, agent1} {
		if _, err = p.AuthorizeWukongClientMessage(ctx, WukongClientMessageInput{
			UserID: sender, ChannelID: session2.ChannelID, ChannelType: wukong.ChannelCustomer, Type: "text", Text: "hello",
		}); err != nil {
			t.Fatalf("customer policy sender=%s err=%v", sender, err)
		}
	}

	if _, _, err = p.SetSupportAgentStatus(ctx, agent1, "offline", time.Now()); err != nil {
		t.Fatal(err)
	}
	session3ID := "support_session_c_" + suffix
	session3, created, err := p.CreateSupportSession(ctx, SupportSessionCreate{
		ID: session3ID, VisitorID: visitor3, SkillGroupID: skillID, Subject: "排队测试",
		ChannelType: int(wukong.ChannelVisitor), At: time.Now(),
	})
	if err != nil || !created || session3.Status != "queued" || session3.QueuePosition != 1 {
		t.Fatalf("queued session: item=%#v created=%v err=%v", session3, created, err)
	}
	session4ID := "support_session_d_" + suffix
	session4, created, err := p.CreateSupportSession(ctx, SupportSessionCreate{
		ID: session4ID, VisitorID: visitor4, SkillGroupID: skillID, Subject: "第二位排队访客",
		ChannelType: int(wukong.ChannelVisitor), At: time.Now(),
	})
	if err != nil || !created || session4.Status != "queued" || session4.QueuePosition != 2 {
		t.Fatalf("second queued session: item=%#v created=%v err=%v", session4, created, err)
	}
	_, autoAssigned, err := p.SetSupportAgentStatus(ctx, agent1, "available", time.Now())
	if err != nil || autoAssigned == nil || autoAssigned.ID != session3ID || autoAssigned.Status != "active" || autoAssigned.AssignedAgentID != agent1 {
		t.Fatalf("agent availability assignment: item=%#v err=%v", autoAssigned, err)
	}
	if session4, err = p.GetSupportSession(ctx, visitor4, session4ID); err != nil || session4.Status != "active" || session4.AssignedAgentID != agent1 {
		t.Fatalf("availability did not fill second slot: item=%#v err=%v", session4, err)
	}
	session5ID := "support_session_e_" + suffix
	session5, created, err := p.CreateSupportSession(ctx, SupportSessionCreate{
		ID: session5ID, VisitorID: visitor5, SkillGroupID: skillID, Subject: "等待释放容量",
		ChannelType: int(wukong.ChannelVisitor), At: time.Now(),
	})
	if err != nil || !created || session5.Status != "queued" {
		t.Fatalf("capacity queue: item=%#v created=%v err=%v", session5, created, err)
	}
	if _, err = p.EndSupportSession(ctx, agent1, session2ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if session5, err = p.GetSupportSession(ctx, visitor5, session5ID); err != nil || session5.Status != "active" || session5.AssignedAgentID != agent1 {
		t.Fatalf("released capacity did not drain queue: item=%#v err=%v", session5, err)
	}
}

func TestSupportRoundRobinAndConcurrentClaim(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p2, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	agents := []string{"support_rr_agent_a_" + suffix, "support_rr_agent_b_" + suffix}
	visitors := []string{"support_rr_visitor_a_" + suffix, "support_rr_visitor_b_" + suffix, "support_rr_visitor_c_" + suffix, "support_rr_visitor_d_" + suffix, "support_claim_visitor_" + suffix}
	for index, userID := range append(append([]string{}, agents...), visitors...) {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$3,now(),now())`,
			userID, fmt.Sprintf("176%08d", time.Now().UnixNano()%100000000+int64(index)), "Support "+userID); err != nil {
			t.Fatal(err)
		}
	}
	skillID := "support_rr_skill_" + suffix
	if _, err = p.SaveSupportSkillGroup(ctx, SupportSkillGroupInput{
		ID: skillID, Name: "轮询客服", RoutingStrategy: "round_robin", MaxConcurrentPerAgent: 10,
		Enabled: true, ActorID: "admin-test",
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, agentID := range agents {
		if _, err = p.SaveSupportAgent(ctx, SupportAgentInput{
			UserID: agentID, Status: "available", MaxConcurrent: 10, SkillGroupIDs: []string{skillID},
		}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Now()
	for index, visitorID := range visitors[:4] {
		session, created, createErr := p.CreateSupportSession(ctx, SupportSessionCreate{
			ID: fmt.Sprintf("support_rr_session_%d_%s", index, suffix), VisitorID: visitorID,
			SkillGroupID: skillID, ChannelType: int(wukong.ChannelVisitor), At: base.Add(time.Duration(index) * time.Second),
		})
		expected := agents[index%len(agents)]
		if createErr != nil || !created || session.AssignedAgentID != expected {
			t.Fatalf("round robin index=%d expected=%s item=%#v created=%v err=%v", index, expected, session, created, createErr)
		}
	}
	for _, agentID := range agents {
		if _, _, err = p.SetSupportAgentStatus(ctx, agentID, "busy", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	claimSessionID := "support_claim_session_" + suffix
	queued, created, err := p.CreateSupportSession(ctx, SupportSessionCreate{
		ID: claimSessionID, VisitorID: visitors[4], SkillGroupID: skillID,
		ChannelType: int(wukong.ChannelVisitor), At: time.Now(),
	})
	if err != nil || !created || queued.Status != "queued" {
		t.Fatalf("claim queue: item=%#v created=%v err=%v", queued, created, err)
	}
	type claimResult struct {
		session *SupportSession
		err     error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for index, repo := range []*Postgres{p, p2} {
		wait.Add(1)
		go func(agentID string, repo *Postgres) {
			defer wait.Done()
			session, claimErr := repo.ClaimSupportSession(ctx, agentID, claimSessionID, time.Now())
			results <- claimResult{session: session, err: claimErr}
		}(agents[index], repo)
	}
	wait.Wait()
	close(results)
	successes, conflicts := 0, 0
	for result := range results {
		if result.err == nil && result.session != nil {
			successes++
		} else if errors.Is(result.err, ErrConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected claim result: session=%#v err=%v", result.session, result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("claim successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestMomentsVisibilityMediaInteractionsAndReminders(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	author := "moment_author_" + suffix
	friend := "moment_friend_" + suffix
	excluded := "moment_excluded_" + suffix
	stranger := "moment_stranger_" + suffix
	for index, userID := range []string{author, friend, excluded, stranger} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$3,now(),now())`,
			userID, fmt.Sprintf("175%08d", time.Now().UnixNano()%100000000+int64(index)), "Moment "+userID); err != nil {
			t.Fatal(err)
		}
	}
	for _, pair := range [][2]string{{author, friend}, {friend, author}, {author, excluded}, {excluded, author}} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id,created_at,updated_at) VALUES($1,$2,now(),now())`, pair[0], pair[1]); err != nil {
			t.Fatal(err)
		}
	}
	imageID := "moment_image_" + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,created_at,completed_at)
		VALUES($1,$2,$3,'image/png',128,'ready',now(),now())`, imageID, author, "moments/"+imageID); err != nil {
		t.Fatal(err)
	}
	momentID := "moment_" + suffix
	moment, err := p.CreateMoment(ctx, MomentCreate{
		ID: momentID, AuthorID: author, Content: "仅好友可见，排除一人", MediaKind: "images",
		MediaIDs: []string{imageID}, Visibility: "excluded", VisibleUserIDs: []string{excluded},
		Location: map[string]any{"name": "上海"}, At: time.Now(),
	})
	if err != nil || len(moment.Media) != 1 || moment.Media[0].MIME != "image/png" {
		t.Fatalf("create moment: item=%#v err=%v", moment, err)
	}
	friendFeed, _, err := p.ListMoments(ctx, friend, author, "", 20)
	if err != nil || len(friendFeed) != 1 || friendFeed[0].ID != momentID {
		t.Fatalf("friend feed=%#v err=%v", friendFeed, err)
	}
	for _, hiddenViewer := range []string{excluded, stranger} {
		feed, _, feedErr := p.ListMoments(ctx, hiddenViewer, author, "", 20)
		if feedErr != nil || len(feed) != 0 {
			t.Fatalf("hidden viewer=%s feed=%#v err=%v", hiddenViewer, feed, feedErr)
		}
		allowed, accessErr := p.CanAccessMedia(ctx, hiddenViewer, imageID)
		if accessErr != nil || allowed {
			t.Fatalf("hidden media viewer=%s allowed=%v err=%v", hiddenViewer, allowed, accessErr)
		}
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, friend, imageID); accessErr != nil || !allowed {
		t.Fatalf("friend media allowed=%v err=%v", allowed, accessErr)
	}

	liked, err := p.SetMomentLike(ctx, friend, momentID, true, time.Now())
	if err != nil || liked.LikeCount != 1 || !liked.LikedByMe {
		t.Fatalf("like moment: item=%#v err=%v", liked, err)
	}
	commentID := "moment_comment_" + suffix
	comment, err := p.CreateMomentComment(ctx, commentID, friend, momentID, "", "写得很好", time.Now())
	if err != nil || comment.AuthorID != friend {
		t.Fatalf("comment=%#v err=%v", comment, err)
	}
	replyID := "moment_reply_" + suffix
	reply, err := p.CreateMomentComment(ctx, replyID, author, momentID, commentID, "谢谢", time.Now())
	if err != nil || reply.ReplyToUserID != friend {
		t.Fatalf("reply=%#v err=%v", reply, err)
	}
	authorReminders, err := p.ListMomentReminders(ctx, author, 20)
	if err != nil || len(authorReminders) != 2 {
		t.Fatalf("author reminders=%#v err=%v", authorReminders, err)
	}
	friendReminders, err := p.ListMomentReminders(ctx, friend, 20)
	if err != nil || len(friendReminders) != 1 || friendReminders[0].Type != "reply" {
		t.Fatalf("friend reminders=%#v err=%v", friendReminders, err)
	}
	if err = p.MarkMomentRemindersRead(ctx, author, nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	authorReminders, err = p.ListMomentReminders(ctx, author, 20)
	if err != nil || authorReminders[0].ReadAt == nil || authorReminders[1].ReadAt == nil {
		t.Fatalf("read reminders=%#v err=%v", authorReminders, err)
	}
	if err = p.DeleteMomentComment(ctx, author, momentID, commentID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err = p.CreateMoment(ctx, MomentCreate{
		ID: "moment_bad_video_" + suffix, AuthorID: author, MediaKind: "video",
		MediaIDs: []string{imageID}, Visibility: "public", At: time.Now(),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("image accepted as video err=%v", err)
	}
	if err = p.CreateReportRecord(ctx, &model.Report{
		ID: "moment_report_" + suffix, ReporterID: friend, TargetType: "moment", TargetID: momentID,
		Reason: "spam", Status: "pending", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}, &model.AuditEntry{
		ID: "moment_report_audit_" + suffix, ActorID: friend, Action: "report.created",
		TargetType: "moment", TargetID: momentID, Metadata: map[string]any{}, CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("report moment: %v", err)
	}
	adminMoments, total, next, err := p.ListAdminMoments(ctx, momentID, "published", "", 20)
	if err != nil || total != 1 || next != "" || len(adminMoments) != 1 || adminMoments[0].ID != momentID {
		t.Fatalf("admin moments=%#v total=%d next=%q err=%v", adminMoments, total, next, err)
	}
	if err = p.ModerateMoment(ctx, momentID, "hidden", "content-admin", "收到有效举报", time.Now()); err != nil {
		t.Fatal(err)
	}
	if feed, _, feedErr := p.ListMoments(ctx, friend, author, "", 20); feedErr != nil || len(feed) != 0 {
		t.Fatalf("moderated hidden feed=%#v err=%v", feed, feedErr)
	}
	if err = p.ModerateMoment(ctx, momentID, "published", "content-admin", "复核恢复", time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var moderationAudits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='moment.moderated' AND target_id=$1 AND metadata ? 'reason'`, momentID).Scan(&moderationAudits); err != nil || moderationAudits != 2 {
		t.Fatalf("moment moderation audits=%d err=%v", moderationAudits, err)
	}
	if err = p.DeleteMoment(ctx, author, momentID, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		event     string
		recipient string
	}{
		{event: "moment.like.updated", recipient: author},
		{event: "moment.comment.created", recipient: author},
		{event: "moment.comment.created", recipient: friend},
		{event: "moment.deleted", recipient: friend},
	} {
		var commands int
		if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox
			WHERE operation=$1 AND aggregate_type='moment' AND aggregate_id=$2
			AND payload->>'event'=$3 AND payload->'recipients' @> $4::jsonb`,
			wukong.OperationBusinessEvent, momentID, expected.event, fmt.Sprintf(`[%q]`, expected.recipient)).Scan(&commands); err != nil || commands != 1 {
			t.Fatalf("moment CMD event=%s recipient=%s count=%d err=%v", expected.event, expected.recipient, commands, err)
		}
	}
	if feed, _, feedErr := p.ListMoments(ctx, friend, author, "", 20); feedErr != nil || len(feed) != 0 {
		t.Fatalf("deleted feed=%#v err=%v", feed, feedErr)
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, friend, imageID); accessErr != nil || allowed {
		t.Fatalf("deleted media allowed=%v err=%v", allowed, accessErr)
	}
}

func TestStickerStoreReviewFavoritesRecentAndMediaAccess(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	creator, viewer := "sticker_creator_"+suffix, "sticker_viewer_"+suffix
	categoryID, packID := "sticker_category_"+suffix, "sticker_pack_"+suffix
	coverID, stickerMediaID, stickerID := "sticker_cover_"+suffix, "sticker_media_"+suffix, "sticker_item_"+suffix
	now := time.Now().UTC()
	users := []string{creator, viewer}
	mediaIDs := []string{coverID, stickerMediaID}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_sticker_packs WHERE id=$1`, packID)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_sticker_categories WHERE id=$1`, categoryID)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_media WHERE id=ANY($1::text[])`, mediaIDs)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, users)
	})
	for index, userID := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at,updated_at) VALUES($1,$2,$3,$4,$4)`,
			userID, fmt.Sprintf("176%08d", time.Now().UnixNano()%100000000+int64(index)), userID, now); err != nil {
			t.Fatal(err)
		}
	}
	for _, mediaID := range mediaIDs {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,created_at,completed_at)
			VALUES($1,$2,$3,'image/webp',128,'ready',$4,$4)`, mediaID, creator, "stickers/"+mediaID, now); err != nil {
			t.Fatal(err)
		}
	}
	category, err := p.SaveStickerCategory(ctx, StickerCategoryInput{
		ID: categoryID, Name: "Greeting", SortOrder: 10, Enabled: true, At: now,
	})
	if err != nil || !category.Enabled {
		t.Fatalf("category=%#v err=%v", category, err)
	}
	pack, err := p.SaveStickerPack(ctx, StickerPackInput{
		ID: packID, CategoryID: categoryID, Name: "Hello", Description: "reviewed pack",
		CoverMediaID: coverID, Status: "reviewing", SortOrder: 10, ActorID: creator, At: now,
	})
	if err != nil || pack.Status != "reviewing" {
		t.Fatalf("pack=%#v err=%v", pack, err)
	}
	item, err := p.SaveStickerItem(ctx, StickerItemInput{
		ID: stickerID, PackID: packID, Name: "Wave", MediaID: stickerMediaID,
		Emoji: "👋", Status: "published", Metadata: map[string]any{"animated": true}, At: now,
	})
	if err != nil || item.MIME != "image/webp" {
		t.Fatalf("item=%#v err=%v", item, err)
	}
	if packs, listErr := p.ListStickerPacks(ctx, viewer, categoryID, false); listErr != nil || len(packs) != 0 {
		t.Fatalf("unreviewed packs=%#v err=%v", packs, listErr)
	}
	adminPacks, total, next, listErr := p.ListAdminStickerPacks(ctx, packID, "reviewing", "", 20)
	if listErr != nil || total != 1 || next != "" || len(adminPacks) != 1 || adminPacks[0].ID != packID || len(adminPacks[0].Items) != 1 {
		t.Fatalf("admin sticker packs=%#v total=%d next=%q err=%v", adminPacks, total, next, listErr)
	}
	if useErr := p.RecordStickerUse(ctx, viewer, stickerID, now); !errors.Is(useErr, ErrNotFound) {
		t.Fatalf("unpublished use err=%v", useErr)
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, viewer, stickerMediaID); accessErr != nil || allowed {
		t.Fatalf("unpublished media allowed=%v err=%v", allowed, accessErr)
	}

	pack, err = p.ReviewStickerPack(ctx, packID, "published", "", creator, now.Add(time.Second))
	if err != nil || pack.Status != "published" {
		t.Fatalf("publish pack=%#v err=%v", pack, err)
	}
	if _, err = p.ReviewStickerPack(ctx, packID, "rejected", "late rejection", creator, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("invalid review transition err=%v", err)
	}
	if _, err = p.SaveStickerPack(ctx, StickerPackInput{
		ID: packID, CategoryID: categoryID, Name: "mutated", CoverMediaID: coverID,
		Status: "reviewing", ActorID: creator, At: now.Add(2 * time.Second),
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("published pack mutated err=%v", err)
	}
	if _, err = p.SaveStickerItem(ctx, StickerItemInput{
		ID: "published_mutation_" + suffix, PackID: packID, Name: "Must review", MediaID: coverID,
		Status: "published", At: now.Add(2 * time.Second),
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("published pack item mutation err=%v", err)
	}
	publicPacks, err := p.ListStickerPacks(ctx, viewer, categoryID, false)
	if err != nil || len(publicPacks) != 1 || len(publicPacks[0].Items) != 1 || publicPacks[0].Items[0].ID != stickerID {
		t.Fatalf("public packs=%#v err=%v", publicPacks, err)
	}
	if allowed, accessErr := p.CanUseSticker(ctx, viewer, stickerID); accessErr != nil || !allowed {
		t.Fatalf("published sticker allowed=%v err=%v", allowed, accessErr)
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, viewer, stickerMediaID); accessErr != nil || !allowed {
		t.Fatalf("published media allowed=%v err=%v", allowed, accessErr)
	}
	if err = p.SetStickerPackFavorite(ctx, viewer, packID, true, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.SetStickerFavorite(ctx, viewer, stickerID, true, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.RecordStickerUse(ctx, viewer, stickerID, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.RecordStickerUse(ctx, viewer, stickerID, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	favorites, err := p.ListFavoriteStickers(ctx, viewer, 20)
	if err != nil || len(favorites) != 1 || !favorites[0].Favorite || favorites[0].ID != stickerID {
		t.Fatalf("favorites=%#v err=%v", favorites, err)
	}
	recent, err := p.ListRecentStickers(ctx, viewer, 20)
	if err != nil || len(recent) != 1 || recent[0].UseCount != 2 || recent[0].UsedAt == nil {
		t.Fatalf("recent=%#v err=%v", recent, err)
	}
	pack, err = p.GetStickerPack(ctx, viewer, packID, false)
	if err != nil || !pack.Favorite || len(pack.Items) != 1 || !pack.Items[0].Favorite {
		t.Fatalf("favorite pack=%#v err=%v", pack, err)
	}

	pack, err = p.ReviewStickerPack(ctx, packID, "disabled", "retired", creator, now.Add(7*time.Second))
	if err != nil || pack.Status != "disabled" {
		t.Fatalf("disable pack=%#v err=%v", pack, err)
	}
	if allowed, accessErr := p.CanUseSticker(ctx, viewer, stickerID); accessErr != nil || allowed {
		t.Fatalf("disabled sticker allowed=%v err=%v", allowed, accessErr)
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, viewer, stickerMediaID); accessErr != nil || allowed {
		t.Fatalf("disabled media allowed=%v err=%v", allowed, accessErr)
	}
	if recent, listErr := p.ListRecentStickers(ctx, viewer, 20); listErr != nil || len(recent) != 0 {
		t.Fatalf("disabled recent=%#v err=%v", recent, listErr)
	}
	var reviewAudits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='sticker_pack.reviewed' AND target_id=$1`, packID).Scan(&reviewAudits); err != nil || reviewAudits != 2 {
		t.Fatalf("sticker review audits=%d err=%v", reviewAudits, err)
	}
}

func TestPostgresWukongMetadataIndexIsConcurrentAndIdempotent(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	a, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1 := "test_a_" + suffix
	u2 := "test_b_" + suffix
	cid := "test_conv_" + suffix
	now := time.Now()
	if _, err = a.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES
		($1,$2,'A',$5),($3,$4,'B',$5)`, u1, "1"+suffix, u2, "2"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = a.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES
		($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = a.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE conversation_id=$1`, cid)
		_, _ = a.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = a.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{u1, u2})
	})
	const n = 32
	messageIDBase := time.Now().UnixNano()
	seqs := make(chan int64, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			repo := a
			if i%2 == 1 {
				repo = b
			}
			_, e := repo.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(
				message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,
				content_type,payload_sha256,message_timestamp,indexed_at)
				VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,now(),now())`,
				messageIDBase+int64(i), fmt.Sprintf("c-%d", i), cid, u1, u2, wukong.ChannelPerson,
				i+1, wukong.ContentTypeText, strings.Repeat("c", 64))
			if e != nil {
				errs <- e
				return
			}
			seqs <- int64(i + 1)
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	close(seqs)
	got := make([]int, 0, n)
	for s := range seqs {
		got = append(got, int(s))
	}
	sort.Ints(got)
	for i, v := range got {
		if v != i+1 {
			t.Fatalf("sequence gap at %d: %v", i, got)
		}
	}
	type dupResult struct {
		duplicate bool
		err       error
	}
	var dupWG sync.WaitGroup
	results := make(chan dupResult, 2)
	for i, repo := range []*Postgres{a, b} {
		dupWG.Add(1)
		go func(i int, repo *Postgres) {
			defer dupWG.Done()
			_, e := repo.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(
				message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,
				content_type,payload_sha256,message_timestamp,indexed_at)
				VALUES($1,'same',$2,$3,$4,$5,$6,$7,$8,now(),now())`,
				messageIDBase+int64(n+i), cid, u1, u2, wukong.ChannelPerson,
				n+i+1, wukong.ContentTypeText, strings.Repeat("d", 64))
			results <- dupResult{duplicate: e != nil}
		}(i, repo)
	}
	dupWG.Wait()
	close(results)
	duplicateCount := 0
	for r := range results {
		if r.duplicate {
			duplicateCount++
		}
	}
	if duplicateCount != 1 {
		t.Fatalf("expected one duplicate ACK, got %d", duplicateCount)
	}
}

func TestPostgresConversationLifecycleScheduledAndExpiry(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2, cid := "life_a_"+suffix, "life_b_"+suffix, "life_c_"+suffix
	now := time.Now().UTC().Truncate(time.Microsecond)
	state, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Users[u1] = &model.User{ID: u1, Phone: "31" + suffix, Name: "A", CreatedAt: now}
	state.Users[u2] = &model.User{ID: u2, Phone: "32" + suffix, Name: "B", CreatedAt: now}
	state.Conversations[cid] = &model.Conversation{ID: cid, Type: "direct", CreatedAt: now, UpdatedAt: now}
	state.Members[cid] = map[string]*model.ConversationMember{
		u1: {ConversationID: cid, UserID: u1, Role: "member", JoinedAt: now},
		u2: {ConversationID: cid, UserID: u2, Role: "member", JoinedAt: now},
	}
	if err = p.Save(ctx, state); err != nil {
		t.Fatal(err)
	}

	archived := true
	if err = p.UpdateConversationPreferences(ctx, u1, cid, ConversationPreferences{Archived: &archived}); err != nil {
		t.Fatal(err)
	}
	conversations, err := p.ListConversations(ctx, u1, 10)
	if err != nil || len(conversations) != 1 || conversations[0]["membership"].(*model.ConversationMember).Archived != true {
		t.Fatalf("archive preference was not persisted: %#v %v", conversations, err)
	}

	expires := now.Add(time.Minute)
	message := insertTestWukongMessage(t, p, ctx, time.Now().UnixNano(), "life-msg-"+suffix, cid, u1, 1, wukong.ContentTypeText, &expires, "", now)
	adminMessages, total, _, err := p.ListAdminMessages(ctx, "secret", "", "", 10)
	if err != nil || total != 0 || len(adminMessages) != 0 {
		t.Fatalf("admin message search must not inspect private body: total=%d items=%#v err=%v", total, adminMessages, err)
	}
	adminMessages, total, _, err = p.ListAdminMessages(ctx, message.ID, "", "", 10)
	if err != nil || total != 1 || len(adminMessages) != 1 || len(adminMessages[0].Body) != 0 {
		t.Fatalf("admin metadata result leaked body: total=%d items=%#v err=%v", total, adminMessages, err)
	}
	pastBan := now.Add(-time.Minute)
	if err = p.SetUserBanRecord(ctx, "admin", u2, true, &pastBan, "temporary test ban", "aud_ban_"+suffix, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	expiredUsers, err := p.ExpireUserBans(ctx, now, 10)
	if err != nil || len(expiredUsers) != 1 || expiredUsers[0] != u2 {
		t.Fatalf("expire timed ban: ids=%v err=%v", expiredUsers, err)
	}
	if err = p.RecordAdminAudit(ctx, &model.AuditEntry{ID: "aud_failed_" + suffix, ActorID: "moderator", Action: "admin.request", TargetType: "admin_request", TargetID: "/v2/admin/groups/x/disband", Metadata: map[string]any{"status": 403}, Result: "failed", IP: "203.0.113.9", CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	audits, auditTotal, _, err := p.ListAdminAudits(ctx, "groups/x", "failed", "", 10)
	foundAudit := false
	for _, item := range audits {
		if item.ID == "aud_failed_"+suffix && item.IP == "203.0.113.9" && item.Result == "failed" {
			foundAudit = true
		}
	}
	if err != nil || auditTotal < 1 || !foundAudit {
		t.Fatalf("admin failed audit: total=%d items=%#v err=%v", auditTotal, audits, err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id,created_at,updated_at) VALUES($1,$2,$3,$3),($2,$1,$3,$3)`, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	friends, friendTotal, _, err := p.ListAdminFriendships(ctx, u1, "", 10)
	if err != nil || friendTotal != 1 || len(friends) != 1 {
		t.Fatalf("admin friendships: total=%d items=%#v err=%v", friendTotal, friends, err)
	}
	feedbackContent := "cannot upload " + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_feedback(id,user_id,category,content,contact,created_at) VALUES($1,$2,'bug',$3,'contact@example.test',$4)`, `feedback_`+suffix, u1, feedbackContent, now); err != nil {
		t.Fatal(err)
	}
	feedback, feedbackTotal, _, err := p.ListAdminFeedback(ctx, feedbackContent, "bug", "", 10)
	if err != nil || feedbackTotal != 1 || len(feedback) != 1 {
		t.Fatalf("admin feedback: total=%d items=%#v err=%v", feedbackTotal, feedback, err)
	}
	groupID := "life_group_" + suffix
	groupTitle := "Operations Group " + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group',$2,$3,$3)`, groupID, groupTitle, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,announcement,announcement_version,join_policy,allow_member_add_friend,updated_at) VALUES($1,$2,'Current announcement',1,'invite',true,$3)`, groupID, u1, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'owner',$4),($1,$3,'member',$4)`, groupID, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	adminGroups, groupTotal, _, err := p.ListAdminGroups(ctx, groupTitle, "active", "", 10)
	if err != nil || groupTotal != 1 || len(adminGroups) != 1 {
		t.Fatalf("admin groups: total=%d items=%#v err=%v", groupTotal, adminGroups, err)
	}
	groupOverview, err := p.AdminGroupOverview(ctx, groupID)
	if err != nil || groupOverview["announcement"] != "Current announcement" {
		t.Fatalf("group overview: %#v err=%v", groupOverview, err)
	}
	groupMembers, memberTotal, _, err := p.ListAdminGroupMembers(ctx, groupID, "", "", 10)
	if err != nil || memberTotal != 2 || len(groupMembers) != 2 {
		t.Fatalf("group members: total=%d items=%#v err=%v", memberTotal, groupMembers, err)
	}
	actual, recipients, err := p.MarkDelivered(ctx, u2, cid, message.Seq+100, now)
	if err != nil || actual != message.Seq || len(recipients) != 2 {
		t.Fatalf("delivered cursor: actual=%d recipients=%v err=%v", actual, recipients, err)
	}
	_, recipients, err = p.MarkDelivered(ctx, u2, cid, 0, now)
	if err != nil || len(recipients) != 0 {
		t.Fatalf("delivered cursor must be monotonic and idempotent: recipients=%v err=%v", recipients, err)
	}
	if _, _, err = p.MarkDelivered(ctx, "outsider", cid, 1, now); err != ErrForbidden {
		t.Fatalf("outsider delivered cursor error=%v", err)
	}

	scheduledAt := now.Add(10 * time.Second)
	scheduled := &model.ScheduledMessage{ID: "life_s_" + suffix, UserID: u1, ConversationID: cid, ClientMsgID: "life-scheduled-" + suffix, Type: "text", Body: map[string]any{"text": "later"}, ScheduledAt: scheduledAt, Status: "pending", CreatedAt: now, UpdatedAt: now}
	created, duplicate, err := p.CreateScheduledMessage(ctx, scheduled)
	if err != nil || duplicate || created.Status != "pending" {
		t.Fatalf("create scheduled: %#v duplicate=%v err=%v", created, duplicate, err)
	}
	if _, duplicate, err = p.CreateScheduledMessage(ctx, scheduled); err != nil || !duplicate {
		t.Fatalf("scheduled idempotency duplicate=%v err=%v", duplicate, err)
	}
	leased, err := p.LeaseScheduledMessages(ctx, scheduledAt.Add(time.Second), 2*time.Minute, 10)
	if err != nil || len(leased) != 1 || leased[0].Attempts != 1 {
		t.Fatalf("lease scheduled: %#v err=%v", leased, err)
	}
	if err = p.CompleteScheduledMessage(ctx, scheduled.ID, message.ID, nil, scheduledAt.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	expired, err := p.ExpireMessages(ctx, expires.Add(time.Second), 10)
	foundExpired := false
	for _, item := range expired {
		if item.MessageID == message.ID && len(item.MemberIDs) == 2 {
			foundExpired = true
		}
	}
	if err != nil || !foundExpired {
		t.Fatalf("expire message: %#v err=%v", expired, err)
	}
	var indexedExpiredAt *time.Time
	if err = p.pool.QueryRow(ctx, `SELECT expired_at FROM im_wukong_message_index WHERE message_id=$1`, message.ID).Scan(&indexedExpiredAt); err != nil || indexedExpiredAt == nil {
		t.Fatalf("WuKong metadata expiry was not persisted: expiredAt=%v err=%v", indexedExpiredAt, err)
	}

	mediaID := "life_media_" + suffix
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,created_at) VALUES($1,$2,$3,'image/png',10,'pending',$4)`, mediaID, u1, "users/"+u1+"/abandoned.png", now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	cleanup, err := p.LeaseMediaCleanup(ctx, now, time.Hour, 24*time.Hour, 10*time.Minute, 10)
	if err != nil || len(cleanup) != 1 || cleanup[0].ID != mediaID {
		t.Fatalf("lease media cleanup: %#v err=%v", cleanup, err)
	}
	if err = p.CompleteMediaCleanup(ctx, mediaID, errors.New("object storage unavailable"), now); err != nil {
		t.Fatal(err)
	}
	status, err := p.MediaCleanupStatus(ctx, now, time.Hour, 24*time.Hour)
	if err != nil || status.PendingCandidates < 1 || status.FailedAttempts < 1 {
		t.Fatalf("media cleanup status: %#v err=%v", status, err)
	}
	cleanup, err = p.LeaseMediaCleanup(ctx, now.Add(time.Second), time.Hour, 24*time.Hour, 10*time.Minute, 10)
	if err != nil || len(cleanup) != 1 {
		t.Fatalf("retry media cleanup: %#v err=%v", cleanup, err)
	}
	if err = p.CompleteMediaCleanup(ctx, mediaID, nil, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresPolicyDeviceOwnershipAndRevision(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2, cid := "policy_a_"+suffix, "policy_b_"+suffix, "policy_c_"+suffix
	now := time.Now()
	for _, u := range []struct{ id, phone string }{{u1, "31" + suffix}, {u2, "32" + suffix}} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$3,$4)`, u.id, u.phone, u.id, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_blocks(user_id,blocked_user_id) VALUES($1,$2)`, u2, u1); err != nil {
		t.Fatal(err)
	}
	input := WukongMessageRouteInput{UserID: u1, ConversationID: cid, Type: "text", Text: "hello"}
	if _, err = p.AuthorizeWukongMessage(ctx, input); err != ErrForbidden {
		t.Fatalf("blocked send err=%v", err)
	}
	if _, err = p.pool.Exec(ctx, `DELETE FROM im_blocks WHERE user_id=$1 AND blocked_user_id=$2`, u2, u1); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_sensitive_words(id,value) VALUES($1,'forbidden|test')`, `policy_w_`+suffix); err != nil {
		t.Fatal(err)
	}
	input.Text = "contains forbidden content"
	if _, err = p.AuthorizeWukongMessage(ctx, input); err != ErrForbidden {
		t.Fatalf("sensitive send err=%v", err)
	}
	if err = p.RegisterDevice(ctx, u1, Device{ID: "shared_device_" + suffix, Platform: "ios", Provider: "apns", PushToken: "token-a-" + suffix}); err != nil {
		t.Fatal(err)
	}
	if err = p.RegisterDevice(ctx, u2, Device{ID: "shared_device_" + suffix, Platform: "ios", Provider: "apns", PushToken: "token-b-" + suffix}); err != ErrForbidden {
		t.Fatalf("device ownership err=%v", err)
	}

	s1, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s2, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s1.Users[u1].Name = "updated"
	if err = p.Save(ctx, s1); err != nil {
		t.Fatal(err)
	}
	s2.Users[u2].Name = "stale"
	if err = p.Save(ctx, s2); err != ErrConflict {
		t.Fatalf("stale snapshot err=%v", err)
	}
	sid, newID := "refresh_old_"+suffix, "refresh_new_"+suffix
	if err = p.CreateRefreshSession(ctx, sid, u1, []byte("old"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = p.RotateRefreshSession(ctx, sid, newID, []byte("new"), time.Now().Add(time.Hour), u1); err != nil {
		t.Fatal(err)
	}
	if err = p.RotateRefreshSession(ctx, sid, "reuse_"+suffix, []byte("reuse"), time.Now().Add(time.Hour), u1); err != ErrForbidden {
		t.Fatalf("refresh reuse err=%v", err)
	}
	if err = p.RevokeRefreshSession(ctx, newID, u1); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresRuntimeModerationReceiptsAndOutboxRecovery(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2 := "runtime_a_"+suffix, "runtime_b_"+suffix
	cid := "runtime_conv_" + suffix
	now := time.Now()
	for _, uid := range []string{u1, u2} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$1,$3)`, uid, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}

	msg := insertTestWukongMessage(t, p, ctx, time.Now().UnixNano(), "runtime-client", cid, u1, 1, wukong.ContentTypeText, nil, "", now)
	msgID := msg.ID
	if err = p.SetFavorite(ctx, u2, msgID, true); err != nil {
		t.Fatalf("favorite create: %v", err)
	}
	favorites, favoriteErr := p.ListFavorites(ctx, u2, 10)
	if favoriteErr != nil || len(favorites) != 1 || favorites[0].ID != msgID {
		t.Fatalf("favorites=%v err=%v", favorites, favoriteErr)
	}
	if err = p.SetFavorite(ctx, "not_a_member_"+suffix, msgID, true); err != ErrForbidden {
		t.Fatalf("non-member favorite err=%v", err)
	}
	if err = p.SetFavorite(ctx, u2, msgID, false); err != nil {
		t.Fatalf("favorite delete: %v", err)
	}
	if favorites, favoriteErr = p.ListFavorites(ctx, u2, 10); favoriteErr != nil || len(favorites) != 0 {
		t.Fatalf("favorites after delete=%v err=%v", favorites, favoriteErr)
	}
	pinned, mutedNotifications, manualUnread := true, true, true
	if err = p.UpdateConversationPreferences(ctx, u2, cid, ConversationPreferences{Pinned: &pinned, NotificationsMuted: &mutedNotifications, ManualUnread: &manualUnread}); err != nil {
		t.Fatal(err)
	}
	conversations, err := p.ListConversations(ctx, u2, 10)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("preferences list=%v err=%v", conversations, err)
	}
	membership := conversations[0]["membership"].(*model.ConversationMember)
	if !membership.Pinned || !membership.NotificationsMuted || !membership.ManualUnread {
		t.Fatalf("preferences not persisted: %+v", membership)
	}
	conversationMembers, ok := conversations[0]["members"].([]*model.ConversationMember)
	if !ok || len(conversationMembers) != 2 {
		t.Fatalf("conversation member projection=%T %+v", conversations[0]["members"], conversations[0]["members"])
	}
	for _, member := range conversationMembers {
		rawMember, _ := json.Marshal(member)
		if member.UserID == "" || member.Name == "" || strings.Contains(string(rawMember), `"phone"`) {
			t.Fatalf("unsafe conversation member=%+v", member)
		}
	}
	readSeq, users, err := p.MarkRead(ctx, u2, cid, msg.Seq, time.Now())
	if err != nil || readSeq != msg.Seq || len(users) != 2 {
		t.Fatalf("read seq=%d users=%v err=%v", readSeq, users, err)
	}
	var readEvents, readRecipients int
	if err = p.pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(jsonb_array_length(payload->'recipients')),0)
		FROM im_wukong_outbox WHERE operation=$1 AND aggregate_type='conversation' AND aggregate_id=$2
		AND payload->>'event'='message.read' AND payload->'param'->'payload'->>'conversationId'=$2`, wukong.OperationBusinessEvent, cid).Scan(&readEvents, &readRecipients); err != nil || readEvents != 1 || readRecipients != 2 {
		t.Fatalf("read CMD count=%d recipients=%d err=%v", readEvents, readRecipients, err)
	}
	if _, repeatedUsers, repeatErr := p.MarkRead(ctx, u2, cid, msg.Seq, time.Now()); repeatErr != nil || len(repeatedUsers) != 0 {
		t.Fatalf("idempotent read users=%v err=%v", repeatedUsers, repeatErr)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(jsonb_array_length(payload->'recipients')),0)
		FROM im_wukong_outbox WHERE operation=$1 AND aggregate_type='conversation' AND aggregate_id=$2
		AND payload->>'event'='message.read' AND payload->'param'->'payload'->>'conversationId'=$2`, wukong.OperationBusinessEvent, cid).Scan(&readEvents, &readRecipients); err != nil || readEvents != 1 || readRecipients != 2 {
		t.Fatalf("duplicate read CMD count=%d recipients=%d err=%v", readEvents, readRecipients, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT manual_unread FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, u2).Scan(&manualUnread); err != nil || manualUnread {
		t.Fatalf("manual unread was not cleared: value=%v err=%v", manualUnread, err)
	}
	if err = p.HideConversation(ctx, u2, cid); err != nil {
		t.Fatal(err)
	}
	if conversations, err = p.ListConversations(ctx, u2, 10); err != nil || conversations == nil || len(conversations) != 0 {
		t.Fatalf("hidden list=%v err=%v", conversations, err)
	}
	insertTestWukongMessage(t, p, ctx, time.Now().UnixNano()+1, "runtime-client-next", cid, u1, 2, wukong.ContentTypeText, nil, "", time.Now())
	if conversations, err = p.ListConversations(ctx, u2, 10); err != nil || len(conversations) != 1 {
		t.Fatalf("reappeared list=%v err=%v", conversations, err)
	}

	recalledCID, recalledSeq, recallUsers, err := p.RecallAuthorized(ctx, u1, msgID, time.Now(), 2*time.Minute)
	if err != nil || recalledCID != cid || recalledSeq != msg.Seq || len(recallUsers) != 2 {
		t.Fatalf("recall cid=%s seq=%d users=%v err=%v", recalledCID, recalledSeq, recallUsers, err)
	}
	var recalled bool
	if err = p.pool.QueryRow(ctx, `SELECT payload ? 'recalledAt' FROM im_wukong_message_extensions WHERE message_id=$1`, msgID).Scan(&recalled); err != nil || !recalled {
		t.Fatalf("WuKong recalled extension=%v err=%v", recalled, err)
	}

	reportID := "runtime_report_" + suffix
	report := &model.Report{ID: reportID, ReporterID: u2, TargetType: "message", TargetID: msgID, Reason: "abuse", Status: "pending", CreatedAt: now, UpdatedAt: now}
	audit := &model.AuditEntry{ID: "runtime_audit_create_" + suffix, ActorID: u2, Action: "report.created", TargetType: "message", TargetID: msgID, Metadata: map[string]any{"reportId": reportID}, CreatedAt: now}
	if err = p.CreateReportRecord(ctx, report, audit); err != nil {
		t.Fatal(err)
	}
	status, err := p.ResolveReportRecord(ctx, "admin", reportID, "delete_message", "confirmed", "runtime_audit_resolve_"+suffix, time.Now())
	if err != nil || status != "resolved" {
		t.Fatalf("resolve status=%s err=%v", status, err)
	}

	refreshID := "runtime_refresh_" + suffix
	if err = p.CreateRefreshSession(ctx, refreshID, u1, []byte("hash"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	banReportID := "runtime_ban_report_" + suffix
	banReport := &model.Report{ID: banReportID, ReporterID: u2, TargetType: "user", TargetID: u1, Reason: "repeat abuse", Status: "pending", CreatedAt: now, UpdatedAt: now}
	banAudit := &model.AuditEntry{ID: "runtime_audit_ban_create_" + suffix, ActorID: u2, Action: "report.created", TargetType: "user", TargetID: u1, Metadata: map[string]any{"reportId": banReportID}, CreatedAt: now}
	if err = p.CreateReportRecord(ctx, banReport, banAudit); err != nil {
		t.Fatal(err)
	}
	if status, err = p.ResolveReportRecord(ctx, "admin", banReportID, "ban_user", "confirmed", "runtime_audit_ban_"+suffix, time.Now()); err != nil || status != "resolved" {
		t.Fatalf("ban resolve status=%s err=%v", status, err)
	}
	var banned, revoked bool
	if err = p.pool.QueryRow(ctx, `SELECT banned FROM im_users WHERE id=$1`, u1).Scan(&banned); err != nil || !banned {
		t.Fatalf("banned=%v err=%v", banned, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM im_refresh_sessions WHERE id=$1`, refreshID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("revoked=%v err=%v", revoked, err)
	}

	usersPage, total, next, err := p.ListAdminUsers(ctx, suffix, "", "", 1)
	if err != nil || len(usersPage) != 1 || total != 2 || next == "" {
		t.Fatalf("admin page len=%d total=%d next=%q err=%v", len(usersPage), total, next, err)
	}

}

func TestPostgresCallLifecycleTimeoutAndAdminMetadata(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2, cid := "call_a_"+suffix, "call_b_"+suffix, "call_conv_"+suffix
	now := time.Now()
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{u1, u2})
	})
	for _, uid := range []string{u1, u2} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'direct',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$4),($1,$3,'member',$4)`, cid, u1, u2, now); err != nil {
		t.Fatal(err)
	}

	callID := "pg-call-" + suffix
	invite := CallInvite{ID: callID, ConversationID: cid, CallerID: u1, CalleeID: u2, MediaType: "video", InvitedAt: now, ExpiresAt: now.Add(30 * time.Second)}
	call, duplicate, err := p.InviteCall(ctx, invite)
	if err != nil || duplicate || call.Status != "invited" {
		t.Fatalf("invite call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	var invitePush string
	if err = p.pool.QueryRow(ctx, `SELECT payload::text FROM im_push_outbox WHERE user_id=$1 AND event_type='call.invited' ORDER BY id DESC LIMIT 1`, u2).Scan(&invitePush); err != nil || !strings.Contains(invitePush, callID) || !strings.Contains(invitePush, cid) || !strings.Contains(invitePush, "video") {
		t.Fatalf("call invite push=%s err=%v", invitePush, err)
	}
	var inviteCommand int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND aggregate_id=$2
		AND payload->>'event'='call.invited' AND payload->'recipients' @> $3::jsonb`, wukong.OperationCallEvent, callID, fmt.Sprintf(`[%q]`, u2)).Scan(&inviteCommand); err != nil || inviteCommand != 1 {
		t.Fatalf("call invite CMD=%d err=%v", inviteCommand, err)
	}
	_, duplicate, err = p.InviteCall(ctx, invite)
	if err != nil || !duplicate {
		t.Fatalf("invite retry duplicate=%v err=%v", duplicate, err)
	}
	if _, _, err = p.TransitionCall(ctx, callID, u1, "accept", "", now.Add(time.Second)); err != ErrConflict {
		t.Fatalf("caller accepted err=%v", err)
	}
	call, duplicate, err = p.TransitionCall(ctx, callID, u2, "accept", "", now.Add(time.Second))
	if err != nil || duplicate || call.Status != "accepted" || call.AcceptedAt == nil {
		t.Fatalf("accept call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	_, duplicate, err = p.TransitionCall(ctx, callID, u2, "accept", "", now.Add(2*time.Second))
	if err != nil || !duplicate {
		t.Fatalf("accept retry duplicate=%v err=%v", duplicate, err)
	}
	call, _, err = p.TransitionCall(ctx, callID, u1, "hangup", "completed", now.Add(4*time.Second))
	if err != nil || call.Status != "ended" || call.EndReason != "completed" || call.DurationSeconds < 3 {
		t.Fatalf("hangup call=%+v err=%v", call, err)
	}

	expiredID := "pg-expired-call-" + suffix
	_, _, err = p.InviteCall(ctx, CallInvite{ID: expiredID, ConversationID: cid, CallerID: u1, CalleeID: u2, MediaType: "audio", InvitedAt: now.Add(-time.Minute), ExpiresAt: now.Add(-30 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	expired, err := p.ExpireCalls(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundMissed := false
	for _, item := range expired {
		if item.ID == expiredID && item.Status == "missed" && item.EndReason == "timeout" {
			foundMissed = true
		}
	}
	if !foundMissed {
		t.Fatalf("expired call missing: %+v", expired)
	}
	lazyExpiredID := "pg-lazy-expired-call-" + suffix
	_, _, err = p.InviteCall(ctx, CallInvite{ID: lazyExpiredID, ConversationID: cid, CallerID: u1, CalleeID: u2, MediaType: "audio", InvitedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(-90 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	lazyExpired, err := p.GetCall(ctx, lazyExpiredID, u1, now)
	if err != nil || lazyExpired.Status != "missed" || lazyExpired.EndReason != "timeout" {
		t.Fatalf("lazy expired call=%+v err=%v", lazyExpired, err)
	}
	for _, userID := range []string{u1, u2} {
		for _, expected := range []struct {
			event  string
			callID string
		}{
			{event: "call.accepted", callID: callID},
			{event: "call.ended", callID: callID},
			{event: "call.timeout", callID: expiredID},
			{event: "call.timeout", callID: lazyExpiredID},
		} {
			var count, unsafeCount int
			if err = p.pool.QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE payload::text ILIKE '%sdp%' OR payload::text ILIKE '%candidate%')
				FROM im_wukong_outbox WHERE operation=$1 AND aggregate_id=$2 AND payload->>'event'=$3
				AND payload->'recipients' @> $4::jsonb`, wukong.OperationCallEvent, expected.callID, expected.event, fmt.Sprintf(`[%q]`, userID)).Scan(&count, &unsafeCount); err != nil || count != 1 || unsafeCount != 0 {
				t.Fatalf("durable call state user=%s event=%s call=%s count=%d unsafe=%d err=%v", userID, expected.event, expected.callID, count, unsafeCount, err)
			}
		}
	}
	for _, expected := range []struct {
		event  string
		callID string
	}{
		{event: "call.invited", callID: callID},
		{event: "call.accepted", callID: callID},
		{event: "call.ended", callID: callID},
		{event: "call.timeout", callID: expiredID},
		{event: "call.timeout", callID: lazyExpiredID},
	} {
		var count int
		var payload string
		if err = p.pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(payload::text),'') FROM im_wukong_outbox WHERE operation=$1 AND aggregate_id=$2 AND payload->>'event'=$3`, wukong.OperationCallEvent, expected.callID, expected.event).Scan(&count, &payload); err != nil || count != 1 || !strings.Contains(payload, `"schemaVersion": 1`) || !strings.Contains(payload, `"contentType": 1005`) {
			t.Fatalf("WuKong call outbox event=%s call=%s count=%d payload=%s err=%v", expected.event, expected.callID, count, payload, err)
		}
	}
	for _, expected := range []struct {
		callID, event, digest string
	}{
		{callID: callID, event: "call.ended", digest: "视频通话已结束"},
		{callID: expiredID, event: "call.timeout", digest: "语音通话未接通"},
		{callID: lazyExpiredID, event: "call.timeout", digest: "语音通话未接通"},
	} {
		var count int
		var payload string
		if err = p.pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(payload::text),'')
			FROM im_wukong_outbox WHERE operation=$1 AND aggregate_type='call_history' AND aggregate_id=$2
			AND payload->'payload'->>'type'=$3 AND payload->'payload'->>'event'=$4`,
			wukong.OperationStoredMessage, expected.callID, strconv.Itoa(wukong.ContentTypeCallEvent), expected.event,
		).Scan(&count, &payload); err != nil || count != 1 || !strings.Contains(payload, expected.digest) ||
			!strings.Contains(payload, `"channel_type": 1`) || !strings.Contains(payload, `"from_uid": "`+u1+`"`) {
			t.Fatalf("stored call event=%s call=%s count=%d payload=%s err=%v", expected.event, expected.callID, count, payload, err)
		}
	}
	items, total, _, err := p.ListAdminCalls(ctx, suffix, "", "", 10)
	if err != nil || total != 3 || len(items) != 3 {
		t.Fatalf("admin calls len=%d total=%d err=%v", len(items), total, err)
	}
}

func TestPostgresGroupCallPersistsIndependentParticipantState(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := []string{"group_call_a_" + suffix, "group_call_b_" + suffix, "group_call_c_" + suffix, "group_call_d_" + suffix}
	cid, callID := "group_call_conv_"+suffix, "pg-group-call-"+suffix
	now := time.Now().UTC()
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, users)
	})
	for _, userID := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, userID, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,created_at,updated_at) VALUES($1,'group',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	for _, userID := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'member',$3)`, cid, userID, now); err != nil {
			t.Fatal(err)
		}
	}

	call, duplicate, err := p.InviteCall(ctx, CallInvite{ID: callID, ConversationID: cid, Kind: "group", CallerID: users[0], ParticipantIDs: users, MediaType: "video", InvitedAt: now, ExpiresAt: now.Add(30 * time.Second)})
	if err != nil || duplicate || call.Kind != "group" || call.CalleeID != "" || !slices.Equal(call.ParticipantIDs, users) || !slices.Equal(call.JoinedUserIDs, users[:1]) {
		t.Fatalf("group invite call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	for _, invitee := range users[1:] {
		var pushes, commands int
		if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_push_outbox WHERE user_id=$1 AND event_type='call.invited' AND payload->>'callId'=$2`, invitee, callID).Scan(&pushes); err != nil {
			t.Fatal(err)
		}
		if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND aggregate_id=$2
			AND payload->>'event'='call.invited' AND payload->'recipients' @> $3::jsonb`, wukong.OperationCallEvent, callID, fmt.Sprintf(`[%q]`, invitee)).Scan(&commands); err != nil {
			t.Fatal(err)
		}
		if pushes != 1 || commands != 1 {
			t.Fatalf("invitee=%s pushes=%d commands=%d", invitee, pushes, commands)
		}
	}
	call, duplicate, err = p.TransitionCall(ctx, callID, users[1], "accept", "", now.Add(time.Second))
	if err != nil || duplicate || call.Status != "accepted" || !slices.Contains(call.JoinedUserIDs, users[1]) {
		t.Fatalf("group accept call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	call, duplicate, err = p.TransitionCall(ctx, callID, users[2], "accept", "", now.Add(2*time.Second))
	if err != nil || duplicate || call.Status != "accepted" || !slices.Contains(call.JoinedUserIDs, users[2]) {
		t.Fatalf("second group accept call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	call, duplicate, err = p.TransitionCall(ctx, callID, users[3], "reject", "busy", now.Add(2500*time.Millisecond))
	if err != nil || duplicate || call.Status != "accepted" || !slices.Contains(call.DeclinedUserIDs, users[3]) {
		t.Fatalf("group decline call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	call, duplicate, err = p.TransitionCall(ctx, callID, users[1], "hangup", "left", now.Add(3*time.Second))
	if err != nil || duplicate || call.Status != "accepted" || !slices.Contains(call.LeftUserIDs, users[1]) {
		t.Fatalf("group leave call=%+v duplicate=%v err=%v", call, duplicate, err)
	}
	if _, _, err = p.TransitionCall(ctx, callID, users[1], "hangup", "left", now.Add(4*time.Second)); err != nil {
		t.Fatalf("group leave retry err=%v", err)
	}
	call, _, err = p.TransitionCall(ctx, callID, users[0], "hangup", "host_left", now.Add(5*time.Second))
	if err != nil || call.Status != "ended" || call.EndedBy != users[0] {
		t.Fatalf("group end call=%+v err=%v", call, err)
	}
	for event, expectedCount := range map[string]int{"call.accepted": 2, "call.participant_declined": 1, "call.participant_left": 1, "call.ended": 1} {
		var outboxCount int
		if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND aggregate_id=$2 AND payload->>'event'=$3`, wukong.OperationCallEvent, callID, event).Scan(&outboxCount); err != nil || outboxCount != expectedCount {
			t.Fatalf("group event=%s outbox=%d want=%d err=%v", event, outboxCount, expectedCount, err)
		}
	}
	var storedCount int
	var storedPayload string
	if err = p.pool.QueryRow(ctx, `SELECT count(*),COALESCE(max(payload::text),'') FROM im_wukong_outbox
		WHERE operation=$1 AND aggregate_type='call_history' AND aggregate_id=$2
		AND payload->'payload'->>'type'=$3 AND payload->'payload'->>'event'='call.ended'`,
		wukong.OperationStoredMessage, callID, strconv.Itoa(wukong.ContentTypeCallEvent),
	).Scan(&storedCount, &storedPayload); err != nil || storedCount != 1 ||
		!strings.Contains(storedPayload, `"channel_id": "`+cid+`"`) ||
		!strings.Contains(storedPayload, `"channel_type": 2`) ||
		!strings.Contains(storedPayload, "视频通话已结束") {
		t.Fatalf("stored group call count=%d payload=%s err=%v", storedCount, storedPayload, err)
	}
}

func TestPostgresFriendStateMachineSyncPushPrivacyAndMetadata(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	u1, u2 := "friend_a_"+suffix, "friend_b_"+suffix
	now := time.Now()
	for _, uid := range []string{u1, u2} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	request := &model.FriendRequest{ID: "friend_req_" + suffix, FromUserID: u1, ToUserID: u2, Message: "private verification text", Source: "search", Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Hour), UpdatedAt: now}
	created, duplicate, err := p.CreateFriendRequest(ctx, request)
	if err != nil || duplicate || created.Source != "search" {
		t.Fatalf("create=%+v duplicate=%v err=%v", created, duplicate, err)
	}
	retry := *request
	retry.ID = "friend_retry_" + suffix
	created, duplicate, err = p.CreateFriendRequest(ctx, &retry)
	if err != nil || !duplicate || created.ID != request.ID {
		t.Fatalf("retry=%+v duplicate=%v err=%v", created, duplicate, err)
	}
	var commandA, commandB int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND payload->>'event'='friend.request.sent' AND payload->'recipients' @> $2::jsonb`, wukong.OperationBusinessEvent, fmt.Sprintf(`[%q]`, u1)).Scan(&commandA); err != nil {
		t.Fatal(err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND payload->>'event'='friend.request' AND payload->'recipients' @> $2::jsonb`, wukong.OperationBusinessEvent, fmt.Sprintf(`[%q]`, u2)).Scan(&commandB); err != nil {
		t.Fatal(err)
	}
	if commandA != 1 || commandB != 1 {
		t.Fatalf("initial CMD a=%d b=%d", commandA, commandB)
	}
	var pushText string
	if err = p.pool.QueryRow(ctx, `SELECT payload::text FROM im_push_outbox WHERE user_id=$1 AND event_type='friend.request' ORDER BY id DESC LIMIT 1`, u2).Scan(&pushText); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(pushText, "private verification") || strings.Contains(pushText, "search") {
		t.Fatalf("private push payload=%s", pushText)
	}
	if _, _, err = p.TransitionFriendRequest(ctx, request.ID, u1, "accept", now.Add(time.Second)); err != ErrForbidden {
		t.Fatalf("sender accept err=%v", err)
	}
	resolved, duplicate, err := p.TransitionFriendRequest(ctx, request.ID, u2, "reject", now.Add(time.Second))
	if err != nil || duplicate || resolved.Status != "rejected" {
		t.Fatalf("reject=%+v duplicate=%v err=%v", resolved, duplicate, err)
	}
	_, duplicate, err = p.TransitionFriendRequest(ctx, request.ID, u2, "reject", now.Add(2*time.Second))
	if err != nil || !duplicate {
		t.Fatalf("reject retry duplicate=%v err=%v", duplicate, err)
	}
	second := &model.FriendRequest{ID: "friend_accept_" + suffix, FromUserID: u1, ToUserID: u2, Message: "again", Source: "search", Status: "pending", CreatedAt: now.Add(3 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(3 * time.Second)}
	if _, _, err = p.CreateFriendRequest(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.TransitionFriendRequest(ctx, second.ID, u2, "accept", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err = p.UpdateFriendMetadata(ctx, u1, u2, FriendMetadata{Remark: "Neighbor", Tags: []string{"local", "photo"}}, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	friends, err := p.ListFriends(ctx, u1)
	if err != nil || len(friends) != 1 || friends[0].Remark != "Neighbor" || len(friends[0].Tags) != 2 {
		t.Fatalf("friends=%+v err=%v", friends, err)
	}
	if err = p.DeleteFriend(ctx, u1, u2, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	friends, err = p.ListFriends(ctx, u2)
	if err != nil || len(friends) != 0 {
		t.Fatalf("deleted friends=%+v err=%v", friends, err)
	}
	expired := &model.FriendRequest{ID: "friend_expired_" + suffix, FromUserID: u1, ToUserID: u2, Source: "qr", Status: "pending", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Hour)}
	if _, _, err = p.CreateFriendRequest(ctx, expired); err != nil {
		t.Fatal(err)
	}
	expiredItems, err := p.ExpireFriendRequests(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range expiredItems {
		if item.ID == expired.ID && item.Status == "expired" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expired missing: %+v", expiredItems)
	}
	third := &model.FriendRequest{ID: "friend_block_" + suffix, FromUserID: u1, ToUserID: u2, Source: "card", Status: "pending", CreatedAt: now.Add(7 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(7 * time.Second)}
	if _, _, err = p.CreateFriendRequest(ctx, third); err != nil {
		t.Fatal(err)
	}
	if err = p.SetFriendBlock(ctx, u2, u1, true, now.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = p.pool.QueryRow(ctx, `SELECT status FROM im_friend_requests WHERE id=$1`, third.ID).Scan(&status); err != nil || status != "cancelled" {
		t.Fatalf("blocked request status=%s err=%v", status, err)
	}
	blockedReq := *third
	blockedReq.ID = "friend_blocked_again_" + suffix
	if _, _, err = p.CreateFriendRequest(ctx, &blockedReq); err != ErrForbidden {
		t.Fatalf("blocked create err=%v", err)
	}
}

func TestWukongBusinessEventOutboxIsIdempotentAndChunksRecipients(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	aggregateID := fmt.Sprintf("business_event_contract_%d", time.Now().UnixNano())
	defer p.pool.Exec(ctx, `DELETE FROM im_wukong_outbox WHERE aggregate_id=$1`, aggregateID)
	recipients := make([]wukongCommandRecipient, 0, 1001)
	for index := 0; index < 1001; index++ {
		recipients = append(recipients, wukongCommandRecipient{
			UserID: fmt.Sprintf("business_recipient_%04d", index),
		})
	}
	raw, _ := json.Marshal(map[string]any{"messageId": "msg-1", "event": "must-not-overwrite-command"})
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	for attempt := 0; attempt < 2; attempt++ {
		if err = enqueueWukongBusinessEvent(ctx, tx, "contract", aggregateID, "message.edited", raw, recipients); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE im_wukong_outbox SET available_at=now()+interval '1 hour' WHERE aggregate_id=$1`, aggregateID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := p.pool.Query(ctx, `SELECT jsonb_array_length(payload->'recipients'),payload->>'event',payload->'param'->>'schemaVersion',payload->'param'->>'event',payload->'param'->'payload'->>'messageId',payload->'param'->'payload'->>'event' FROM im_wukong_outbox WHERE operation=$1 AND aggregate_id=$2 ORDER BY id`, wukong.OperationBusinessEvent, aggregateID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sizes []int
	for rows.Next() {
		var size int
		var event, version, paramEvent, messageID, originalEvent string
		if err = rows.Scan(&size, &event, &version, &paramEvent, &messageID, &originalEvent); err != nil {
			t.Fatal(err)
		}
		if event != "message.edited" || version != "1" || paramEvent != event || messageID != "msg-1" || originalEvent != "must-not-overwrite-command" {
			t.Fatalf("event=%s version=%s paramEvent=%s message=%s original=%s", event, version, paramEvent, messageID, originalEvent)
		}
		sizes = append(sizes, size)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(sizes) != 2 || sizes[0]+sizes[1] != 1001 || sizes[0] > wukong.MaxCommandRecipients || sizes[1] > wukong.MaxCommandRecipients {
		t.Fatalf("chunk sizes=%v", sizes)
	}
}

func TestWukongMessageRouteAndWebhookMetadataIndex(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sender, recipient := "wk_sender_"+suffix, "wk_recipient_"+suffix
	conversationID := "wk_direct_" + suffix
	now := time.Now().UTC()
	state, err := p.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	state.Users[sender] = &model.User{ID: sender, Phone: "wk_sender_phone_" + suffix, Name: "Sender", CreatedAt: now}
	state.Users[recipient] = &model.User{ID: recipient, Phone: "wk_recipient_phone_" + suffix, Name: "Recipient", CreatedAt: now}
	state.Conversations[conversationID] = &model.Conversation{ID: conversationID, Type: "direct", CreatedAt: now, UpdatedAt: now}
	state.Members[conversationID] = map[string]*model.ConversationMember{
		sender:    {ConversationID: conversationID, UserID: sender, Role: "member", JoinedAt: now},
		recipient: {ConversationID: conversationID, UserID: recipient, Role: "member", JoinedAt: now},
	}
	if err = p.Save(ctx, state); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id,created_at,updated_at) VALUES($1,$2,$3,$3),($2,$1,$3,$3) ON CONFLICT DO NOTHING`, sender, recipient, now); err != nil {
		t.Fatal(err)
	}
	eventID := "wke_test_" + suffix
	duplicateClientEventID := "wke_duplicate_client_" + suffix
	onlineEventID := "wke_online_" + suffix
	offlinePresenceEventID := "wke_offline_presence_" + suffix
	offlinePushEventID := "wke_offline_push_" + suffix
	t.Cleanup(func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_webhook_events WHERE id=ANY($1::text[])`, []string{eventID, duplicateClientEventID, onlineEventID, offlinePresenceEventID, offlinePushEventID})
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_push_outbox WHERE user_id=$1 AND payload->'message'->>'conversationId'=$2`, recipient, conversationID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_presence WHERE user_id=$1`, sender)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE sender_id=$1 AND client_msg_no=$2`, sender, "wk-client-"+suffix)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, conversationID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{sender, recipient})
	})
	route, err := p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: sender, ConversationID: conversationID, Type: "text", Text: "hello"})
	if err != nil || route.ChannelID != recipient || route.ChannelType != wukong.ChannelPerson {
		t.Fatalf("route=%+v err=%v", route, err)
	}
	channelInfo, err := p.LoadWukongChannelInfo(ctx, recipient, sender, wukong.ChannelPerson)
	if err != nil || channelInfo.Name != "Sender" || channelInfo.ChannelID != sender || channelInfo.Receipt != 1 {
		t.Fatalf("channel info=%+v err=%v", channelInfo, err)
	}
	onlineAt := now.Add(2 * time.Second)
	onlinePayload, _ := json.Marshal(sender + "-0-1-41-1-1")
	if inserted, putErr := p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{
		ID: onlineEventID, EventType: wukong.EventOnlineStatus, Payload: onlinePayload, ReceivedAt: onlineAt,
	}); putErr != nil || !inserted {
		t.Fatalf("online webhook inserted=%v err=%v", inserted, putErr)
	}
	channelInfo, err = p.LoadWukongChannelInfo(ctx, recipient, sender, wukong.ChannelPerson)
	if err != nil || channelInfo.Online != 1 || channelInfo.Version < onlineAt.UnixMicro() {
		t.Fatalf("online channel info=%+v err=%v", channelInfo, err)
	}
	offlineAt := now.Add(3 * time.Second)
	offlinePayload, _ := json.Marshal(sender + "-0-0-41-0-0")
	if inserted, putErr := p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{
		ID: offlinePresenceEventID, EventType: wukong.EventOnlineStatus, Payload: offlinePayload, ReceivedAt: offlineAt,
	}); putErr != nil || !inserted {
		t.Fatalf("offline webhook inserted=%v err=%v", inserted, putErr)
	}
	channelInfo, err = p.LoadWukongChannelInfo(ctx, recipient, sender, wukong.ChannelPerson)
	if err != nil || channelInfo.Online != 0 || channelInfo.LastOffline != offlineAt.Unix() || channelInfo.Version < offlineAt.UnixMicro() {
		t.Fatalf("offline channel info=%+v err=%v", channelInfo, err)
	}
	channelMembers, err := p.SyncWukongChannelMembers(ctx, recipient, sender, wukong.ChannelPerson, 0, 100)
	if err != nil || len(channelMembers) != 2 {
		t.Fatalf("channel members=%+v err=%v", channelMembers, err)
	}
	memberVersion := int64(0)
	for _, member := range channelMembers {
		memberVersion = max(memberVersion, member.Version)
	}
	if _, err = p.pool.Exec(ctx, `UPDATE im_users SET name='Sender updated',updated_at=now() WHERE id=$1`, sender); err != nil {
		t.Fatal(err)
	}
	channelMembers, err = p.SyncWukongChannelMembers(ctx, recipient, sender, wukong.ChannelPerson, memberVersion, 100)
	if err != nil || len(channelMembers) != 1 || channelMembers[0].UserID != sender || channelMembers[0].Name != "Sender updated" || channelMembers[0].Version <= memberVersion {
		t.Fatalf("channel member delta=%+v err=%v", channelMembers, err)
	}
	if err = p.HideConversation(ctx, recipient, conversationID); err != nil {
		t.Fatal(err)
	}
	if hidden, listErr := p.ListConversations(ctx, recipient, 10); listErr != nil || len(hidden) != 0 {
		t.Fatalf("conversation was not hidden: items=%v err=%v", hidden, listErr)
	}

	messageID := time.Now().UnixNano()
	// PostgreSQL timestamptz stores microsecond precision, so construct the
	// expected portable deadline at the same precision before round-tripping it.
	portableExpiry := now.Add(5 * time.Minute).Truncate(time.Microsecond)
	notification := wukongMessageNotification{
		MessageID: messageID, MessageIDStr: strconv.FormatInt(messageID, 10),
		ClientMsgNo: "wk-client-" + suffix, MessageSeq: 9,
		FromUID: sender, ChannelID: recipient, ChannelType: wukong.ChannelPerson,
		Timestamp: now.Unix(), Payload: []byte(fmt.Sprintf(`{"type":1,"content":"hello","expiresAt":%q}`, portableExpiry.Format(time.RFC3339Nano))),
	}
	notification.Header.RedDot = 1
	raw, err := json.Marshal(notification)
	if err != nil {
		t.Fatal(err)
	}
	event := wukong.WebhookEvent{ID: eventID, EventType: wukong.EventMessageNotify, Payload: raw, ReceivedAt: now}
	inserted, err := p.PutWukongWebhookEvent(ctx, event)
	if err != nil || !inserted {
		t.Fatalf("first webhook inserted=%v err=%v", inserted, err)
	}
	inserted, err = p.PutWukongWebhookEvent(ctx, event)
	if err != nil || inserted {
		t.Fatalf("duplicate webhook inserted=%v err=%v", inserted, err)
	}
	var indexedConversation, indexedSender, status string
	var storedWebhookPayload []byte
	var indexedSeq int64
	var indexedExpiry *time.Time
	if err = p.pool.QueryRow(ctx, `
		SELECT i.conversation_id,i.sender_id,i.message_seq,i.expires_at,e.status,e.payload
		FROM im_wukong_message_index i JOIN im_wukong_webhook_events e ON e.id=$2
		WHERE i.message_id=$1
	`, messageID, event.ID).Scan(&indexedConversation, &indexedSender, &indexedSeq, &indexedExpiry, &status, &storedWebhookPayload); err != nil {
		t.Fatal(err)
	}
	if indexedConversation != conversationID || indexedSender != sender || indexedSeq != 9 || indexedExpiry == nil || !indexedExpiry.Equal(portableExpiry) || status != "completed" || string(storedWebhookPayload) != "{}" {
		t.Fatalf("conversation=%s sender=%s seq=%d expiry=%v status=%s webhookPayload=%s", indexedConversation, indexedSender, indexedSeq, indexedExpiry, status, storedWebhookPayload)
	}
	offlineNotification := wukongOfflineNotification{wukongMessageNotification: notification, ToUIDs: []string{recipient}}
	offlineRaw, marshalErr := json.Marshal(offlineNotification)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	inserted, err = p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{
		ID: offlinePushEventID, EventType: wukong.EventMessageOffline, Payload: offlineRaw, ReceivedAt: now,
	})
	if err != nil || !inserted {
		t.Fatalf("offline push webhook inserted=%v err=%v", inserted, err)
	}
	var pushPayload, offlineStoredPayload []byte
	if err = p.pool.QueryRow(ctx, `SELECT payload FROM im_push_outbox WHERE user_id=$1 AND event_type='message.created' AND payload->'message'->>'id'=$2 ORDER BY id DESC LIMIT 1`, recipient, notification.MessageIDStr).Scan(&pushPayload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(pushPayload), "hello") || !strings.Contains(string(pushPayload), conversationID) || !strings.Contains(string(pushPayload), notification.MessageIDStr) {
		t.Fatalf("offline push payload is unsafe or incomplete: %s", pushPayload)
	}
	if err = p.pool.QueryRow(ctx, `SELECT payload FROM im_wukong_webhook_events WHERE id=$1`, offlinePushEventID).Scan(&offlineStoredPayload); err != nil || string(offlineStoredPayload) != "{}" {
		t.Fatalf("offline webhook payload=%s err=%v", offlineStoredPayload, err)
	}
	duplicateClientNotification := notification
	duplicateClientNotification.MessageID++
	duplicateClientNotification.MessageIDStr = strconv.FormatInt(duplicateClientNotification.MessageID, 10)
	duplicateClientNotification.MessageSeq++
	duplicateClientNotification.Payload = []byte(`{"type":1,"content":"must not replace canonical message"}`)
	duplicateClientRaw, marshalErr := json.Marshal(duplicateClientNotification)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	inserted, err = p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{
		ID: duplicateClientEventID, EventType: wukong.EventMessageNotify, Payload: duplicateClientRaw, ReceivedAt: now,
	})
	if err != nil || !inserted {
		t.Fatalf("duplicate client webhook inserted=%v err=%v", inserted, err)
	}
	var canonicalCount int
	var duplicateStatus string
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_message_index WHERE sender_id=$1 AND client_msg_no=$2`, sender, notification.ClientMsgNo).Scan(&canonicalCount); err != nil || canonicalCount != 1 {
		t.Fatalf("canonical message count=%d err=%v", canonicalCount, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT status FROM im_wukong_webhook_events WHERE id=$1`, duplicateClientEventID).Scan(&duplicateStatus); err != nil || duplicateStatus != "completed" {
		t.Fatalf("duplicate webhook status=%s err=%v", duplicateStatus, err)
	}
	conversations, err := p.ListConversations(ctx, recipient, 10)
	if err != nil || len(conversations) != 1 {
		t.Fatalf("WuKong conversation metadata=%v err=%v", conversations, err)
	}
	conversation := conversations[0]["conversation"].(*model.Conversation)
	lastMessage, _ := conversations[0]["lastMessage"].(*model.Message)
	if conversation.LastMessageSeq != 9 || conversation.Seq != 9 || lastMessage != nil || conversations[0]["unreadCount"] != int64(9) {
		t.Fatalf("WuKong conversation projection=%#v", conversations[0])
	}
	refs, err := p.ListWukongForwardMessageRefs(ctx, recipient, []string{strconv.FormatInt(messageID, 10)})
	if err != nil || len(refs) != 1 || refs[0].ConversationID != conversationID || refs[0].ChannelID != sender || refs[0].ChannelType != wukong.ChannelPerson {
		t.Fatalf("recipient forward refs=%+v err=%v", refs, err)
	}
	messageIDText := strconv.FormatInt(messageID, 10)
	reaction, duplicate, err := p.SetMessageReaction(ctx, recipient, messageIDText, "👍", true, now.Add(time.Second))
	if err != nil || duplicate || reaction.Count != 1 || !reaction.ReactedByMe {
		t.Fatalf("WuKong reaction=%+v duplicate=%v err=%v", reaction, duplicate, err)
	}
	extensions, err := p.LoadWukongMessageExtensions(ctx, recipient, []string{messageIDText})
	if err != nil {
		t.Fatal(err)
	}
	reactions, _ := extensions[messageIDText]["reactions"].([]map[string]any)
	if len(reactions) != 1 || reactions[0]["emoji"] != "👍" || reactions[0]["count"] != 1 || reactions[0]["reactedByMe"] != true {
		t.Fatalf("WuKong extensions=%#v", extensions[messageIDText])
	}
	edited, duplicate, err := p.EditMessage(ctx, sender, messageIDText, "wk-edit-"+suffix, map[string]any{"text": "edited hello"}, map[string]any{"text": "hello"}, now.Add(2*time.Second), 2*time.Minute)
	if err != nil || duplicate || edited.EditVersion != 1 || edited.EditedAt == nil || edited.Body["text"] != "edited hello" {
		t.Fatalf("WuKong edit=%+v duplicate=%v err=%v", edited, duplicate, err)
	}
	if _, duplicate, err = p.EditMessage(ctx, sender, messageIDText, "wk-edit-"+suffix, map[string]any{"text": "edited hello"}, map[string]any{"text": "edited hello"}, now.Add(3*time.Second), 2*time.Minute); err != nil || !duplicate {
		t.Fatalf("WuKong edit retry duplicate=%v err=%v", duplicate, err)
	}
	if _, _, err = p.EditMessage(ctx, recipient, messageIDText, "wk-edit-other-"+suffix, map[string]any{"text": "hijack"}, map[string]any{"text": "edited hello"}, now.Add(3*time.Second), 2*time.Minute); err != ErrForbidden {
		t.Fatalf("WuKong non-author edit err=%v", err)
	}
	edits, err := p.ListMessageEdits(ctx, recipient, messageIDText)
	if err != nil || len(edits) != 2 || edits[0].Version != 0 || edits[0].Body["text"] != "hello" || edits[1].Version != 1 || edits[1].Body["text"] != "edited hello" {
		t.Fatalf("WuKong edits=%+v err=%v", edits, err)
	}
	extensions, err = p.LoadWukongMessageExtensions(ctx, recipient, []string{messageIDText})
	editedBody, _ := extensions[messageIDText]["editedBody"].(map[string]any)
	if err != nil || editedBody["text"] != "edited hello" || extensionInt(extensions[messageIDText], "editVersion") != 1 {
		t.Fatalf("edited extensions=%#v err=%v", extensions[messageIDText], err)
	}
	extras, err := p.SyncWukongMessageExtras(ctx, recipient, sender, wukong.ChannelPerson, 0, 100)
	if err != nil || len(extras) != 1 || extras[0].MessageID != messageIDText || extras[0].EditedBody["text"] != "edited hello" || extras[0].SyncVersion <= 0 {
		t.Fatalf("message extra sync=%+v err=%v", extras, err)
	}
	extraVersion := extras[0].SyncVersion
	recalledCID, recalledSeq, recipients, err := p.RecallAuthorized(ctx, sender, messageIDText, now.Add(4*time.Second), 2*time.Minute)
	if err != nil || recalledCID != conversationID || recalledSeq != 9 || len(recipients) != 2 {
		t.Fatalf("WuKong recall cid=%s seq=%d recipients=%v err=%v", recalledCID, recalledSeq, recipients, err)
	}
	extensions, err = p.LoadWukongMessageExtensions(ctx, recipient, []string{messageIDText})
	if err != nil || extensions[messageIDText]["recalledAt"] == nil {
		t.Fatalf("recalled extensions=%#v err=%v", extensions[messageIDText], err)
	}
	extras, err = p.SyncWukongMessageExtras(ctx, recipient, sender, wukong.ChannelPerson, extraVersion, 100)
	if err != nil || len(extras) != 1 || !extras[0].Recalled || extras[0].Revoker != sender || extras[0].SyncVersion <= extraVersion {
		t.Fatalf("recalled message extra sync=%+v err=%v", extras, err)
	}
	if _, _, err = p.SetMessageReaction(ctx, recipient, messageIDText, "👍", false, now.Add(5*time.Second)); err != ErrForbidden {
		t.Fatalf("reaction after recall err=%v", err)
	}
	var legacyTable *string
	if err = p.pool.QueryRow(ctx, `SELECT to_regclass('im_messages')::text`).Scan(&legacyTable); err != nil || legacyTable != nil {
		t.Fatalf("legacy message table=%v err=%v", legacyTable, err)
	}
}

func TestWukongMentionRemindersSyncAndCompleteByVersion(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	sender := "wk_reminder_sender_" + suffix
	mentioned := "wk_reminder_mentioned_" + suffix
	other := "wk_reminder_other_" + suffix
	groupID := "wk_reminder_group_" + suffix
	now := time.Now().UTC()
	if _, err = p.pool.Exec(ctx, `
		INSERT INTO im_users(id,phone,name,created_at) VALUES
			($1,$2,'Sender',$7),($3,$4,'Mentioned',$7),($5,$6,'Other',$7)
	`, sender, "wk_reminder_sender_phone_"+suffix, mentioned, "wk_reminder_mentioned_phone_"+suffix,
		other, "wk_reminder_other_phone_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group','Reminder group',$2,$2)`, groupID, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at) VALUES($1,$2,$3)`, groupID, sender, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `
		INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES
			($1,$2,'owner',$5),($1,$3,'member',$5),($1,$4,'member',$5)
	`, groupID, sender, mentioned, other, now); err != nil {
		t.Fatal(err)
	}
	messageID := time.Now().UnixNano()
	allMessageID := messageID + 1
	eventIDs := []string{"wke_reminder_mention_" + suffix, "wke_reminder_all_" + suffix}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_outbox WHERE aggregate_type='reminder' AND payload->'recipients' ?| $1::text[]`, []string{mentioned, other})
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_webhook_events WHERE id=ANY($1::text[])`, eventIDs)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE message_id=ANY($1::bigint[])`, []int64{messageID, allMessageID})
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, groupID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{sender, mentioned, other})
	})
	put := func(id string, messageID int64, sequence uint64, payload string) {
		notification := wukongMessageNotification{
			MessageID: messageID, MessageIDStr: strconv.FormatInt(messageID, 10),
			ClientMsgNo: id + "_client", MessageSeq: sequence, FromUID: sender,
			ChannelID: groupID, ChannelType: wukong.ChannelGroup,
			Timestamp: now.Unix(), Payload: []byte(payload),
		}
		raw, marshalErr := json.Marshal(notification)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		inserted, putErr := p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{
			ID: id, EventType: wukong.EventMessageNotify, Payload: raw, ReceivedAt: now,
		})
		if putErr != nil || !inserted {
			t.Fatalf("put reminder webhook inserted=%v err=%v", inserted, putErr)
		}
	}
	put(eventIDs[0], messageID, 1, fmt.Sprintf(`{"type":1,"content":"hello","mention":{"uids":[%q,%q,%q]}}`, mentioned, mentioned, "not-a-member"))
	items, err := p.SyncWukongReminders(ctx, mentioned, 0, 100)
	if err != nil || len(items) != 1 {
		t.Fatalf("mentioned reminders=%+v err=%v", items, err)
	}
	item := items[0]
	if item.MessageID != strconv.FormatInt(messageID, 10) || item.MessageSeq != 1 || item.ChannelID != groupID ||
		item.ChannelType != wukong.ChannelGroup || item.Type != 1 || item.IsLocate != 1 || item.Done != 0 ||
		item.Publisher != sender || item.Text != "[有人@你]" || item.Version <= 0 {
		t.Fatalf("unexpected mention reminder=%+v", item)
	}
	if otherItems, syncErr := p.SyncWukongReminders(ctx, other, 0, 100); syncErr != nil || len(otherItems) != 0 {
		t.Fatalf("non-mentioned reminders=%+v err=%v", otherItems, syncErr)
	}
	mentionedConversations, listErr := p.ListConversations(ctx, mentioned, 10)
	if listErr != nil || len(mentionedConversations) != 1 || mentionedConversations[0]["mentionUnreadCount"] != int64(1) {
		t.Fatalf("mentioned conversation count=%+v err=%v", mentionedConversations, listErr)
	}
	var commandCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox
		WHERE operation=$1 AND aggregate_type='reminder' AND aggregate_id=$2
		  AND payload->>'event'='reminder.updated' AND payload->'recipients' @> $3::jsonb`,
		wukong.OperationBusinessEvent, strconv.FormatInt(messageID, 10), fmt.Sprintf(`[%q]`, mentioned)).Scan(&commandCount); err != nil || commandCount != 1 {
		t.Fatalf("reminder updated command count=%d err=%v", commandCount, err)
	}
	if err = p.DoneWukongReminders(ctx, other, []int64{item.ID}); err != ErrForbidden {
		t.Fatalf("foreign reminder completion err=%v", err)
	}
	previousVersion := item.Version
	if err = p.DoneWukongReminders(ctx, mentioned, []int64{item.ID, item.ID}); err != nil {
		t.Fatal(err)
	}
	items, err = p.SyncWukongReminders(ctx, mentioned, previousVersion, 100)
	if err != nil || len(items) != 1 || items[0].ID != item.ID || items[0].Done != 1 || items[0].Version <= previousVersion {
		t.Fatalf("completed reminder delta=%+v err=%v", items, err)
	}
	completedVersion := items[0].Version
	if err = p.DoneWukongReminders(ctx, mentioned, []int64{item.ID}); err != nil {
		t.Fatal(err)
	}
	if items, err = p.SyncWukongReminders(ctx, mentioned, completedVersion, 100); err != nil || len(items) != 0 {
		t.Fatalf("idempotent completion emitted delta=%+v err=%v", items, err)
	}
	mentionedConversations, listErr = p.ListConversations(ctx, mentioned, 10)
	if listErr != nil || len(mentionedConversations) != 1 || mentionedConversations[0]["mentionUnreadCount"] != int64(0) {
		t.Fatalf("completed conversation count=%+v err=%v", mentionedConversations, listErr)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox
		WHERE operation=$1 AND aggregate_type='reminder' AND payload->>'event'='reminder.done'
		  AND payload->'recipients' @> $2::jsonb`, wukong.OperationBusinessEvent, fmt.Sprintf(`[%q]`, mentioned)).Scan(&commandCount); err != nil || commandCount != 1 {
		t.Fatalf("reminder done command count=%d err=%v", commandCount, err)
	}

	put(eventIDs[1], allMessageID, 2, `{"type":1,"content":"everyone","mention":{"all":1,"uids":[]}}`)
	for _, userID := range []string{mentioned, other} {
		allItems, syncErr := p.SyncWukongReminders(ctx, userID, completedVersion, 100)
		if syncErr != nil || len(allItems) != 1 || allItems[0].MessageID != strconv.FormatInt(allMessageID, 10) ||
			allItems[0].Text != "[有人@所有人]" || allItems[0].Publisher != sender {
			t.Fatalf("mention-all reminder for %s=%+v err=%v", userID, allItems, syncErr)
		}
		conversations, listErr := p.ListConversations(ctx, userID, 10)
		if listErr != nil || len(conversations) != 1 || conversations[0]["mentionUnreadCount"] != int64(1) {
			t.Fatalf("mention-all conversation for %s=%+v err=%v", userID, conversations, listErr)
		}
	}
	if senderItems, syncErr := p.SyncWukongReminders(ctx, sender, 0, 100); syncErr != nil || len(senderItems) != 0 {
		t.Fatalf("sender reminders=%+v err=%v", senderItems, syncErr)
	}
}

func TestBusinessChannelsProvisionMembershipPolicyAndWebhookIndex(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, member, later := "channel_owner_"+suffix, "channel_member_"+suffix, "channel_later_"+suffix
	communityID := "community_" + suffix
	topicID := communityID + "@topic_" + suffix
	infoID := "info_" + suffix
	liveID := "live_" + suffix
	now := time.Now().UTC().Truncate(time.Microsecond)
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES
		($1,$2,'Channel Owner',$7),($3,$4,'Channel Member',$7),($5,$6,'Later Member',$7)`,
		owner, "channel_owner_phone_"+suffix, member, "channel_member_phone_"+suffix,
		later, "channel_later_phone_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_outbox WHERE aggregate_id LIKE '%'||$1||'%'`, suffix)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_webhook_events WHERE id=$1`, "wke_channel_"+suffix)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE client_msg_no=$1`, "channel-client-"+suffix)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=ANY($1::text[])`, []string{topicID, communityID, infoID, liveID})
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{owner, member, later})
	})

	community, err := p.CreateBusinessChannel(ctx, BusinessChannelCreate{
		ID: communityID, ActorID: owner, ChannelType: int(wukong.ChannelCommunity), Name: "Test community " + suffix,
		Visibility: "public", JoinPolicy: "open", PostingPolicy: "members", Metadata: map[string]any{"color": "blue"},
	}, now)
	if err != nil || community.ID != communityID || community.Role != "owner" || community.MemberCount != 1 {
		t.Fatalf("community=%+v err=%v", community, err)
	}
	if err = p.ApplyBusinessChannelMemberAction(ctx, BusinessChannelMemberAction{
		ActorID: member, TargetID: member, ChannelID: communityID, ChannelType: int(wukong.ChannelCommunity), Action: "subscribe", At: now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	topic, err := p.CreateBusinessChannel(ctx, BusinessChannelCreate{
		ID: topicID, ActorID: owner, ChannelType: int(wukong.ChannelCommunityTopic), ParentID: communityID,
		Name: "Announcements " + suffix, Visibility: "public", JoinPolicy: "open", PostingPolicy: "members", Metadata: map[string]any{},
	}, now.Add(2*time.Second))
	if err != nil || topic.ParentID != communityID || topic.MemberCount != 2 {
		t.Fatalf("topic=%+v err=%v", topic, err)
	}
	if _, err = p.CreateBusinessChannel(ctx, BusinessChannelCreate{
		ID: "wrong@topic", ActorID: owner, ChannelType: int(wukong.ChannelCommunityTopic), ParentID: communityID,
		Name: "Invalid topic", Visibility: "public", JoinPolicy: "open", PostingPolicy: "members", Metadata: map[string]any{},
	}, now); err != ErrConflict {
		t.Fatalf("invalid topic id err=%v", err)
	}
	if err = p.ApplyBusinessChannelMemberAction(ctx, BusinessChannelMemberAction{
		ActorID: later, TargetID: later, ChannelID: communityID, ChannelType: int(wukong.ChannelCommunity), Action: "subscribe", At: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	topicMembers, _, err := p.ListBusinessChannelMembers(ctx, owner, topicID, int(wukong.ChannelCommunityTopic), "", 100)
	if err != nil || len(topicMembers) != 3 {
		t.Fatalf("topic members=%+v err=%v", topicMembers, err)
	}
	topicSnapshot, err := p.LoadWukongChannelSnapshot(ctx, topicID, wukong.ChannelCommunityTopic)
	if err != nil || topicSnapshot.ChannelType != wukong.ChannelCommunityTopic || len(topicSnapshot.Subscribers) != 3 || topicSnapshot.Cursor != "05:"+topicID {
		t.Fatalf("topic snapshot=%+v err=%v", topicSnapshot, err)
	}
	channelInfo, err := p.LoadWukongChannelInfo(ctx, member, topicID, wukong.ChannelCommunityTopic)
	if err != nil || channelInfo.Category != "community_topic" || channelInfo.Extra["parentChannelId"] != communityID || channelInfo.Extra["memberCount"] != 3 {
		t.Fatalf("topic channel info=%+v err=%v", channelInfo, err)
	}

	info, err := p.CreateBusinessChannel(ctx, BusinessChannelCreate{
		ID: infoID, ActorID: owner, ChannelType: int(wukong.ChannelInfo), Name: "Official news " + suffix,
		Visibility: "public", JoinPolicy: "open", PostingPolicy: "operators", Metadata: map[string]any{},
	}, now.Add(4*time.Second))
	if err != nil || info.PostingPolicy != "operators" {
		t.Fatalf("info=%+v err=%v", info, err)
	}
	if err = p.ApplyBusinessChannelMemberAction(ctx, BusinessChannelMemberAction{
		ActorID: member, TargetID: member, ChannelID: infoID, ChannelType: int(wukong.ChannelInfo), Action: "subscribe", At: now.Add(5 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err = p.AuthorizeBusinessChannelSend(ctx, member, infoID, int(wukong.ChannelInfo), now.Add(6*time.Second)); err != ErrForbidden {
		t.Fatalf("ordinary info publisher err=%v", err)
	}
	if err = p.ApplyBusinessChannelMemberAction(ctx, BusinessChannelMemberAction{
		ActorID: owner, TargetID: member, ChannelID: infoID, ChannelType: int(wukong.ChannelInfo), Action: "role", Role: "admin", At: now.Add(7 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err = p.AuthorizeBusinessChannelSend(ctx, member, infoID, int(wukong.ChannelInfo), now.Add(8*time.Second)); err != nil {
		t.Fatalf("info operator send err=%v", err)
	}

	live, err := p.CreateBusinessChannel(ctx, BusinessChannelCreate{
		ID: liveID, ActorID: owner, ChannelType: int(wukong.ChannelLive), Name: "Live room " + suffix,
		Visibility: "public", JoinPolicy: "open", PostingPolicy: "members", SlowModeSeconds: 10, Metadata: map[string]any{"mode": "interactive"},
	}, now.Add(9*time.Second))
	if err != nil || live.SlowModeSeconds != 10 {
		t.Fatalf("live=%+v err=%v", live, err)
	}
	if err = p.ApplyBusinessChannelMemberAction(ctx, BusinessChannelMemberAction{
		ActorID: member, TargetID: member, ChannelID: liveID, ChannelType: int(wukong.ChannelLive), Action: "subscribe", At: now.Add(10 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err = p.AuthorizeBusinessChannelSend(ctx, member, liveID, int(wukong.ChannelLive), now.Add(11*time.Second)); err != nil {
		t.Fatalf("first live send err=%v", err)
	}
	if err = p.AuthorizeBusinessChannelSend(ctx, member, liveID, int(wukong.ChannelLive), now.Add(12*time.Second)); err != ErrForbidden {
		t.Fatalf("slow-mode immediate retry err=%v", err)
	}
	if err = p.AuthorizeBusinessChannelSend(ctx, member, liveID, int(wukong.ChannelLive), now.Add(22*time.Second)); err != nil {
		t.Fatalf("slow-mode later send err=%v", err)
	}
	if err = p.ApplyBusinessChannelAccess(ctx, BusinessChannelAccessAction{
		ActorID: owner, TargetID: member, ChannelID: liveID, ChannelType: int(wukong.ChannelLive), AccessType: "deny", Enabled: true, Reason: "test", At: now.Add(23 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err = p.AuthorizeBusinessChannelSend(ctx, member, liveID, int(wukong.ChannelLive), now.Add(40*time.Second)); err != ErrForbidden {
		t.Fatalf("denied live send err=%v", err)
	}
	liveSnapshot, err := p.LoadWukongChannelSnapshot(ctx, liveID, wukong.ChannelLive)
	if err != nil || len(liveSnapshot.Denylist) != 1 || liveSnapshot.Denylist[0] != member {
		t.Fatalf("live snapshot=%+v err=%v", liveSnapshot, err)
	}
	temporaryExpiry := now.Add(30 * time.Second)
	if err = p.ApplyBusinessChannelMemberAction(ctx, BusinessChannelMemberAction{
		ActorID: owner, TargetID: later, ChannelID: liveID, ChannelType: int(wukong.ChannelLive),
		Action: "add", ExpiresAt: &temporaryExpiry, At: now.Add(24 * time.Second),
	}); err != nil {
		t.Fatalf("add temporary live subscriber: %v", err)
	}
	temporaryMembers, _, err := p.ListBusinessChannelMembers(ctx, owner, liveID, int(wukong.ChannelLive), "", 100)
	if err != nil {
		t.Fatal(err)
	}
	foundTemporary := false
	for _, item := range temporaryMembers {
		foundTemporary = foundTemporary || item.UserID == later && item.ExpiresAt != nil && item.ExpiresAt.Equal(temporaryExpiry)
	}
	if !foundTemporary {
		t.Fatalf("temporary subscriber missing expiry: %+v", temporaryMembers)
	}
	removed, err := p.ExpireBusinessChannelMemberships(ctx, temporaryExpiry.Add(time.Second), 200)
	if err != nil || removed < 1 {
		t.Fatalf("expire temporary subscribers removed=%d err=%v", removed, err)
	}
	if _, err = p.ResolveWukongChannel(ctx, later, liveID); err != ErrForbidden {
		t.Fatalf("expired subscriber still resolves live channel: %v", err)
	}
	var expiryCommands int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox
		WHERE operation=$1 AND aggregate_type='business_channel' AND aggregate_id=$2
		AND payload->>'event'='channel.subscription.expired' AND payload->'recipients' @> $3::jsonb`,
		wukong.OperationBusinessEvent, liveID, fmt.Sprintf(`[%q]`, later)).Scan(&expiryCommands); err != nil || expiryCommands != 1 {
		t.Fatalf("subscription expiry CMD count=%d err=%v", expiryCommands, err)
	}
	liveSnapshot, err = p.LoadWukongChannelSnapshot(ctx, liveID, wukong.ChannelLive)
	if err != nil || slices.Contains(liveSnapshot.Subscribers, later) {
		t.Fatalf("expired subscriber remained in WuKong snapshot=%+v err=%v", liveSnapshot, err)
	}

	messageID := time.Now().UnixNano()
	notification := wukongMessageNotification{
		MessageID: messageID, MessageIDStr: strconv.FormatInt(messageID, 10), ClientMsgNo: "channel-client-" + suffix,
		MessageSeq: 1, FromUID: owner, ChannelID: liveID, ChannelType: wukong.ChannelLive,
		Timestamp: now.Unix(), Payload: []byte(`{"type":1,"content":"live hello"}`),
	}
	raw, err := json.Marshal(notification)
	if err != nil {
		t.Fatal(err)
	}
	inserted, err := p.PutWukongWebhookEvent(ctx, wukong.WebhookEvent{
		ID: "wke_channel_" + suffix, EventType: wukong.EventMessageNotify, Payload: raw, ReceivedAt: now,
	})
	if err != nil || !inserted {
		t.Fatalf("channel webhook inserted=%v err=%v", inserted, err)
	}
	var indexedConversation string
	if err = p.pool.QueryRow(ctx, `SELECT conversation_id FROM im_wukong_message_index WHERE message_id=$1`, messageID).Scan(&indexedConversation); err != nil || indexedConversation != liveID {
		t.Fatalf("indexed conversation=%s err=%v", indexedConversation, err)
	}
	foundListed := map[string]bool{}
	after := ""
	for page := 0; page < 100; page++ {
		channels, next, listErr := p.ListBusinessChannels(ctx, member, "", "", 0, after, 100)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, channel := range channels {
			if channel.ID == communityID || channel.ID == topicID || channel.ID == infoID || channel.ID == liveID {
				foundListed[channel.ID] = true
			}
		}
		if next == "" {
			break
		}
		after = next
	}
	if !foundListed[communityID] || !foundListed[topicID] || !foundListed[infoID] || foundListed[liveID] {
		t.Fatalf("business channel pagination/deny visibility mismatch: %v", foundListed)
	}
	foundCommunity, foundTopic, foundInfo, foundLive := false, false, false, false
	after = ""
	for page := 0; page < 100; page++ {
		allSnapshots, listErr := p.ListWukongChannels(ctx, after, 500)
		if listErr != nil {
			t.Fatal(listErr)
		}
		for _, snapshot := range allSnapshots {
			switch snapshot.ChannelID {
			case communityID:
				foundCommunity = snapshot.ChannelType == wukong.ChannelCommunity
			case topicID:
				foundTopic = snapshot.ChannelType == wukong.ChannelCommunityTopic
			case infoID:
				foundInfo = snapshot.ChannelType == wukong.ChannelInfo
			case liveID:
				foundLive = snapshot.ChannelType == wukong.ChannelLive
			}
		}
		if len(allSnapshots) < 500 {
			break
		}
		after = allSnapshots[len(allSnapshots)-1].Cursor
	}
	if !foundCommunity || !foundTopic || !foundInfo || !foundLive {
		t.Fatalf("reconcile snapshots missing community=%v topic=%v info=%v live=%v", foundCommunity, foundTopic, foundInfo, foundLive)
	}
}

func TestWukongGroupMessagePinUsesMetadataAndExtensionOnly(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, member, cid := "wk_pin_owner_"+suffix, "wk_pin_member_"+suffix, "wk_pin_group_"+suffix
	messageID := time.Now().UnixNano()
	messageIDText := strconv.FormatInt(messageID, 10)
	now := time.Now().UTC()
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,'Owner',$5),($3,$4,'Member',$5)`, owner, "pin_owner_phone_"+suffix, member, "pin_member_phone_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group','Pinned group',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at) VALUES($1,$2,$3)`, cid, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,'owner',$4),($1,$3,'member',$4)`, cid, owner, member, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `
		INSERT INTO im_wukong_message_index(message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,content_type,payload_sha256,message_timestamp,indexed_at)
		VALUES($1,$2,$3,$4,$3,$5,7,$6,$7,$8,$8)
	`, messageID, "wk-pin-client-"+suffix, cid, member, wukong.ChannelGroup, wukong.ContentTypeText, strings.Repeat("a", 64), now); err != nil {
		t.Fatal(err)
	}
	adminMessages, total, _, err := p.ListAdminMessages(ctx, messageIDText, "text", "", 10)
	if err != nil || total != 1 || len(adminMessages) != 1 || adminMessages[0].ID != messageIDText || adminMessages[0].Body["text"] != nil {
		t.Fatalf("WuKong admin messages=%+v total=%d err=%v", adminMessages, total, err)
	}
	groupOverview, err := p.AdminGroupOverview(ctx, cid)
	if err != nil || groupOverview["messageCount"] != int64(7) {
		t.Fatalf("WuKong group overview=%#v err=%v", groupOverview, err)
	}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_reports WHERE target_id=$1`, messageIDText)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_audits WHERE target_id=$1 OR actor_id=ANY($2::text[])`, messageIDText, []string{owner, member})
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE message_id=$1`, messageID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{owner, member})
	})
	if _, _, err = p.SetGroupMessagePin(ctx, member, cid, messageIDText, true, now.Add(time.Second)); err != ErrForbidden {
		t.Fatalf("member pin err=%v", err)
	}
	pin, duplicate, err := p.SetGroupMessagePin(ctx, owner, cid, messageIDText, true, now.Add(2*time.Second))
	if err != nil || duplicate || pin.Message.ID != messageIDText || pin.Message.Body != nil {
		t.Fatalf("pin=%+v duplicate=%v err=%v", pin, duplicate, err)
	}
	if _, duplicate, err = p.SetGroupMessagePin(ctx, owner, cid, messageIDText, true, now.Add(3*time.Second)); err != nil || !duplicate {
		t.Fatalf("pin retry duplicate=%v err=%v", duplicate, err)
	}
	items, err := p.ListGroupMessagePins(ctx, member, cid, 0, 50)
	if err != nil || len(items) != 1 || items[0].Message.ID != messageIDText || items[0].Message.Body != nil {
		t.Fatalf("pins=%+v err=%v", items, err)
	}
	extensions, err := p.LoadWukongMessageExtensions(ctx, member, []string{messageIDText})
	if err != nil || extensions[messageIDText]["isPinned"] != true || extensions[messageIDText]["pinnedBy"] != owner {
		t.Fatalf("pin extension=%#v err=%v", extensions[messageIDText], err)
	}
	if _, duplicate, err = p.SetGroupMessagePin(ctx, owner, cid, messageIDText, false, now.Add(4*time.Second)); err != nil || duplicate {
		t.Fatalf("unpin duplicate=%v err=%v", duplicate, err)
	}
	items, err = p.ListGroupMessagePins(ctx, member, cid, 0, 50)
	if err != nil || len(items) != 0 {
		t.Fatalf("pins after unpin=%+v err=%v", items, err)
	}
	extensions, err = p.LoadWukongMessageExtensions(ctx, member, []string{messageIDText})
	if err != nil || extensions[messageIDText]["isPinned"] != nil || extensions[messageIDText]["pinnedAt"] != nil {
		t.Fatalf("unpin extension=%#v err=%v", extensions[messageIDText], err)
	}
	if err = p.SetFavorite(ctx, member, messageIDText, true); err != nil {
		t.Fatalf("favorite WuKong message: %v", err)
	}
	favorites, err := p.ListFavorites(ctx, member, 50)
	if err != nil || len(favorites) != 1 || favorites[0].ID != messageIDText || favorites[0].Body != nil {
		t.Fatalf("favorites=%+v err=%v", favorites, err)
	}
	deliveredSequence, _, err := p.MarkDelivered(ctx, member, cid, 99, now.Add(5*time.Second))
	if err != nil || deliveredSequence != 7 {
		t.Fatalf("WuKong delivered sequence=%d err=%v", deliveredSequence, err)
	}
	readSequence, _, err := p.MarkRead(ctx, member, cid, 99, now.Add(6*time.Second))
	if err != nil || readSequence != 7 {
		t.Fatalf("WuKong read sequence=%d err=%v", readSequence, err)
	}
	reportID := "wk_pin_report_" + suffix
	report := &model.Report{ID: reportID, ReporterID: member, TargetType: "message", TargetID: messageIDText, Reason: "abuse", Status: "pending", CreatedAt: now, UpdatedAt: now}
	audit := &model.AuditEntry{ID: "wk_pin_report_create_" + suffix, ActorID: member, Action: "report.created", TargetType: "message", TargetID: messageIDText, Metadata: map[string]any{"reportId": reportID}, CreatedAt: now}
	if err = p.CreateReportRecord(ctx, report, audit); err != nil {
		t.Fatalf("report WuKong message: %v", err)
	}
	status, err := p.ResolveReportRecord(ctx, owner, reportID, "delete_message", "confirmed", "wk_pin_report_resolve_"+suffix, now.Add(7*time.Second))
	if err != nil || status != "resolved" {
		t.Fatalf("moderate WuKong message status=%s err=%v", status, err)
	}
	extensions, err = p.LoadWukongMessageExtensions(ctx, member, []string{messageIDText})
	if err != nil || extensions[messageIDText]["recalledAt"] == nil || extensions[messageIDText]["moderatedBy"] != owner {
		t.Fatalf("moderated extension=%#v err=%v", extensions[messageIDText], err)
	}
	if _, err = p.pool.Exec(ctx, `DELETE FROM im_members WHERE conversation_id=$1 AND user_id=$2`, cid, member); err != nil {
		t.Fatal(err)
	}
	refs, err := p.ListWukongForwardMessageRefs(ctx, member, []string{messageIDText})
	if err != nil || len(refs) != 1 || refs[0].ChannelID != cid {
		t.Fatalf("favorite access refs=%+v err=%v", refs, err)
	}
	if err = p.SetFavorite(ctx, member, messageIDText, false); err != nil {
		t.Fatal(err)
	}
	if _, err = p.ListWukongForwardMessageRefs(ctx, member, []string{messageIDText}); err != ErrForbidden {
		t.Fatalf("removed favorite access err=%v", err)
	}
	var legacyTable *string
	if err = p.pool.QueryRow(ctx, `SELECT to_regclass('im_messages')::text`).Scan(&legacyTable); err != nil || legacyTable != nil {
		t.Fatalf("legacy message table=%v err=%v", legacyTable, err)
	}
}

func TestWukongMessageExpiryCleansBusinessMetadataAndEmitsOneCommand(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, member := "wk_exp_owner_"+suffix, "wk_exp_member_"+suffix
	cid, mediaID := "wk_exp_group_"+suffix, "wk_exp_media_"+suffix
	messageID := time.Now().UnixNano()
	messageIDText := strconv.FormatInt(messageID, 10)
	now := time.Now().UTC().Truncate(time.Microsecond)
	messageAt := now.Add(-2 * time.Minute)
	expiresAt := messageAt.Add(time.Minute)
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES
		($1,$2,'Owner',$5),($3,$4,'Member',$5)`, owner, "exp_owner_"+suffix, member, "exp_member_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group','Expiry group',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at) VALUES($1,$2,$3)`, cid, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES
		($1,$2,'owner',$4),($1,$3,'member',$4)`, cid, owner, member, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_media(id,owner_id,object_key,mime,size,status,created_at,completed_at)
		VALUES($1,$2,$3,'image/png',10,'ready',$4,$4)`, mediaID, owner, "users/"+owner+"/expiry.png", now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_media_channels(media_id,channel_id,channel_type,sender_id,created_at)
		VALUES($1,$2,$3,$4,$5)`, mediaID, cid, wukong.ChannelGroup, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_message_index(
		message_id,client_msg_no,conversation_id,sender_id,channel_id,channel_type,message_seq,
		content_type,expire_seconds,media_id,expires_at,payload_sha256,message_timestamp,indexed_at)
		VALUES($1,$2,$3,$4,$3,$5,5,$6,60,$7,$8,$9,$10,$11)`, messageID,
		"wk-exp-client-"+suffix, cid, owner, wukong.ChannelGroup, wukong.ContentTypeImage,
		mediaID, expiresAt, strings.Repeat("e", 64), messageAt, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_message_reactions(message_id,user_id,emoji,created_at) VALUES($1,$2,'like',$3)`, messageIDText, member, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_group_message_pins(conversation_id,message_id,pinned_by,pinned_at) VALUES($1,$2,$3,$4)`, cid, messageIDText, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_favorites(user_id,message_id,created_at) VALUES($1,$2,$3)`, member, messageIDText, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_message_extensions(channel_id,channel_type,message_id,version,payload,updated_by,updated_at)
		VALUES($1,$2,$3,1,'{"isPinned":true}'::jsonb,$4,$5)`, cid, wukong.ChannelGroup, messageID, owner, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_wukong_reminders(user_id,conversation_id,message_id,message_seq,channel_id,channel_type,type,created_at,updated_at)
		VALUES($1,$2,$3,5,$2,$4,1,$5,$5)`, member, cid, messageID, wukong.ChannelGroup, now); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_outbox WHERE aggregate_type='conversation' AND aggregate_id=$1`, cid)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_wukong_message_index WHERE message_id=$1`, messageID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_media WHERE id=$1`, mediaID)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(context.Background(), `DELETE FROM im_users WHERE id=ANY($1::text[])`, []string{owner, member})
	})

	expired, err := p.ExpireMessages(ctx, now, 10)
	if err != nil {
		t.Fatal(err)
	}
	var found *ExpiredMessage
	for index := range expired {
		if expired[index].MessageID == messageIDText {
			found = &expired[index]
			break
		}
	}
	if found == nil || !found.ExpiredAt.Equal(expiresAt) || !slices.Equal(found.MemberIDs, []string{member, owner}) {
		t.Fatalf("expired=%+v expected deadline=%s", found, expiresAt)
	}
	var metadataRows, mediaBindings, commands int
	if err = p.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM im_message_reactions WHERE message_id=$1)+
		(SELECT count(*) FROM im_group_message_pins WHERE message_id=$1)+
		(SELECT count(*) FROM im_favorites WHERE message_id=$1)+
		(SELECT count(*) FROM im_wukong_message_extensions WHERE message_id=$2)+
		(SELECT count(*) FROM im_wukong_reminders WHERE message_id=$2),
		(SELECT count(*) FROM im_wukong_media_channels WHERE media_id=$3),
		(SELECT count(*) FROM im_wukong_outbox WHERE operation=$4 AND aggregate_type='conversation'
			AND aggregate_id=$5 AND payload->>'event'='message.expired'
			AND payload->'recipients' @> $6::jsonb)`, messageIDText, messageID, mediaID,
		wukong.OperationBusinessEvent, cid, fmt.Sprintf(`[%q,%q]`, member, owner)).Scan(&metadataRows, &mediaBindings, &commands); err != nil {
		t.Fatal(err)
	}
	if metadataRows != 0 || mediaBindings != 0 || commands != 1 {
		t.Fatalf("metadata=%d mediaBindings=%d commands=%d", metadataRows, mediaBindings, commands)
	}
	again, err := p.ExpireMessages(ctx, now.Add(time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range again {
		if item.MessageID == messageIDText {
			t.Fatalf("WuKong expiry was not idempotent: %+v", item)
		}
	}
}

func TestPostgresUserHandlePolicyAndExactIdentifierSearch(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uid, phone := "usr_handle_"+suffix, "phone_"+suffix
	u, err := p.RegisterPasswordUser(ctx, phone, "Handle User", uid, "hash", time.Now())
	if err != nil || !strings.HasPrefix(u.Handle, "ll_") || u.HandleChangeCount != 0 {
		t.Fatalf("registered=%+v err=%v", u, err)
	}
	first, second := "neighbor_"+suffix[len(suffix)-6:], "resident_"+suffix[len(suffix)-6:]
	u, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &first})
	if err != nil || u.HandleChangeCount != 1 {
		t.Fatalf("first update=%+v err=%v", u, err)
	}
	u, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &first})
	if err != nil || u.HandleChangeCount != 1 {
		t.Fatalf("idempotent update=%+v err=%v", u, err)
	}
	u, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &second})
	if err != nil || u.HandleChangeCount != 2 {
		t.Fatalf("second update=%+v err=%v", u, err)
	}
	third := "blocked_" + suffix[len(suffix)-6:]
	if _, err = p.UpdateUserProfile(ctx, uid, UserProfileUpdate{Handle: &third}); err != ErrForbidden {
		t.Fatalf("third update err=%v", err)
	}
	items, err := p.SearchUsersByIdentifier(ctx, second, "handle", 20)
	if err != nil || len(items) != 1 || items[0].ID != uid || items[0].Phone != "" {
		t.Fatalf("handle search=%+v err=%v", items, err)
	}
	items, err = p.SearchUsersByIdentifier(ctx, phone, "phone", 20)
	if err != nil || len(items) != 1 || items[0].ID != uid || items[0].Phone != "" {
		t.Fatalf("phone search=%+v err=%v", items, err)
	}
}

func TestPostgresMediaAccessRequiresOwnerOrCurrentReferencedConversationMember(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, member, outsider := "media_owner_"+suffix, "media_member_"+suffix, "media_out_"+suffix
	now := time.Now()
	for _, uid := range []string{owner, member, outsider} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	cid, mediaID := "media_group_"+suffix, "media_"+suffix
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "Media Group", []string{member}, now); err != nil {
		t.Fatal(err)
	}
	if err = p.CreateMedia(ctx, Media{ID: mediaID, OwnerID: owner, ObjectKey: "objects/" + mediaID, MIME: "image/jpeg", Size: 10, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if err = p.BindMediaChannel(ctx, MediaChannelBinding{MediaID: mediaID, ChannelID: cid, ChannelType: wukong.ChannelGroup, SenderID: owner}); err != nil {
		t.Fatal(err)
	}
	for label, uid := range map[string]string{"owner": owner, "member": member} {
		allowed, accessErr := p.CanAccessMedia(ctx, uid, mediaID)
		if accessErr != nil || !allowed {
			t.Fatalf("%s allowed=%v err=%v", label, allowed, accessErr)
		}
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, outsider, mediaID); accessErr != nil || allowed {
		t.Fatalf("outsider allowed=%v err=%v", allowed, accessErr)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: owner, ConversationID: cid, TargetID: member, Action: "remove", At: now.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if allowed, accessErr := p.CanAccessMedia(ctx, member, mediaID); accessErr != nil || allowed {
		t.Fatalf("removed member allowed=%v err=%v", allowed, accessErr)
	}
}

func TestPostgresAccountDeletionAnonymizesAndPreservesMessageReferences(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, successor, originalPhone := "delete_owner_"+suffix, "delete_successor_"+suffix, "139"+suffix
	now := time.Now()
	for _, data := range []struct{ id, phone string }{{owner, originalPhone}, {successor, "138" + suffix}} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,handle,created_at) VALUES($1,$2,$1,'ll_'||right(md5($1),20),$3)`, data.id, data.phone, now); err != nil {
			t.Fatal(err)
		}
	}
	cid := "delete_group_" + suffix
	if _, err = p.CreateGroupRecord(ctx, cid, owner, "Delete Test", []string{successor}, now); err != nil {
		t.Fatal(err)
	}
	messageID := time.Now().UnixNano()
	insertTestWukongMessage(t, p, ctx, messageID, "before-delete", cid, owner, 1, wukong.ContentTypeText, nil, "", now)
	if err = p.CreateRefreshSession(ctx, "delete_refresh_"+suffix, owner, []byte("hash"), now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = p.RegisterDevice(ctx, owner, Device{ID: "delete_device_" + suffix, Platform: "ios", Provider: "apns", PushToken: "delete_token_" + suffix}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_friendships(user_id,friend_user_id) VALUES($1,$2),($2,$1)`, owner, successor); err != nil {
		t.Fatal(err)
	}
	if duplicate, deleteErr := p.DeleteAccount(ctx, owner, now.Add(time.Second)); deleteErr != ErrConflict || duplicate {
		t.Fatalf("owner deletion duplicate=%v err=%v", duplicate, deleteErr)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: owner, ConversationID: cid, TargetID: successor, Action: "transfer", At: now.Add(2 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if duplicate, deleteErr := p.DeleteAccount(ctx, owner, now.Add(3*time.Second)); deleteErr != nil || duplicate {
		t.Fatalf("delete duplicate=%v err=%v", duplicate, deleteErr)
	}
	if duplicate, deleteErr := p.DeleteAccount(ctx, owner, now.Add(4*time.Second)); deleteErr != nil || !duplicate {
		t.Fatalf("retry duplicate=%v err=%v", duplicate, deleteErr)
	}
	var phone, handle, name string
	var banned bool
	var deletedAt *time.Time
	if err = p.pool.QueryRow(ctx, `SELECT phone,handle,name,banned,deleted_at FROM im_users WHERE id=$1`, owner).Scan(&phone, &handle, &name, &banned, &deletedAt); err != nil || phone == originalPhone || !strings.HasPrefix(handle, "deleted_") || name != "已注销用户" || !banned || deletedAt == nil {
		t.Fatalf("anonymized phone=%q handle=%q name=%q banned=%v deletedAt=%v err=%v", phone, handle, name, banned, deletedAt, err)
	}
	for label, query := range map[string]string{
		"active refresh": `SELECT count(*) FROM im_refresh_sessions WHERE user_id=$1 AND revoked_at IS NULL`,
		"devices":        `SELECT count(*) FROM im_devices WHERE user_id=$1`,
		"friendships":    `SELECT count(*) FROM im_friendships WHERE user_id=$1 OR friend_user_id=$1`,
		"memberships":    `SELECT count(*) FROM im_members WHERE user_id=$1`,
	} {
		var count int
		if err = p.pool.QueryRow(ctx, query, owner).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", label, count, err)
		}
	}
	var senderID string
	if err = p.pool.QueryRow(ctx, `SELECT sender_id FROM im_wukong_message_index WHERE message_id=$1`, messageID).Scan(&senderID); err != nil || senderID != owner {
		t.Fatalf("message reference sender=%q err=%v", senderID, err)
	}
	var audits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE actor_id=$1 AND action='account.deleted'`, owner).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("account deletion audits=%d err=%v", audits, err)
	}
	newUserID := "replacement_" + suffix
	if user, createErr := p.LoginOrCreateUser(ctx, originalPhone, "Replacement", newUserID, now.Add(5*time.Second)); createErr != nil || user.ID != newUserID {
		t.Fatalf("phone reuse user=%+v err=%v", user, createErr)
	}
}

func TestPostgresGroupManagementPermissionsInvitesQRAndAudit(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	users := []string{"grp_owner_" + suffix, "grp_admin_" + suffix, "grp_member_" + suffix, "grp_qr_" + suffix}
	now := time.Now()
	for _, uid := range users {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at)VALUES($1,$1,$1,$2)`, uid, now); err != nil {
			t.Fatal(err)
		}
	}
	cid := "grp_conv_" + suffix
	c, err := p.CreateGroupRecord(ctx, cid, users[0], "Initial", []string{users[1]}, now)
	if err != nil || c.Title != "Initial" {
		t.Fatalf("create=%+v err=%v", c, err)
	}
	g, err := p.GetGroupProfile(ctx, users[1], cid)
	if err != nil || g.OwnerID != users[0] {
		t.Fatalf("profile=%+v err=%v", g, err)
	}
	name, policy, allow := "Renamed", "qr", false
	if _, err = p.UpdateGroupProfile(ctx, users[1], cid, GroupProfileUpdate{Name: &name}, now.Add(time.Second)); err != ErrForbidden {
		t.Fatalf("member update=%v", err)
	}
	avatarID := "grp_avatar_" + suffix
	if err = p.CreateMedia(ctx, Media{ID: avatarID, OwnerID: users[0], ObjectKey: "objects/" + avatarID, MIME: "image/jpeg", Size: 128, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	g, err = p.UpdateGroupProfile(ctx, users[0], cid, GroupProfileUpdate{Name: &name, AvatarMediaID: &avatarID, JoinPolicy: &policy, AllowMemberAddFriend: &allow, RotateQR: true}, now.Add(2*time.Second))
	if err != nil || g.QRToken == "" || g.AllowMemberAddFriend || g.AvatarURL != "/v2/media/"+avatarID {
		t.Fatalf("update=%+v err=%v", g, err)
	}
	foreignAvatarID := "grp_foreign_avatar_" + suffix
	if err = p.CreateMedia(ctx, Media{ID: foreignAvatarID, OwnerID: users[1], ObjectKey: "objects/" + foreignAvatarID, MIME: "image/png", Size: 64, Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err = p.UpdateGroupProfile(ctx, users[0], cid, GroupProfileUpdate{AvatarMediaID: &foreignAvatarID}, now.Add(2500*time.Millisecond)); err != ErrForbidden {
		t.Fatalf("foreign group avatar update=%v", err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[0], ConversationID: cid, TargetID: users[1], Action: "role", Role: "admin", At: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	g, err = p.SetGroupAnnouncement(ctx, users[1], cid, "Welcome", now.Add(4*time.Second))
	if err != nil || g.AnnouncementVersion != 1 {
		t.Fatalf("announcement=%+v err=%v", g, err)
	}
	if err = p.MarkGroupAnnouncementRead(ctx, users[1], cid, now.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	invite := &model.GroupInvite{ID: "ginv_" + suffix, ConversationID: cid, InviterID: users[1], InviteeID: users[2], Source: "invite", Status: "pending", CreatedAt: now.Add(6 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(6 * time.Second)}
	created, dup, err := p.CreateGroupInvite(ctx, invite)
	if err != nil || dup || created.Status != "pending" {
		t.Fatalf("invite=%+v dup=%v err=%v", created, dup, err)
	}
	_, dup, err = p.CreateGroupInvite(ctx, invite)
	if err != nil || !dup {
		t.Fatalf("retry dup=%v err=%v", dup, err)
	}
	listedInvites, err := p.ListGroupInvites(ctx, users[2], "pending", 20)
	if err != nil || len(listedInvites) != 1 {
		t.Fatalf("list pending invites=%+v err=%v", listedInvites, err)
	}
	listedInvite, _ := listedInvites[0]["invite"].(*model.GroupInvite)
	listedInviter, _ := listedInvites[0]["inviter"].(*model.User)
	if listedInvite == nil || listedInvite.ID != invite.ID || listedInviter == nil || listedInviter.ID != users[1] || listedInviter.Phone != "" || listedInvites[0]["groupName"] != "Renamed" || listedInvites[0]["outgoing"] != false {
		t.Fatalf("unsafe/incomplete group invite projection=%+v", listedInvites[0])
	}
	accepted, dup, err := p.TransitionGroupInvite(ctx, invite.ID, users[2], "accept", now.Add(7*time.Second))
	if err != nil || dup || accepted.Status != "accepted" {
		t.Fatalf("accept=%+v dup=%v err=%v", accepted, dup, err)
	}
	listedInvites, err = p.ListGroupInvites(ctx, users[2], "pending", 20)
	if err != nil || len(listedInvites) != 0 {
		t.Fatalf("accepted invite remained pending=%+v err=%v", listedInvites, err)
	}
	groupFriendRequest := &model.FriendRequest{ID: "group_friend_" + suffix, FromUserID: users[1], ToUserID: users[2], Source: "group", SourceID: cid, Status: "pending", CreatedAt: now.Add(7 * time.Second), ExpiresAt: now.Add(time.Hour), UpdatedAt: now.Add(7 * time.Second)}
	if _, _, err = p.CreateFriendRequest(ctx, groupFriendRequest); err != ErrForbidden {
		t.Fatalf("disabled group member add friend err=%v", err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[2], ConversationID: cid, TargetID: users[2], Action: "nickname", Nickname: "小三", At: now.Add(8 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	until := now.Add(time.Hour)
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[1], ConversationID: cid, TargetID: users[2], Action: "mute", MutedUntil: &until, At: now.Add(9 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	members, err := p.ListConversationMembers(ctx, users[0], cid)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range members {
		if m.UserID == users[2] && m.GroupNickname == "小三" && m.MutedUntil != nil {
			found = true
		}
	}
	if !found {
		t.Fatalf("members=%+v", members)
	}
	if err = p.JoinGroupByQR(ctx, users[3], g.QRToken, now.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	groupConversations, err := p.ListConversations(ctx, users[0], 20)
	if err != nil {
		t.Fatal(err)
	}
	foundGroupProjection := false
	for _, item := range groupConversations {
		conversation := item["conversation"].(*model.Conversation)
		if conversation.ID != cid {
			continue
		}
		projected, ok := item["members"].([]*model.ConversationMember)
		if !ok || len(projected) != 4 {
			t.Fatalf("group conversation members=%T %+v", item["members"], item["members"])
		}
		for _, member := range projected {
			rawMember, _ := json.Marshal(member)
			if member.UserID == "" || member.Name == "" || strings.Contains(string(rawMember), `"phone"`) {
				t.Fatalf("unsafe group conversation member=%+v", member)
			}
		}
		foundGroupProjection = true
	}
	if !foundGroupProjection {
		t.Fatalf("group conversation projection missing: %+v", groupConversations)
	}
	allMuted := now.Add(time.Hour)
	if _, err = p.UpdateGroupProfile(ctx, users[0], cid, GroupProfileUpdate{AllMutedUntil: &allMuted}, now.Add(11*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: users[3], ConversationID: cid, Type: "text", Text: "no"}); err != ErrForbidden {
		t.Fatalf("mute send=%v", err)
	}
	if err = p.ApplyGroupMemberAction(ctx, GroupMemberAction{ActorID: users[0], ConversationID: cid, TargetID: users[2], Action: "transfer", At: now.Add(13 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if err = p.DisbandGroupRecord(ctx, users[0], cid, "old", now.Add(14*time.Second)); err != ErrForbidden {
		t.Fatalf("old owner=%v", err)
	}
	if err = p.DisbandGroupRecord(ctx, users[2], cid, "closed", now.Add(15*time.Second)); err != nil {
		t.Fatal(err)
	}
	var audits int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE target_id=$1 AND action LIKE 'group.%'`, cid).Scan(&audits); err != nil || audits < 8 {
		t.Fatalf("audits=%d err=%v", audits, err)
	}
	var systemMessages int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND payload->>'channel_id'=$2 AND payload->'payload'->>'type'=$3`, wukong.OperationStoredMessage, cid, strconv.Itoa(wukong.ContentTypeSystemEvent)).Scan(&systemMessages); err != nil || systemMessages < 8 {
		t.Fatalf("WuKong system messages=%d err=%v", systemMessages, err)
	}
	var legacyTable *string
	if err = p.pool.QueryRow(ctx, `SELECT to_regclass('im_messages')::text`).Scan(&legacyTable); err != nil || legacyTable != nil {
		t.Fatalf("legacy message table=%v err=%v", legacyTable, err)
	}
}

func TestAnnouncementPostgresLifecycleAndPushOutbox(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uid := "announce_user_" + suffix
	now := time.Now()
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$1,$1,$2)`, uid, now); err != nil {
		t.Fatal(err)
	}
	defer p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=$1`, uid)
	announcementID, scheduledID := "announce_"+suffix, "scheduled_"+suffix
	defer func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_wukong_outbox WHERE aggregate_type='announcement' AND aggregate_id=ANY($1::text[])`, []string{announcementID, scheduledID})
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_push_outbox WHERE payload->>'announcementId'=ANY($1::text[])`, []string{announcementID, scheduledID})
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_audits WHERE target_type='announcement' AND target_id=ANY($1::text[])`, []string{announcementID, scheduledID})
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_announcements WHERE id=ANY($1::text[])`, []string{announcementID, scheduledID})
	}()
	input := AnnouncementInput{ID: announcementID, Title: "Release", Content: "New version", Status: "draft", Pinned: true, TargetType: "users", TargetUserIDs: []string{uid}, ActorID: "admin"}
	created, err := p.CreateAnnouncement(ctx, input, now)
	if err != nil || created.Status != "draft" {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	items, err := p.ListAnnouncements(ctx, uid, now)
	if err != nil || len(items) != 0 {
		t.Fatalf("draft list=%v err=%v", items, err)
	}
	published, err := p.PublishAnnouncement(ctx, input.ID, "admin", true, now.Add(time.Second))
	if err != nil || published.Status != "published" {
		t.Fatalf("publish=%+v err=%v", published, err)
	}
	var pushes int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_push_outbox WHERE user_id=$1 AND event_type='announcement.published' AND payload->>'announcementId'=$2`, uid, input.ID).Scan(&pushes); err != nil || pushes != 1 {
		t.Fatalf("pushes=%d err=%v", pushes, err)
	}
	if err = p.MarkAnnouncementRead(ctx, uid, input.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListAnnouncements(ctx, uid, now.Add(3*time.Second))
	if err != nil || len(items) != 1 || items[0].ReadAt == nil {
		t.Fatalf("published list=%v err=%v", items, err)
	}
	if _, err = p.WithdrawAnnouncement(ctx, input.ID, "admin", now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListAnnouncements(ctx, uid, now.Add(5*time.Second))
	if err != nil || len(items) != 0 {
		t.Fatalf("withdrawn list=%v err=%v", items, err)
	}
	if err = p.DeleteAnnouncement(ctx, input.ID, "admin", now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	due := now.Add(10 * time.Second)
	scheduled := AnnouncementInput{ID: scheduledID, Title: "Scheduled", Content: "Scheduled release", Status: "scheduled", TargetType: "users", TargetUserIDs: []string{uid}, ScheduledAt: &due, PushOnPublish: true, ActorID: "admin"}
	if _, err = p.CreateAnnouncement(ctx, scheduled, now); err != nil {
		t.Fatal(err)
	}
	count, err := p.PromoteDueAnnouncements(ctx, due.Add(time.Second))
	if err != nil || count != 1 {
		t.Fatalf("scheduled promotion count=%d err=%v", count, err)
	}
	count, err = p.PromoteDueAnnouncements(ctx, due.Add(2*time.Second))
	if err != nil || count != 0 {
		t.Fatalf("idempotent promotion count=%d err=%v", count, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_push_outbox WHERE user_id=$1 AND event_type='announcement.published' AND payload->>'announcementId'=$2`, uid, scheduled.ID).Scan(&pushes); err != nil || pushes != 1 {
		t.Fatalf("scheduled pushes=%d err=%v", pushes, err)
	}
	if _, err = p.WithdrawAnnouncement(ctx, scheduled.ID, "admin", due.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		id    string
		event string
	}{
		{id: announcementID, event: "announcement.published"},
		{id: announcementID, event: "announcement.withdrawn"},
		{id: scheduledID, event: "announcement.published"},
		{id: scheduledID, event: "announcement.withdrawn"},
	} {
		var count, recipients int
		if err = p.pool.QueryRow(ctx, `SELECT count(*),COALESCE(sum(jsonb_array_length(payload->'recipients')),0) FROM im_wukong_outbox WHERE operation=$1 AND aggregate_type='announcement' AND aggregate_id=$2 AND payload->>'event'=$3 AND payload->'param'->>'schemaVersion'='1'`, wukong.OperationBusinessEvent, expected.id, expected.event).Scan(&count, &recipients); err != nil || count != 1 || recipients != 1 {
			t.Fatalf("announcement CMD id=%s event=%s count=%d recipients=%d err=%v", expected.id, expected.event, count, recipients, err)
		}
	}
	if err = p.DeleteAnnouncement(ctx, scheduled.ID, "admin", due.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresMessageCollaborationLifecycle(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	owner, admin, member, outsider := "collab_owner_"+suffix, "collab_admin_"+suffix, "collab_member_"+suffix, "collab_outsider_"+suffix
	cid := "collab_group_" + suffix
	messageID := time.Now().UnixNano()
	mid := strconv.FormatInt(messageID, 10)
	now := time.Now().UTC()
	users := []string{owner, admin, member, outsider}
	defer func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_audits WHERE actor_id=ANY($1::text[])`, users)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_conversations WHERE id=$1`, cid)
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=ANY($1::text[])`, users)
	}()
	for index, userID := range users {
		phone := fmt.Sprintf("18%d%s", index, suffix[len(suffix)-8:])
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,$1,$3)`, userID, phone, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_conversations(id,kind,title,created_at,updated_at) VALUES($1,'group','Collaboration',$2,$2)`, cid, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_groups(conversation_id,owner_id,updated_at) VALUES($1,$2,$3)`, cid, owner, now); err != nil {
		t.Fatal(err)
	}
	for userID, role := range map[string]string{owner: "owner", admin: "admin", member: "member"} {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_members(conversation_id,user_id,role,joined_at) VALUES($1,$2,$3,$4)`, cid, userID, role, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_devices(id,user_id,platform,provider,push_token,updated_at) VALUES($1,$2,'ios','apns',$3,$4)`, "collab_device_"+suffix, member, "collab_token_"+suffix, now); err != nil {
		t.Fatal(err)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: member, ConversationID: cid, Type: "text", Text: "hello", MentionAll: true}); err != ErrForbidden {
		t.Fatalf("member @all error=%v", err)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: owner, ConversationID: cid, Type: "text", Text: "hello", Mentions: []string{outsider}}); err != ErrForbidden {
		t.Fatalf("outside mention error=%v", err)
	}
	if _, err = p.AuthorizeWukongMessage(ctx, WukongMessageRouteInput{UserID: owner, ConversationID: cid, Type: "text", Text: "first draft", Mentions: []string{member}}); err != nil {
		t.Fatalf("WuKong policy=%v", err)
	}
	insertTestWukongMessage(t, p, ctx, messageID, "valid-"+suffix, cid, owner, 1, wukong.ContentTypeText, nil, "", now)
	duplicate := false
	originalBody := map[string]any{"text": "first draft", "mentions": []string{member}}
	edited, duplicate, err := p.EditMessage(ctx, owner, mid, "edit-1", map[string]any{"text": "final 100%_searchable", "mentions": []string{member}}, originalBody, now.Add(time.Second), 2*time.Minute)
	if err != nil || duplicate || edited.EditVersion != 1 || edited.EditedAt == nil {
		t.Fatalf("edit=%+v duplicate=%v err=%v", edited, duplicate, err)
	}
	if _, duplicate, err = p.EditMessage(ctx, owner, mid, "edit-1", map[string]any{"text": "final 100%_searchable", "mentions": []string{member}}, nil, now.Add(2*time.Second), 2*time.Minute); err != nil || !duplicate {
		t.Fatalf("edit retry duplicate=%v err=%v", duplicate, err)
	}
	if _, duplicate, err = p.EditMessage(ctx, owner, mid, "noop-1", map[string]any{"text": "final 100%_searchable", "mentions": []string{member}}, nil, now.Add(2*time.Second), 2*time.Minute); err != nil || !duplicate {
		t.Fatalf("no-op edit duplicate=%v err=%v", duplicate, err)
	}
	if _, _, err = p.EditMessage(ctx, owner, mid, "noop-1", map[string]any{"text": "different body"}, nil, now.Add(2*time.Second), 2*time.Minute); err != ErrConflict {
		t.Fatalf("reused edit id error=%v", err)
	}
	if _, _, err = p.EditMessage(ctx, member, mid, "edit-other", map[string]any{"text": "hijack"}, nil, now.Add(3*time.Second), 2*time.Minute); err != ErrForbidden {
		t.Fatalf("non-author edit error=%v", err)
	}
	edits, err := p.ListMessageEdits(ctx, member, mid)
	if err != nil || len(edits) != 2 || edits[0].Version != 0 || edits[1].Version != 1 {
		t.Fatalf("edits=%+v err=%v", edits, err)
	}
	reaction, duplicate, err := p.SetMessageReaction(ctx, member, mid, "👍", true, now.Add(4*time.Second))
	if err != nil || duplicate || reaction.Count != 1 || !reaction.ReactedByMe {
		t.Fatalf("member reaction=%+v duplicate=%v err=%v", reaction, duplicate, err)
	}
	reaction, duplicate, err = p.SetMessageReaction(ctx, admin, mid, "👍", true, now.Add(5*time.Second))
	if err != nil || duplicate || reaction.Count != 2 {
		t.Fatalf("admin reaction=%+v duplicate=%v err=%v", reaction, duplicate, err)
	}
	if _, duplicate, err = p.SetMessageReaction(ctx, admin, mid, "👍", true, now.Add(6*time.Second)); err != nil || !duplicate {
		t.Fatalf("reaction retry duplicate=%v err=%v", duplicate, err)
	}
	start := make(chan struct{})
	concurrentResults := make(chan model.MessageReactionSummary, 2)
	concurrentErrors := make(chan error, 2)
	var reactionWG sync.WaitGroup
	for _, actor := range []string{member, admin} {
		reactionWG.Add(1)
		go func(actorID string) {
			defer reactionWG.Done()
			<-start
			item, wasDuplicate, reactionErr := p.SetMessageReaction(ctx, actorID, mid, "🎉", true, now.Add(6*time.Second))
			if reactionErr != nil {
				concurrentErrors <- reactionErr
				return
			}
			if wasDuplicate {
				concurrentErrors <- fmt.Errorf("unexpected concurrent reaction duplicate for %s", actorID)
				return
			}
			concurrentResults <- item
		}(actor)
	}
	close(start)
	reactionWG.Wait()
	close(concurrentErrors)
	for reactionErr := range concurrentErrors {
		t.Fatal(reactionErr)
	}
	close(concurrentResults)
	counts := []int{}
	for item := range concurrentResults {
		counts = append(counts, item.Count)
	}
	sort.Ints(counts)
	if fmt.Sprint(counts) != "[1 2]" {
		t.Fatalf("concurrent reaction counts=%v want [1 2]", counts)
	}
	var persistedReactionCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_message_reactions WHERE message_id=$1 AND emoji='🎉'`, mid).Scan(&persistedReactionCount); err != nil || persistedReactionCount != 2 {
		t.Fatalf("persisted concurrent reactions=%d err=%v", persistedReactionCount, err)
	}
	if _, _, err = p.SetGroupMessagePin(ctx, member, cid, mid, true, now.Add(7*time.Second)); err != ErrForbidden {
		t.Fatalf("member pin error=%v", err)
	}
	if _, duplicate, err = p.SetGroupMessagePin(ctx, admin, cid, mid, true, now.Add(8*time.Second)); err != nil || duplicate {
		t.Fatalf("admin pin duplicate=%v err=%v", duplicate, err)
	}
	pins, err := p.ListGroupMessagePins(ctx, member, cid, 0, 10)
	if err != nil || len(pins) != 1 || pins[0].Message.ID != mid {
		t.Fatalf("pins=%+v err=%v", pins, err)
	}
	extensions, err := p.LoadWukongMessageExtensions(ctx, member, []string{mid})
	if err != nil {
		t.Fatal(err)
	}
	reactions, _ := extensions[mid]["reactions"].([]map[string]any)
	if len(reactions) != 2 {
		t.Fatalf("reaction extensions=%+v", extensions[mid])
	}
	var thumbsUpCount int
	for _, summary := range reactions {
		if summary["emoji"] == "👍" {
			thumbsUpCount, _ = summary["count"].(int)
		}
	}
	if thumbsUpCount != 2 {
		t.Fatalf("thumbs-up reaction count=%d want 2", thumbsUpCount)
	}
	if _, duplicate, err = p.SetGroupMessagePin(ctx, owner, cid, mid, false, now.Add(9*time.Second)); err != nil || duplicate {
		t.Fatalf("owner unpin duplicate=%v err=%v", duplicate, err)
	}
	var systemCount, editAuditCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE operation=$1 AND payload->>'channel_id'=$2 AND payload->'payload'->>'type'=$3`, wukong.OperationStoredMessage, cid, strconv.Itoa(wukong.ContentTypeSystemEvent)).Scan(&systemCount); err != nil || systemCount != 2 {
		t.Fatalf("WuKong system messages=%d err=%v", systemCount, err)
	}
	var legacyTable *string
	if err = p.pool.QueryRow(ctx, `SELECT to_regclass('im_messages')::text`).Scan(&legacyTable); err != nil || legacyTable != nil {
		t.Fatalf("legacy message table=%v err=%v", legacyTable, err)
	}
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE target_id=$1 AND action='message.edited'`, mid).Scan(&editAuditCount); err != nil || editAuditCount != 1 {
		t.Fatalf("edit audits=%d err=%v", editAuditCount, err)
	}
}

func TestPostgresInvalidatesOnlySupportedPushProviders(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	uid := "push_invalidation_user_" + suffix
	deviceIDs := []string{
		"push_invalidation_getui_" + suffix,
		"push_invalidation_voip_" + suffix,
		"push_invalidation_webpush_" + suffix,
		"push_invalidation_fcm_" + suffix,
	}
	if _, err = p.pool.Exec(ctx, `INSERT INTO im_users(id,phone,name,created_at) VALUES($1,$2,'Push invalidation test',now())`, uid, "push_invalidation_"+suffix); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_users WHERE id=$1`, uid)
	})
	providers := []string{"getui", "apns_voip", "webpush", "fcm"}
	for index, deviceID := range deviceIDs {
		if _, err = p.pool.Exec(ctx, `INSERT INTO im_devices(id,user_id,platform,provider,push_token,notifications_enabled,updated_at) VALUES($1,$2,'ios',$3,$4,true,now())`, deviceID, uid, providers[index], "push_token_"+suffix+fmt.Sprint(index)); err != nil {
			t.Fatal(err)
		}
	}

	if err = p.InvalidatePushDevices(ctx, deviceIDs); err != nil {
		t.Fatal(err)
	}
	type deviceState struct {
		enabled bool
		token   string
	}
	states := make(map[string]deviceState, len(deviceIDs))
	rows, err := p.pool.Query(ctx, `SELECT provider,notifications_enabled,push_token FROM im_devices WHERE id=ANY($1::text[])`, deviceIDs)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var provider string
		var state deviceState
		if err = rows.Scan(&provider, &state.enabled, &state.token); err != nil {
			t.Fatal(err)
		}
		states[provider] = state
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, provider := range []string{"getui", "apns_voip", "webpush"} {
		state := states[provider]
		if state.enabled || state.token != "" {
			t.Fatalf("provider %s was not invalidated: %+v", provider, state)
		}
	}
	fcm := states["fcm"]
	if !fcm.enabled || fcm.token == "" {
		t.Fatalf("unrelated provider was modified: %+v", fcm)
	}
}

func TestClientVersionPoliciesAreDurableAndAudited(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, _ = p.pool.Exec(ctx, `DELETE FROM im_client_version_policies WHERE platform='web'`)
	t.Cleanup(func() {
		_, _ = p.pool.Exec(ctx, `DELETE FROM im_client_version_policies WHERE platform='web'`)
	})
	at := time.Now().UTC()
	policy, err := p.UpsertClientVersionPolicy(ctx, ClientVersionPolicy{
		Platform: "web", MinimumVersion: "1.2.0", LatestVersion: "1.4.0",
		ForceUpdate: true, RolloutPercentage: 35, ReleaseNotes: "可靠性更新",
		DownloadURL: "https://app.example.com/",
	}, "admin-version-test", "分批发布", at)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Platform != "web" || policy.UpdatedBy != "admin-version-test" || policy.RolloutPercentage != 35 {
		t.Fatalf("unexpected policy: %+v", policy)
	}
	loaded, err := p.GetClientVersionPolicy(ctx, "web")
	if err != nil || *loaded != *policy {
		t.Fatalf("loaded=%+v err=%v want=%+v", loaded, err, policy)
	}
	items, err := p.ListClientVersionPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range items {
		found = found || item.Platform == "web"
	}
	if !found {
		t.Fatal("web policy missing from list")
	}
	var auditCount int
	if err = p.pool.QueryRow(ctx, `SELECT count(*) FROM im_audits WHERE action='client_version_policy.updated' AND target_id='web' AND metadata->>'reason'='分批发布'`).Scan(&auditCount); err != nil || auditCount < 1 {
		t.Fatalf("audit count=%d err=%v", auditCount, err)
	}
}

func TestSetWukongSystemUserRejectsMissingAccountAsNotFound(t *testing.T) {
	url := os.Getenv("IM_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("IM_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	p, err := NewPostgres(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	missingID := fmt.Sprintf("missing_system_user_%d", time.Now().UnixNano())
	item, err := p.SetWukongSystemUser(ctx, missingID, false, "admin-system-user-test", "cleanup missing account", time.Now().UTC())
	if err != ErrNotFound || item != nil {
		t.Fatalf("item=%+v err=%v want nil, ErrNotFound", item, err)
	}
	var outboxCount int
	if queryErr := p.pool.QueryRow(ctx, `SELECT count(*) FROM im_wukong_outbox WHERE aggregate_type='system_user' AND aggregate_id=$1`, missingID).Scan(&outboxCount); queryErr != nil || outboxCount != 0 {
		t.Fatalf("outbox count=%d err=%v", outboxCount, queryErr)
	}
}
