package handler

import (
	"errors"
	"testing"

	"github.com/WuKongIM/WuKongIM/internal/eventbus"
	"github.com/WuKongIM/WuKongIM/internal/options"
	"github.com/WuKongIM/WuKongIM/internal/service"
	"github.com/WuKongIM/WuKongIM/pkg/wklog"
	"github.com/WuKongIM/WuKongIM/pkg/wknet"
	wkproto "github.com/WuKongIM/WuKongIMGoProto"
)

type closeProbeManager struct {
	service.IConnManager
	conn    wknet.Conn
	invalid bool
}

func (m *closeProbeManager) GetConn(int64) wknet.Conn { return m.conn }
func (m *closeProbeManager) GetConnByFd(int) wknet.Conn {
	if m.invalid {
		return nil
	}
	return m.conn
}

type closeProbeConn struct {
	wknet.Conn
	event  *eventbus.Conn
	err    error
	closed bool
}

func (c *closeProbeConn) Fd() wknet.NetFd      { return wknet.NetFd{} }
func (c *closeProbeConn) Context() interface{} { return c.event }
func (c *closeProbeConn) Close() error         { c.closed = c.err == nil; return c.err }

type closeProbePool struct {
	eventbus.IUser
	manager            *closeProbeManager
	removed            bool
	removedBeforeClose bool
	events             []*eventbus.Event
}

func (p *closeProbePool) AddEvent(_ string, e *eventbus.Event) { p.events = append(p.events, e) }

func (p *closeProbePool) RemoveConn(*eventbus.Conn) {
	if p.manager.conn != nil {
		p.removedBeforeClose = !p.manager.conn.(*closeProbeConn).closed
	}
	p.manager.conn = nil // Reproduce EventPool.RemoveConn's physical index removal.
	p.removed = true
}

func TestTenantAuthenticationFailureClosesUnauthenticatedSocket(t *testing.T) {
	previousUser, previousOptions := eventbus.User, options.G
	t.Cleanup(func() { eventbus.User, options.G = previousUser, previousOptions })
	options.G = options.New()
	options.G.TokenAuthOn = true
	pool := &closeProbePool{}
	eventbus.RegisterUser(pool)
	conn := &eventbus.Conn{Uid: "test-rejected", ConnId: 8, NodeId: 1001}
	h := &Handler{Log: wklog.NewWKLog("tenant-reject-test")}
	h.connect(&eventbus.UserContext{Events: []*eventbus.Event{{Conn: conn, Frame: &wkproto.ConnectPacket{UID: conn.Uid}}}})
	if len(pool.events) != 1 || pool.events[0].Type != eventbus.EventConnClose || pool.events[0].Conn != conn {
		t.Fatal("failed authentication left unauthenticated socket open")
	}
}

func TestTenantClosePhysicalSocketBeforeRemovingLookup(t *testing.T) {
	previousUser, previousManager, previousOptions := eventbus.User, service.ConnManager, options.G
	t.Cleanup(func() { eventbus.User, service.ConnManager, options.G = previousUser, previousManager, previousOptions })
	options.G = options.New()
	options.G.Cluster.NodeId = 1001
	for _, scenario := range []string{"success", "close_error", "invalid", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			e := &eventbus.Conn{Uid: "test", ConnId: 7, NodeId: 1001}
			conn := &closeProbeConn{event: e}
			m := &closeProbeManager{conn: conn, invalid: scenario == "invalid"}
			if scenario == "missing" {
				m.conn = nil
			}
			if scenario == "close_error" {
				conn.err = errors.New("close unavailable")
			}
			pool := &closeProbePool{manager: m}
			eventbus.RegisterUser(pool)
			service.ConnManager = m
			h := &Handler{Log: wklog.NewWKLog("tenant-close-test")}
			h.closeConn(&eventbus.UserContext{Events: []*eventbus.Event{{Conn: e}}})
			if pool.removedBeforeClose {
				t.Fatal("connection index removed before physical close")
			}
			if scenario == "success" && (!conn.closed || !pool.removed) {
				t.Fatal("socket and index not closed")
			}
			if (scenario == "close_error" || scenario == "invalid") && (pool.removed || conn.closed) {
				t.Fatal("unconfirmed close falsely reported offline")
			}
			if scenario == "missing" && !pool.removed {
				t.Fatal("stale logical connection retained")
			}
		})
	}
}
