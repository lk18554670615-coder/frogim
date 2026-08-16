package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

type adminHealthService struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Latency int64  `json:"latency"`
	Uptime  string `json:"uptime"`
	Version string `json:"version"`
	Detail  string `json:"detail"`
}

func (x *API) adminHealth(w http.ResponseWriter, r *http.Request) {
	items := []adminHealthService{{
		Name:    "青蛙呱呱 API",
		Status:  "healthy",
		Latency: 0,
		Uptime:  durationLabel(time.Since(x.started)),
		Version: "当前部署",
		Detail:  "HTTP 服务已响应",
	}}
	items = append(items, probeAdminHealth(r.Context(), "PostgreSQL 数据库", "当前部署", func(ctx context.Context) error {
		return x.app.Ready(ctx)
	}))
	if !x.cfg.WukongEnabled || x.imSessions == nil || x.wukongSetupErr != nil {
		items = append(items,
			adminHealthService{Name: "WuKongIM 实时消息", Status: "down", Uptime: "实时探测", Version: "当前部署", Detail: "实时消息服务未正确配置"},
			adminHealthService{Name: "WuKongIM 管理接口", Status: "down", Uptime: "实时探测", Version: "当前部署", Detail: "设备和节点管理暂不可用"},
		)
	} else {
		items = append(items, probeAdminHealth(r.Context(), "WuKongIM 实时消息", "当前部署", func(ctx context.Context) error {
			return x.imSessions.Ready(ctx)
		}))
		items = append(items, probeAdminHealth(r.Context(), "WuKongIM 管理接口", "当前部署", func(ctx context.Context) error {
			var result map[string]any
			return x.wukongClient.ManagerHealth(ctx, &result)
		}))
	}
	write(w, http.StatusOK, map[string]any{"items": items})
}

func probeAdminHealth(parent context.Context, name, version string, probe func(context.Context) error) adminHealthService {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	started := time.Now()
	err := probe(ctx)
	latency := time.Since(started).Milliseconds()
	if latency < 1 {
		latency = 1
	}
	item := adminHealthService{Name: name, Status: "healthy", Latency: latency, Uptime: "实时探测", Version: version, Detail: "依赖服务响应正常"}
	if err != nil {
		item.Status = "down"
		item.Detail = "依赖服务当前无法连接，请检查进程、端口和网络"
	}
	return item
}

func durationLabel(duration time.Duration) string {
	hours := int(duration.Hours())
	if hours >= 24 {
		return fmt.Sprintf("%d 天", hours/24)
	}
	if hours > 0 {
		return fmt.Sprintf("%d 小时", hours)
	}
	minutes := int(duration.Minutes())
	if minutes > 0 {
		return fmt.Sprintf("%d 分钟", minutes)
	}
	return "不到 1 分钟"
}
