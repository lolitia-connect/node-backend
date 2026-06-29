package node

import (
	"fmt"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/common/logx"
	"github.com/perfect-panel/ppanel-node/conf"
	vCore "github.com/perfect-panel/ppanel-node/core"
)

type Node struct {
	controllers  []*Controller
	mieruNodes   []*MieruController
}

func New(core *vCore.XrayCore, config *conf.Conf, serverconfig *panel.ServerConfigResponse) (*Node, error) {
	node := &Node{
		controllers: make([]*Controller, 0),
		mieruNodes:  make([]*MieruController, 0),
	}
	pushinterval := serverconfig.Data.PushInterval
	if pushinterval <= 0 {
		pushinterval = 60
	}
	pullinterval := serverconfig.Data.PullInterval
	if pullinterval <= 0 {
		pullinterval = 60
	}
	for _, nodeconfig := range *serverconfig.Data.Protocols {
		n := &panel.NodeInfo{
			Id:                     config.ApiConfig.ServerId,
			Type:                   nodeconfig.Type,
			TrafficReportThreshold: serverconfig.Data.TrafficReportThreshold,
			PushInterval:           pushinterval,
			PullInterval:           pullinterval,
			Protocol:               &nodeconfig,
		}
		p, err := panel.NewNodeClient(&conf.NodeApiConfig{
			APIHost:   config.ApiConfig.ApiHost,
			NodeType:  nodeconfig.Type,
			NodeID:    config.ApiConfig.ServerId,
			SecretKey: config.ApiConfig.SecretKey,
		})
		if err != nil {
			return nil, err
		}
		if nodeconfig.Type == "mieru" {
			node.mieruNodes = append(node.mieruNodes, NewMieruController(config, p, n))
		} else {
			node.controllers = append(node.controllers, NewController(core, p, n))
		}
	}

	return node, nil
}

func (n *Node) Start() error {
	for i := range n.controllers {
		if !n.controllers[i].info.Protocol.Enable {
			continue
		}
		err := n.controllers[i].Start()
		if err != nil {
			return fmt.Errorf("启动节点 [%s-%s-%d] 失败: %s",
				n.controllers[i].apiClient.APIHost,
				n.controllers[i].info.Type,
				n.controllers[i].info.Id,
				err)
		}
	}
	for i := range n.mieruNodes {
		if !n.mieruNodes[i].info.Protocol.Enable {
			continue
		}
		if err := n.mieruNodes[i].Start(); err != nil {
			return fmt.Errorf("启动 mieru 节点 [%s] 失败: %w",
				n.mieruNodes[i].tag, err)
		}
	}
	return nil
}

func (n *Node) Close() {
	if n == nil {
		return
	}
	for _, c := range n.controllers {
		if c == nil {
			continue
		}
		if err := c.Close(); err != nil {
			logx.Node(c.tag).WithError(err).Error("关闭节点控制器失败")
		}
	}
	n.controllers = nil

	for _, m := range n.mieruNodes {
		if m == nil {
			continue
		}
		if err := m.Close(); err != nil {
			logx.Node(m.tag).WithError(err).Error("关闭 mieru 节点失败")
		}
	}
	n.mieruNodes = nil
}
