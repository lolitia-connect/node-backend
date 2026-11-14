package node

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	apicommon "github.com/enfein/mieru/v3/apis/common"
	"github.com/enfein/mieru/v3/apis/trafficpattern"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlcommon"
	"github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	"github.com/enfein/mieru/v3/pkg/common"
	"github.com/enfein/mieru/v3/pkg/protocol"
	"github.com/enfein/mieru/v3/pkg/socks5"
	"google.golang.org/protobuf/proto"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/logx"
	"github.com/perfect-panel/ppanel-node/common/serverstatus"
	"github.com/perfect-panel/ppanel-node/common/task"
	"github.com/perfect-panel/ppanel-node/conf"
)

// MieruController manages a mieru proxy node, parallel to XrayController.
type MieruController struct {
	mux       *protocol.Mux
	socks5Srv *socks5.Server
	tag       string
	info      *panel.NodeInfo
	config    *conf.Conf
	apiClient *panel.NodeClient
	userList  []panel.UserInfo
	userIDs   map[string]int

	userListMonitorPeriodic *task.Task
	userReportPeriodic      *task.Task
	onlinePeriodic          *task.Task

	pendingTraffic map[int]panel.UserTraffic
	pendingOnline  map[panel.OnlineUser]uint64
	onlineSequence uint64
	connections    map[*mieruConn]struct{}
	serveDone      chan struct{}
	traffic        *trafficQueue
	prepared       bool

	mu      sync.Mutex
	running bool
}

// NewMieruController creates a new mieru controller.
func NewMieruController(config *conf.Conf, apiClient *panel.NodeClient, info *panel.NodeInfo) *MieruController {
	protocolID := info.Type
	if info.Protocol != nil && info.Protocol.ID != "" {
		protocolID = info.Protocol.ID
	}
	tag := fmt.Sprintf("[%s]-%s(%s):%d", apiClient.APIHost, info.Type, protocolID, info.Id)
	return &MieruController{
		tag:            tag,
		info:           info,
		config:         config,
		apiClient:      apiClient,
		pendingTraffic: make(map[int]panel.UserTraffic),
		pendingOnline:  make(map[panel.OnlineUser]uint64),
		connections:    make(map[*mieruConn]struct{}),
		traffic:        newTrafficQueue(),
	}
}

func (c *MieruController) Prepare() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.prepared {
		return nil
	}
	users, err := c.apiClient.GetUserList(context.Background())
	if err != nil {
		return fmt.Errorf("mieru %s: get user list: %w", c.tag, err)
	}
	c.userList = users
	c.updateUserIDs(users)
	c.prepared = true
	return nil
}

