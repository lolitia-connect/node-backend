package node

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/logx"
	"github.com/perfect-panel/ppanel-node/common/task"
	vCore "github.com/perfect-panel/ppanel-node/core"
	inboundbuilder "github.com/perfect-panel/ppanel-node/core/inbound"
	"github.com/perfect-panel/ppanel-node/limiter"
	xray "github.com/xtls/xray-core/core"
)

type Controller struct {
	server                  *vCore.XrayCore
	apiClient               *panel.NodeClient
	tag                     string
	limiter                 *limiter.Limiter
	userList                []panel.UserInfo
	aliveMap                map[int]int
	localDeviceLimits       bool
	info                    *panel.NodeInfo
	userListMonitorPeriodic *task.Task
	onlinePeriodic          *task.Task
	renewCertPeriodic       *task.Task
	traffic                 *trafficQueue
	prepared                bool
	started                 bool
	certificateReload       atomic.Bool
	inboundConfig           *xray.InboundHandlerConfig
}

func NewController(core *vCore.XrayCore, api *panel.NodeClient, info *panel.NodeInfo) *Controller {
	controller := &Controller{
		server:    core,
		apiClient: api,
		info:      info,
		traffic:   newTrafficQueue(),
	}
	return controller
}

// Prepare completes network requests and builds the inbound before stopping a
// running node. The cached certificate and user snapshot also support rollback.
func (c *Controller) Prepare() error {
	if c.prepared {
		return nil
	}
	var err error
	c.tag = c.buildNodeTag(c.info)
	selfCertificate := usesTLSCertificate(c.info) && strings.TrimSpace(c.info.Protocol.CertMode) == "self"
	if selfCertificate {
		if err = c.requestCert(); err != nil {
			return fmt.Errorf("request self-signed certificate: %w", err)
		}
		if err = c.reportSelfCertificateSHA256(); err != nil {
			return fmt.Errorf("report self-signed certificate fingerprint: %w", err)
		}
	}

	// Update user
	c.userList, err = c.apiClient.GetUserList(context.Background())
	if err != nil {
		return fmt.Errorf("get user list error: %s", err)
	}
	c.aliveMap, err = c.apiClient.GetUserAlive(context.Background())
	if errors.Is(err, panel.ErrDeviceAPIUnsupported) {
		c.localDeviceLimits = true
		c.aliveMap = make(map[int]int)
	} else if err != nil {
		return fmt.Errorf("failed to get user alive list: %s", err)
	}
	if usesTLSCertificate(c.info) && !selfCertificate {
		err = c.requestCert()
		if err != nil {
			return fmt.Errorf("request cert error: %s", err)
		}
	}
	c.inboundConfig, err = inboundbuilder.Build(c.info, c.tag)
	if err != nil {
		return fmt.Errorf("build inbound: %w", err)
	}
	c.prepared = true
	return nil
}

func (c *Controller) Start() error {
	if c.started {
		return nil
	}
	if err := c.Prepare(); err != nil {
		return err
	}
	if err := c.server.AddNodeConfig(c.inboundConfig); err != nil {
		return err
	}
	c.started = true
	if c.limiter == nil {
		c.limiter = c.server.LimiterManager.Add(c.tag, c.userList, c.aliveMap, c.info.Type)
		if !c.localDeviceLimits {
			c.limiter.SetDeviceAdmission(c.apiClient.ClaimDevice)
		}
	} else {
		c.server.LimiterManager.Set(c.tag, c.limiter)
	}
	added, err := c.server.AddUsers(&vCore.AddUsersParams{
		Tag:      c.tag,
		Users:    c.userList,
		NodeInfo: c.info,
	})
	if err != nil {
		return fmt.Errorf("add users error: %s", err)
	}
	logx.Xray(c.tag).WithField("user_added", added).Info("已添加新用户")
	c.startTasks(c.info)
	return nil
}

// Stop releases listeners while retaining prepared configuration, users,
// limiters and pending traffic so Start can restore service without the panel.
func (c *Controller) Stop() error {
	if c == nil {
		return nil
	}
	if c.onlinePeriodic != nil {
		c.onlinePeriodic.Close()
		c.onlinePeriodic = nil
	}
	if c.userListMonitorPeriodic != nil {
		c.userListMonitorPeriodic.Close()
		c.userListMonitorPeriodic = nil
	}
	if c.renewCertPeriodic != nil {
		c.renewCertPeriodic.Close()
		c.renewCertPeriodic = nil
	}
	if !c.started {
		return nil
	}
	if err := c.server.DelNode(c.tag); err != nil {
		c.startTasks(c.info)
		return err
	}
	c.started = false
	c.collectTraffic(0)
	c.server.ClearNodeUsers(c.tag)
	c.server.LimiterManager.Delete(c.tag)
	return nil
}

func (c *Controller) Close() error {
	return c.Stop()
}

func (c *Controller) buildNodeTag(node *panel.NodeInfo) string {
	protocolID := node.Type
	if node.Protocol != nil && node.Protocol.ID != "" {
		protocolID = node.Protocol.ID
	}
	return fmt.Sprintf("[%s]-%s(%s):%d", c.apiClient.APIHost, node.Type, protocolID, node.Id)
}
