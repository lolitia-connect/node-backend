package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/enfein/mieru/v3/apis/trafficpattern"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/enfein/mieru/v3/pkg/common"
	"github.com/enfein/mieru/v3/pkg/metrics"
	"github.com/enfein/mieru/v3/pkg/protocol"
	"github.com/enfein/mieru/v3/pkg/socks5"
	"google.golang.org/protobuf/proto"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/serverstatus"
	"github.com/perfect-panel/ppanel-node/common/task"
	"github.com/perfect-panel/ppanel-node/conf"
	log "github.com/sirupsen/logrus"
)

// MieruController manages a mieru proxy node, parallel to XrayController.
type MieruController struct {
	mux       *protocol.Mux
	socks5Srv *socks5.Server
	Tag       string
	Info      *panel.NodeInfo
	Config    *conf.Conf
	ApiClient *panel.ClientV1
	userList  []panel.UserInfo

	UserListMonitorPeriodic *task.Task
	UserReportPeriodic      *task.Task

	mu      sync.Mutex
	running bool
}

// NewMieruController creates a new mieru controller.
func NewMieruController(config *conf.Conf, apiClient *panel.ClientV1, info *panel.NodeInfo) *MieruController {
	tag := fmt.Sprintf("[%s]-%s:%d", apiClient.APIHost, info.Type, info.Id)
	return &MieruController{
		Tag:       tag,
		Info:      info,
		Config:    config,
		ApiClient: apiClient,
	}
}

// Start starts the mieru node.
func (c *MieruController) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("mieru node %s is already running", c.Tag)
	}

	// Fetch user list from panel — same as XrayController
	userList, err := c.ApiClient.GetUserList(context.Background())
	if err != nil {
		return fmt.Errorf("mieru %s: get user list error: %w", c.Tag, err)
	}
	if len(userList) == 0 {
		return fmt.Errorf("mieru %s: no users configured", c.Tag)
	}
	c.userList = userList

	// Build mieru users from panel data — use Uuid as credential, consistent with other protocols
	mieruUsers := BuildMieruUsers(userList)

	// Determine transport protocol
	transportTCP := true
	if c.Info.Protocol.Transport == "udp" {
		transportTCP = false
	}

	// Create port binding
	port := int32(c.Info.Protocol.Port)
	protoEnum := appctlpb.TransportProtocol_TCP
	if !transportTCP {
		protoEnum = appctlpb.TransportProtocol_UDP
	}
	portBinding := &appctlpb.PortBinding{
		Port:     &port,
		Protocol: &protoEnum,
	}

	// Create underlay properties
	mtu := common.DefaultMTU
	endpoints, err := appctlcommon.PortBindingsToUnderlayProperties([]*appctlpb.PortBinding{portBinding}, mtu)
	if err != nil {
		return fmt.Errorf("mieru %s: create endpoints error: %w", c.Tag, err)
	}

	// Create mieru mux (server mode), apply multiplex/traffic pattern from panel config
	mux := protocol.NewMux(false).
		SetServerUsers(mieruUsers).
		SetEndpoints(endpoints)
	if tp := BuildTrafficPattern(c.Info.Protocol.Multiplex); tp != nil {
		mux.SetTrafficPattern(tp)
	}

	// Create socks5 server with client-side auth
	socks5Config := &socks5.Config{
		AuthOpts: socks5.Auth{
			ClientSideAuthentication: true,
		},
		HandshakeTimeout: 10 * time.Second,
		Users:            mieruUsers,
	}
	socks5Srv, err := socks5.New(socks5Config)
	if err != nil {
		return fmt.Errorf("mieru %s: create socks5 server error: %w", c.Tag, err)
	}

	// Start the mux
	if err := mux.Start(); err != nil {
		return fmt.Errorf("mieru %s: start mux error: %w", c.Tag, err)
	}

	c.mux = mux
	c.socks5Srv = socks5Srv
	c.running = true

	// Serve socks5 in background
	go func() {
		log.WithField("节点", c.Tag).Info("mieru socks5 server is running")
		if err := socks5Srv.Serve(mux); err != nil {
			log.WithField("节点", c.Tag).WithError(err).Error("mieru socks5 server stopped with error")
		}
		log.WithField("节点", c.Tag).Info("mieru socks5 server stopped")
	}()

	// Start periodic tasks — same pattern as XrayController
	c.startTasks()

	log.WithField("节点", c.Tag).Infof("mieru node started on port %d with %d users", c.Info.Protocol.Port, len(userList))
	return nil
}

// Close stops the mieru node.
func (c *MieruController) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.running {
		return nil
	}

	// Stop periodic tasks
	if c.UserListMonitorPeriodic != nil {
		c.UserListMonitorPeriodic.Close()
	}
	if c.UserReportPeriodic != nil {
		c.UserReportPeriodic.Close()
	}

	// Stop socks5 and mux
	if c.socks5Srv != nil {
		if err := c.socks5Srv.Close(); err != nil {
			log.WithField("节点", c.Tag).WithError(err).Warn("close socks5 server error")
		}
	}
	if c.mux != nil {
		if err := c.mux.Close(); err != nil {
			log.WithField("节点", c.Tag).WithError(err).Warn("close mux error")
		}
	}

	c.running = false
	log.WithField("节点", c.Tag).Info("mieru node stopped")
	return nil
}