// Start starts the mieru node.
func (c *MieruController) Start() error {
	if err := c.Prepare(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.running {
		return fmt.Errorf("mieru node %s is already running", c.tag)
	}

	mieruUsers := buildMieruUsers(c.userList)
	if len(mieruUsers) > 0 {
		if err := c.startProxy(mieruUsers); err != nil {
			return err
		}
	}
	c.running = true
	c.startTasks()
	logx.Mieru(c.tag).Infof("mieru node started on port %d with %d users", c.info.Protocol.Port, len(c.userList))
	return nil
}

func (c *MieruController) startProxy(mieruUsers map[string]*appctlpb.User) error {
	// Determine transport protocol
	transportTCP := true
	if strings.EqualFold(c.info.Protocol.Transport, "udp") {
		transportTCP = false
	}

	// Create port binding
	port := int32(c.info.Protocol.Port)
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
	endpoints, err := appctlcommon.AddrPortToUnderlayProperties("", []*appctlpb.PortBinding{portBinding}, mtu)
	if err != nil {
		return fmt.Errorf("mieru %s: create endpoints error: %w", c.tag, err)
	}

	// Create mieru mux (server mode), apply multiplex/traffic pattern from panel config
	mux := protocol.NewMux(false).
		SetServerUsers(mieruUsers).
		SetEndpoints(endpoints)
	if tp := buildTrafficPattern(c.info.Protocol.Multiplex); tp != nil {
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
		return fmt.Errorf("mieru %s: create socks5 server error: %w", c.tag, err)
	}

	// Start the mux
	if err := mux.Start(); err != nil {
		_ = mux.Close()
		_ = socks5Srv.Close()
		return fmt.Errorf("mieru %s: start mux error: %w", c.tag, err)
	}

	c.mux = mux
	c.socks5Srv = socks5Srv
	c.serveDone = make(chan struct{})

	go c.serve(mux, socks5Srv, c.serveDone)
	return nil
}

// Close stops the mieru node.
func (c *MieruController) Close() error {
	return c.Stop()
}

func (c *MieruController) Stop() error {
	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	monitor, report, online := c.userListMonitorPeriodic, c.userReportPeriodic, c.onlinePeriodic
	c.userListMonitorPeriodic, c.userReportPeriodic, c.onlinePeriodic = nil, nil, nil
	c.running = false
	mux, server, done := c.mux, c.socks5Srv, c.serveDone
	connections := make([]*mieruConn, 0, len(c.connections))
	for connection := range c.connections {
		connections = append(connections, connection)
	}
	c.mu.Unlock()
	if monitor != nil {
		monitor.Close()
	}
	if report != nil {
		report.Close()
	}
	if online != nil {
		online.Close()
	}
	for _, connection := range connections {
		_ = connection.Close()
	}
	if server != nil {
		_ = server.Close()
	}
	if mux != nil {
		if err := mux.Close(); err != nil {
			logx.Mieru(c.tag).WithError(err).Warn("close mux error")
		}
	}
	if done != nil {
		<-done
	}
	c.mu.Lock()
	c.mux, c.socks5Srv, c.serveDone = nil, nil, nil
	c.mu.Unlock()
	c.collectTraffic(0)
	logx.Mieru(c.tag).Info("mieru node stopped")
	return nil
}

// startTasks starts periodic tasks for user list monitoring and traffic reporting.
func (c *MieruController) startTasks() {
	c.userListMonitorPeriodic = &task.Task{
		Name:     "mieruUserListMonitor",
		Interval: time.Duration(c.info.PullInterval) * time.Second,
		Execute:  c.userListMonitor,
	}
	c.userReportPeriodic = &task.Task{
		Name:     "mieruReportUserTraffic",
		Interval: time.Duration(c.info.PushInterval) * time.Second,
		Execute:  c.reportUserTrafficTask,
	}
	c.onlinePeriodic = &task.Task{
		Name: "mieruReportOnline", Interval: time.Duration(min(max(c.info.PushInterval, 1), 60)) * time.Second,
		Execute: c.reportOnline,
	}
	_ = c.userListMonitorPeriodic.Start(false)
	logx.Mieru(c.tag).Info("mieru 用户列表监控任务已启动")
	_ = c.userReportPeriodic.Start(false)
	logx.Mieru(c.tag).Info("mieru 用户流量报告任务已启动")
	_ = c.onlinePeriodic.Start(false)
}

// userListMonitor periodically fetches the user list from the panel and updates mieru users.
func (c *MieruController) userListMonitor(ctx context.Context) error {
	newU, err := c.apiClient.GetUserList(ctx)
	if err != nil {
		logx.Mieru(c.tag).WithError(err).Error("mieru: get user list failed")
		return nil
	}
	if newU == nil {
		return nil // 304 Not Modified
	}

	c.mu.Lock()
	if !c.running {
		c.mu.Unlock()
		return nil
	}
	deleted, added := compareUserList(c.userList, newU)
	var revoked []*mieruConn
	if len(deleted) > 0 || len(added) > 0 {
		c.collectTrafficLocked(0)
		mieruUsers := buildMieruUsers(newU)
		if c.mux != nil {
			c.mux.SetServerUsers(mieruUsers)
		} else if len(mieruUsers) > 0 {
			if err := c.startProxy(mieruUsers); err != nil {
				c.apiClient.ResetUserETag()
				c.mu.Unlock()
				return err
			}
		}
		c.userList = newU
		c.updateUserIDs(newU)
		for connection := range c.connections {
			for _, user := range deleted {
				if connection.UserName() == user.Uuid {
					revoked = append(revoked, connection)
					break
				}
			}
		}

		logx.Mieru(c.tag).Infof("mieru 用户列表已更新: 添加 %d, 删除 %d", len(added), len(deleted))
	}
	c.mu.Unlock()
	for _, connection := range revoked {
		_ = connection.Close()
	}
	return nil
}

// reportUserTrafficTask periodically reports user traffic to the panel.
func (c *MieruController) reportUserTrafficTask(ctx context.Context) error {
	CPU, Mem, Disk, Uptime, err := serverstatus.GetSystemInfo()
	if err != nil {
		return err
	}
	return c.apiClient.ReportNodeStatusContext(ctx, &panel.NodeStatus{
		CPU: CPU, Mem: Mem, Disk: Disk, Uptime: Uptime,
	})
}

func (c *MieruController) reportOnline(ctx context.Context) error {
	c.mu.Lock()
	users := make(map[panel.OnlineUser]uint64, len(c.pendingOnline))
	for user, sequence := range c.pendingOnline {
		users[user] = sequence
	}
	for connection := range c.connections {
		if c.running && connection.online.UID != 0 && c.userIDs[connection.UserName()] == connection.online.UID {
			users[connection.online] = c.pendingOnline[connection.online]
		}
	}
	c.mu.Unlock()
	online := make([]panel.OnlineUser, 0, len(users))
	for user := range users {
		online = append(online, user)
	}
	for start := 0; start < len(online); start += 10000 {
		batch := online[start:min(start+10000, len(online))]
		if err := c.apiClient.ReportNodeOnlineUsers(ctx, &batch); err != nil {
			return err
		}
		c.mu.Lock()
		for _, reported := range batch {
			if c.pendingOnline[reported] == users[reported] {
				delete(c.pendingOnline, reported)
			}
		}
		c.mu.Unlock()
	}
	return nil
}

func (c *MieruController) collectTraffic(threshold int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.collectTrafficLocked(threshold)
}

func (c *MieruController) collectTrafficLocked(threshold int64) {
	var userTraffic []panel.UserTraffic
	for userID, traffic := range c.pendingTraffic {
		if traffic.Upload+traffic.Download > threshold {
			userTraffic = append(userTraffic, traffic)
			delete(c.pendingTraffic, userID)
		}
	}

	c.traffic.add(c.apiClient, userTraffic)
}

// buildMieruUsers converts panel UserInfo list to mieru User map.
// Uses Uuid as the authentication credential, consistent with trojan/ss/vless/vmess.
func buildMieruUsers(userList []panel.UserInfo) map[string]*appctlpb.User {
	mieruUsers := make(map[string]*appctlpb.User, len(userList))
	for _, u := range userList {
		if u.Uuid == "" {
			logx.Component("mieru").WithField("user_id", u.Id).Error("user UUID is empty, skipping")
			continue
		}
		password := u.Uuid
		mieruUsers[password] = &appctlpb.User{
			Name:     proto.String(password), // same as password
			Password: proto.String(password),
		}
	}
	return mieruUsers
}

func (c *MieruController) serve(mux *protocol.Mux, server *socks5.Server, done chan struct{}) {
	var handlers sync.WaitGroup
	defer close(done)
	defer handlers.Wait()
	for {
		connection, err := mux.Accept()
		if err != nil {
			return
		}
		c.mu.Lock()
		if !c.running {
			c.mu.Unlock()
			_ = connection.Close()
			return
		}
		counted := &mieruConn{Conn: connection, controller: c, users: c.userIDs}
		c.connections[counted] = struct{}{}
		c.mu.Unlock()
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			defer func() {
				c.mu.Lock()
				delete(c.connections, counted)
				c.mu.Unlock()
			}()
			_ = server.ServeConn(counted)
		}()
	}
}

