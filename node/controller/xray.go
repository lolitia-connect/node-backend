package controller

import (
	"context"
	"errors"
	"fmt"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/task"
	vCore "github.com/perfect-panel/ppanel-node/core"
	"github.com/perfect-panel/ppanel-node/limiter"
	log "github.com/sirupsen/logrus"
)

type XrayController struct {
	Server                  *vCore.XrayCore
	ApiClient               *panel.ClientV1
	Tag                     string
	Limiter                 *limiter.Limiter
	UserList                []panel.UserInfo
	AliveMap                map[int]int
	Info                    *panel.NodeInfo
	UserListMonitorPeriodic *task.Task
	UserReportPeriodic      *task.Task
	RenewCertPeriodic       *task.Task
	OnlineIpReportPeriodic  *task.Task
}

// NewXrayController return a Node controller with default parameters.
func NewXrayController(core *vCore.XrayCore, api *panel.ClientV1, info *panel.NodeInfo) *XrayController {
	return &XrayController{
		Server:    core,
		ApiClient: api,
		Info:      info,
	}
}

// Start implements the Start() function of the service interface.
func (c *XrayController) Start() error {
	var err error
	c.UserList, err = c.ApiClient.GetUserList(context.Background())
	if err != nil {
		return fmt.Errorf("get user list error: %s", err)
	}
	if len(c.UserList) == 0 {
		return errors.New("add users error: not have any user")
	}
	c.AliveMap, err = c.ApiClient.GetUserAlive()
	if err != nil {
		return fmt.Errorf("failed to get user alive list: %s", err)
	}
	c.Tag = c.BuildNodeTag(c.Info)

	l := limiter.AddLimiter(c.Tag, c.UserList, c.AliveMap)
	c.Limiter = l

	if c.Info.Protocol.Security == "tls" {
		err = c.RequestCert()
		if err != nil {
			return fmt.Errorf("request cert error: %s", err)
		}
	}

	err = c.Server.AddNode(c.Tag, c.Info)
	if err != nil {
		return fmt.Errorf("add new node error: %s", err)
	}
	added, err := c.Server.AddUsers(&vCore.AddUsersParams{
		Tag:      c.Tag,
		Users:    c.UserList,
		NodeInfo: c.Info,
	})
	if err != nil {
		return fmt.Errorf("add users error: %s", err)
	}
	log.WithField("节点", c.Tag).Infof("已添加 %d 个新用户", added)
	c.StartTasks(c.Info)
	return nil
}

// Close implements the Close() function of the service interface.
func (c *XrayController) Close() error {
	limiter.DeleteLimiter(c.Tag)
	if c.UserListMonitorPeriodic != nil {
		c.UserListMonitorPeriodic.Close()
	}
	if c.UserReportPeriodic != nil {
		c.UserReportPeriodic.Close()
	}
	if c.RenewCertPeriodic != nil {
		c.RenewCertPeriodic.Close()
	}
	if c.OnlineIpReportPeriodic != nil {
		c.OnlineIpReportPeriodic.Close()
	}
	err := c.Server.DelNode(c.Tag)
	if err != nil {
		return fmt.Errorf("del node error: %s", err)
	}
	return nil
}

func (c *XrayController) BuildNodeTag(node *panel.NodeInfo) string {
	return fmt.Sprintf("[%s]-%s:%d", c.ApiClient.APIHost, node.Type, node.Id)
}
