package controller

import (
	"context"
	"strconv"
	"time"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/serverstatus"
	"github.com/perfect-panel/ppanel-node/common/task"
	vCore "github.com/perfect-panel/ppanel-node/core"
	log "github.com/sirupsen/logrus"
)

// StartTasks starts periodic tasks for the XrayController.
func (c *XrayController) StartTasks(node *panel.NodeInfo) {
	c.UserListMonitorPeriodic = &task.Task{
		Name:     "userListMonitor",
		Interval: time.Duration(node.PullInterval) * time.Second,
		Execute:  c.userListMonitor,
		ReloadCh: c.Server.ReloadCh,
	}
	c.UserReportPeriodic = &task.Task{
		Name:     "reportUserTraffic",
		Interval: time.Duration(node.PushInterval) * time.Second,
		Execute:  c.reportUserTrafficTask,
		ReloadCh: c.Server.ReloadCh,
	}
	_ = c.UserListMonitorPeriodic.Start(false)
	log.WithField("节点", c.Tag).Info("用户列表监控任务已启动")
	_ = c.UserReportPeriodic.Start(false)
	log.WithField("节点", c.Tag).Info("用户流量报告任务已启动")

	var security string
	switch node.Type {
	case "vless", "vmess", "trojan":
		security = node.Protocol.Security
	case "tuic", "hysteria", "hysteria2":
		security = "tls"
	default:
		security = ""
	}

	if security == "tls" {
		switch node.Protocol.CertMode {
		case "none", "", "file", "self":
		default:
			c.RenewCertPeriodic = &task.Task{
				Name:     "renewCert",
				Interval: time.Hour * 24,
				Execute:  c.renewCertTask,
				ReloadCh: c.Server.ReloadCh,
			}
			log.WithField("节点", c.Tag).Info("证书定期更新任务已启动")
			_ = c.RenewCertPeriodic.Start(true)
		}
	}
}

func (c *XrayController) reloadTask() {
	c.UserListMonitorPeriodic.Close()
	c.UserReportPeriodic.Close()
	if c.RenewCertPeriodic != nil {
		c.RenewCertPeriodic.Close()
	}
	c.StartTasks(c.Info)
}

func (c *XrayController) userListMonitor(ctx context.Context) (err error) {
	newU, err := c.ApiClient.GetUserList(ctx)
	if err != nil {
		log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Error("Get user list failed")
		return nil
	}
	newA, err := c.ApiClient.GetUserAlive()
	if err != nil {
		log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Error("Get alive list failed")
		return nil
	}
	if newA != nil {
		c.Limiter.AliveList = newA
	}
	if newU == nil {
		return nil
	}

	deleted, added := CompareUserList(c.UserList, newU)
	if len(deleted) > 0 {
		err = c.Server.DelUsers(deleted, c.Tag, c.Info)
		if err != nil {
			log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Error("Delete users failed")
			return nil
		}
	}
	if len(added) > 0 {
		_, err = c.Server.AddUsers(&vCore.AddUsersParams{
			Tag:      c.Tag,
			NodeInfo: c.Info,
			Users:    added,
		})
		if err != nil {
			log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Error("Add users failed")
			return nil
		}
	}
	if len(added) > 0 || len(deleted) > 0 {
		c.Limiter.UpdateUser(c.Tag, added, deleted)
		c.UserList = newU
		log.WithField("节点", c.Tag).Infof("删除 %d 个用户，新增 %d 个用户", len(deleted), len(added))
	}
	return nil
}

func (c *XrayController) reportUserTrafficTask(ctx context.Context) (err error) {
	var reportmin int
	if c.Info.TrafficReportThreshold > 0 {
		reportmin = c.Info.TrafficReportThreshold
	}
	userTraffic, _ := c.Server.GetUserTrafficSlice(c.Tag, reportmin)
	if len(userTraffic) > 0 {
		err = c.ApiClient.ReportUserTraffic(ctx, &userTraffic)
		if err != nil {
			log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Info("Report user traffic failed")
		} else {
			log.WithField("节点", c.Tag).Infof("已上报 %d 名用户消耗流量", len(userTraffic))
		}
	}

	if onlineDevice, err := c.Limiter.GetOnlineDevice(); err != nil {
		log.Print(err)
	} else if len(*onlineDevice) > 0 {
		var result []panel.OnlineUser
		nocountUID := make(map[int]struct{})
		for _, traffic := range userTraffic {
			if traffic.Upload+traffic.Download <= 0 {
				nocountUID[traffic.UID] = struct{}{}
			}
		}
		for _, online := range *onlineDevice {
			if _, ok := nocountUID[online.UID]; !ok {
				result = append(result, online)
			}
		}
		if err = c.ApiClient.ReportNodeOnlineUsers(ctx, &result); err != nil {
			log.WithFields(log.Fields{"tag": c.Tag, "err": err}).Info("Report online users failed")
		} else {
			log.WithField("节点", c.Tag).Infof("总计 %d 名在线用户, %d 名已上报", len(*onlineDevice), len(result))
		}
	}

	CPU, Mem, Disk, Uptime, err := serverstatus.GetSystemInfo()
	if err != nil {
		log.Print(err)
	}
	if err = c.ApiClient.ReportNodeStatus(&panel.NodeStatus{
		CPU: CPU, Mem: Mem, Disk: Disk, Uptime: Uptime,
	}); err != nil {
		log.Print(err)
	}
	return nil
}

// CompareUserList compares two user lists and returns deleted and added users.
// Shared by XrayController and MieruController.
func CompareUserList(old, new []panel.UserInfo) (deleted, added []panel.UserInfo) {
	oldMap := make(map[string]int)
	for i, user := range old {
		key := user.Uuid + strconv.Itoa(user.SpeedLimit)
		oldMap[key] = i
	}
	for _, user := range new {
		key := user.Uuid + strconv.Itoa(user.SpeedLimit)
		if _, exists := oldMap[key]; !exists {
			added = append(added, user)
		} else {
			delete(oldMap, key)
		}
	}
	for _, index := range oldMap {
		deleted = append(deleted, old[index])
	}
	return
}