type mieruConn struct {
	net.Conn
	controller *MieruController
	users      map[string]int
	online     panel.OnlineUser
}

func (c *MieruController) updateUserIDs(users []panel.UserInfo) {
	identities := make(map[string]int, len(users))
	validIDs := make(map[int]struct{}, len(users))
	for _, user := range users {
		if user.Uuid != "" {
			identities[user.Uuid] = user.Id
			validIDs[user.Id] = struct{}{}
		}
	}
	c.userIDs = identities
	for user := range c.pendingOnline {
		if _, valid := validIDs[user.UID]; !valid {
			delete(c.pendingOnline, user)
		}
	}
}

func (connection *mieruConn) UserName() string {
	if user, ok := connection.Conn.(apicommon.UserContext); ok {
		return user.UserName()
	}
	return ""
}

func (connection *mieruConn) Read(buffer []byte) (int, error) {
	count, err := connection.Conn.Read(buffer)
	if count > 0 {
		userName := connection.UserName()
		userID, known := connection.users[userName]
		controller := connection.controller
		controller.mu.Lock()
		currentID, current := controller.userIDs[userName]
		allowed := controller.running && known && current && currentID == userID
		controller.mu.Unlock()
		if !allowed {
			_ = connection.Close()
			return 0, errors.New("mieru user is no longer authorized")
		}
	}
	connection.record(int64(count), 0)
	return count, err
}

func (connection *mieruConn) Write(buffer []byte) (int, error) {
	count, err := connection.Conn.Write(buffer)
	connection.record(0, int64(count))
	return count, err
}

func (connection *mieruConn) record(upload, download int64) {
	userID, ok := connection.users[connection.UserName()]
	if !ok || upload+download == 0 {
		return
	}
	controller := connection.controller
	controller.mu.Lock()
	defer controller.mu.Unlock()
	if connection.online.UID == 0 && controller.running && controller.userIDs[connection.UserName()] == userID {
		if host, _, err := net.SplitHostPort(connection.RemoteAddr().String()); err == nil {
			if address, err := netip.ParseAddr(host); err == nil {
				connection.online = panel.OnlineUser{UID: userID, IP: address.Unmap().String()}
				controller.onlineSequence++
				controller.pendingOnline[connection.online] = controller.onlineSequence
			}
		}
	}
	traffic := controller.pendingTraffic[userID]
	traffic.UID = userID
	traffic.Upload += upload
	traffic.Download += download
	controller.pendingTraffic[userID] = traffic
}

// buildTrafficPattern converts the panel "multiplex" field to a mieru TrafficPattern config.
// Panel multiplex levels: "none", "low", "middle", "high"
func buildTrafficPattern(multiplex string) *trafficpattern.Config {
	if multiplex == "" || multiplex == "none" {
		return nil
	}
	unlockAll := multiplex == "high"
	pattern := &appctlpb.TrafficPattern{
		UnlockAll: proto.Bool(unlockAll),
	}
	tp, err := trafficpattern.NewConfig(pattern)
	if err != nil {
		logx.Component("mieru").WithError(err).Warn("buildTrafficPattern: invalid pattern, using default")
		return nil
	}
	return tp
}