// startTasks starts periodic tasks for user list monitoring and traffic reporting.
func (c *MieruController) startTasks() {
	c.UserListMonitorPeriodic = &task.Task{
		Name:     "mieruUserListMonitor",
		Interval: time.Duration(c.Info.PullInterval) * time.Second,
		Execute:  c.userListMonitor,
	}
	c.UserReportPeriodic = &task.Task{
		Name:     "mieruReportUserTraffic",
		Interval: time.Duration(c.Info.PushInterval) * time.Second,
		Execute:  c.reportUserTrafficTask,
	}
	_ = c.UserListMonitorPeriodic.Start(false)
	log.WithField("节点", c.Tag).Info("mieru 用户列表监控任务已启动")
	_ = c.UserReportPeriodic.Start(false)
	log.WithField("节点", c.Tag).Info("mieru 用户流量报告任务已启动")
}

// userListMonitor periodically fetches the user list from the panel and updates mieru users.
func (c *MieruController) userListMonitor(ctx context.Context) error {
	newU, err := c.ApiClient.GetUserList(ctx)
	if err != nil {
		log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Error("mieru: get user list failed")
		return nil
	}
	if newU == nil {
		return nil // 304 Not Modified
	}

	// Compare and update
	deleted, added := CompareUserList(c.userList, newU)
	if len(deleted) > 0 || len(added) > 0 {
		c.mu.Lock()
		mieruUsers := BuildMieruUsers(newU)
		c.mux.SetServerUsers(mieruUsers)
		c.userList = newU
		c.mu.Unlock()

		log.WithField("节点", c.Tag).Infof("mieru 用户列表已更新: 添加 %d, 删除 %d", len(added), len(deleted))
	}
	return nil
}

// reportUserTrafficTask periodically reports user traffic to the panel.
func (c *MieruController) reportUserTrafficTask(ctx context.Context) error {
	var userTraffic []panel.UserTraffic
	var threshold int64
	if c.Info.TrafficReportThreshold > 0 {
		threshold = int64(c.Info.TrafficReportThreshold)
	}

	// Read per-user traffic from mieru metrics and reset counters
	for _, u := range c.userList {
		userMetrics := metrics.GetMetricsForUser(u.Uuid)
		if userMetrics == nil {
			continue
		}
		var up, down int64
		for _, m := range userMetrics {
			switch m.Name() {
			case metrics.UserMetricUploadBytes:
				up = m.Load()
			case metrics.UserMetricDownloadBytes:
				down = m.Load()
			}
		}
		if up+down > threshold {
			// Reset counters after reading, so next report only contains new traffic
			for _, m := range userMetrics {
				switch m.Name() {
				case metrics.UserMetricUploadBytes, metrics.UserMetricDownloadBytes:
					m.Store(0)
				}
			}
			userTraffic = append(userTraffic, panel.UserTraffic{
				UID:      u.Id,
				Upload:   up,
				Download: down,
			})
		}
	}

	if len(userTraffic) > 0 {
		if err := c.ApiClient.ReportUserTraffic(ctx, &userTraffic); err != nil {
			log.WithField("tag", c.Tag).WithError(err).Info("mieru: report user traffic failed")
		} else {
			log.WithField("节点", c.Tag).Infof("mieru 已上报 %d 名用户消耗流量", len(userTraffic))
		}
	}

	// Report node status
	CPU, Mem, Disk, Uptime, err := serverstatus.GetSystemInfo()
	if err != nil {
		log.WithField("tag", c.Tag).WithError(err).Warn("mieru: get system info failed")
		return nil
	}
	if err := c.ApiClient.ReportNodeStatus(&panel.NodeStatus{
		CPU: CPU, Mem: Mem, Disk: Disk, Uptime: Uptime,
	}); err != nil {
		log.WithField("tag", c.Tag).WithError(err).Warn("mieru: report node status failed")
	}
	return nil
}

// BuildMieruUsers converts panel UserInfo list to mieru User map.
// Uses Uuid as the authentication credential, consistent with trojan/ss/vless/vmess.
func BuildMieruUsers(userList []panel.UserInfo) map[string]*appctlpb.User {
	mieruUsers := make(map[string]*appctlpb.User, len(userList))
	for _, u := range userList {
		password := u.Uuid
		if password == "" {
			password = fmt.Sprintf("user_%d", u.Id)
		}
		mieruUsers[fmt.Sprintf("%d", u.Id)] = &appctlpb.User{
			Name:     proto.String(u.Uuid),
			Password: proto.String(password),
		}
	}
	return mieruUsers
}

// BuildTrafficPattern converts the panel "multiplex" field to a mieru TrafficPattern config.
// Panel multiplex levels: "none", "low", "middle", "high"
func BuildTrafficPattern(multiplex string) *trafficpattern.Config {
	if multiplex == "" || multiplex == "none" {
		return nil
	}
	unlockAll := multiplex == "high"
	pattern := &appctlpb.TrafficPattern{
		UnlockAll: proto.Bool(unlockAll),
	}
	tp, err := trafficpattern.NewConfig(pattern)
	if err != nil {
		log.WithError(err).Warn("BuildTrafficPattern: invalid pattern, using default")
		return nil
	}
	return tp
}
