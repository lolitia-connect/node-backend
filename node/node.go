package node

import (
	"fmt"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/conf"
	vCore "github.com/perfect-panel/ppanel-node/core"
	"github.com/perfect-panel/ppanel-node/node/controller"
	log "github.com/sirupsen/logrus"
)

type Node struct {
	xrayNodes  []*controller.XrayController
	mieruNodes []*controller.MieruController
}

func New(core *vCore.XrayCore, config *conf.Conf, serverconfig *panel.ServerConfigResponse) (*Node, error) {
	node := &Node{
		xrayNodes:  make([]*controller.XrayController, 0),
		mieruNodes: make([]*controller.MieruController, 0),
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
		p, err := panel.NewClientV1(&conf.NodeApiConfig{
			APIHost:   config.ApiConfig.ApiHost,
			NodeType:  nodeconfig.Type,
			NodeID:    config.ApiConfig.ServerId,
			SecretKey: config.ApiConfig.SecretKey,
		})
		if err != nil {
			return nil, err
		}

		if nodeconfig.Type == "mieru" {
			node.mieruNodes = append(node.mieruNodes, controller.NewMieruController(config, p, n))
		} else {
			node.xrayNodes = append(node.xrayNodes, controller.NewXrayController(core, p, n))
		}
	}

	return node, nil
}

func (n *Node) Start() error {
	for i := range n.xrayNodes {
		if !n.xrayNodes[i].Info.Protocol.Enable {
			continue
		}
		err := n.xrayNodes[i].Start()
		if err != nil {
			return fmt.Errorf("启动节点 [%s-%s-%d] 失败: %s",
				n.xrayNodes[i].ApiClient.APIHost,
				n.xrayNodes[i].Info.Type,
				n.xrayNodes[i].Info.Id,
				err)
		}
	}
	for i := range n.mieruNodes {
		if !n.mieruNodes[i].Info.Protocol.Enable {
			continue
		}
		if err := n.mieruNodes[i].Start(); err != nil {
			return fmt.Errorf("启动 mieru 节点 [%s] 失败: %w",
				n.mieruNodes[i].Tag, err)
		}
	}
	return nil
}

func (n *Node) Close() {
	for _, c := range n.xrayNodes {
		if err := c.Close(); err != nil {
			panic(err)
		}
	}
	n.xrayNodes = nil

	for _, m := range n.mieruNodes {
		if err := m.Close(); err != nil {
			log.WithError(err).Warn("close mieru node error")
		}
	}
	n.mieruNodes = nil
}
