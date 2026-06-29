package inbound

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/core"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

// Build builds Inbound config for different protocol.
func Build(nodeInfo *panel.NodeInfo, tag string) (*core.InboundHandlerConfig, error) {
	in := &coreConf.InboundDetourConfig{}
	var err error
	switch nodeInfo.Type {
	case "vless":
		err = buildVLess(nodeInfo, in)
	case "vmess":
		err = buildVMess(nodeInfo, in)
	case "trojan":
		err = buildTrojan(nodeInfo, in)
	case "shadowsocks":
		err = buildShadowsocks(nodeInfo, in)
	case "hysteria2", "hysteria":
		err = buildHysteria2(nodeInfo, in)
	case "tuic":
		err = buildTuic(nodeInfo, in)
	case "anytls":
		err = buildAnyTLS(nodeInfo, in)
	default:
		return nil, fmt.Errorf("unsupported node type: %s", nodeInfo.Type)
	}
	if err != nil {
		return nil, err
	}
	// Set network protocol
	// Set server port
	in.PortList = &coreConf.PortList{
		Range: []coreConf.PortRange{
			{
				From: uint32(nodeInfo.Protocol.Port),
				To:   uint32(nodeInfo.Protocol.Port),
			}},
	}
	// Set Listen IP address
	ipAddress := net.ParseAddress("0.0.0.0")
	in.ListenOn = &coreConf.Address{Address: ipAddress}
	// Set SniffingConfig
	sniffingConfig := &coreConf.SniffingConfig{
		Enabled:      true,
		DestOverride: coreConf.StringList{"http", "tls", "quic"},
	}
	in.SniffingConfig = sniffingConfig

	// Set TLS or Reality settings
	switch nodeInfo.Protocol.Security {
	case "tls":
		switch nodeInfo.Protocol.CertMode {
		case "none", "":
			break
		default:
			if in.StreamSetting == nil {
				in.StreamSetting = &coreConf.StreamConfig{}
			}
			in.StreamSetting.Security = "tls"
			in.StreamSetting.TLSSettings = &coreConf.TLSConfig{
				Certs: []*coreConf.TLSCertConfig{
					{
						CertFile: filepath.Join("/etc/PPanel-node/", nodeInfo.Type+strconv.Itoa(nodeInfo.Id)+".cer"),
						KeyFile:  filepath.Join("/etc/PPanel-node/", nodeInfo.Type+strconv.Itoa(nodeInfo.Id)+".key"),
					},
				},
			}
		}
	case "reality":
		if in.StreamSetting == nil {
			in.StreamSetting = &coreConf.StreamConfig{}
		}
		in.StreamSetting.Security = "reality"
		v := nodeInfo.Protocol
		add := v.RealityServerAddr
		if add == "" {
			add = v.SNI
		}
		d, err := json.Marshal(fmt.Sprintf(
			"%s:%d",
			add,
			v.RealityServerPort))
		if err != nil {
			return nil, fmt.Errorf("marshal reality dest error: %s", err)
		}
		in.StreamSetting.REALITYSettings = &coreConf.REALITYConfig{
			Dest:        d,
			Xver:        uint64(0),
			Show:        false,
			ServerNames: []string{v.SNI},
			PrivateKey:  v.RealityPrivateKey,
			ShortIds:    []string{v.RealityShortID},
			//Mldsa65Seed: v.RealityMldsa65Seed,
		}
	default:
		break
	}
	in.Tag = tag
	return in.Build()
}

func buildTransportSetting(nodeInfo *panel.NodeInfo) (*coreConf.StreamConfig, error) {
	t := coreConf.TransportProtocol(nodeInfo.Protocol.Transport)
	stream := &coreConf.StreamConfig{Network: &t}
	switch nodeInfo.Protocol.Transport {
	case "tcp":
		stream.TCPSettings = &coreConf.TCPConfig{
			AcceptProxyProtocol: nodeInfo.Protocol.AcceptProxyProtocol,
		}
	case "ws", "websocket":
		stream.WSSettings = &coreConf.WebSocketConfig{
			Host:                nodeInfo.Protocol.Host,
			Path:                nodeInfo.Protocol.Path,
			AcceptProxyProtocol: nodeInfo.Protocol.AcceptProxyProtocol,
		}
	case "grpc":
		stream.GRPCSettings = &coreConf.GRPCConfig{
			ServiceName: nodeInfo.Protocol.ServiceName,
		}
	case "httpupgrade":
		stream.HTTPUPGRADESettings = &coreConf.HttpUpgradeConfig{
			Host:                nodeInfo.Protocol.Host,
			Path:                nodeInfo.Protocol.Path,
			AcceptProxyProtocol: nodeInfo.Protocol.AcceptProxyProtocol,
		}
	case "splithttp", "xhttp":
		stream.SplitHTTPSettings = &coreConf.SplitHTTPConfig{
			Host: nodeInfo.Protocol.Host,
			Path: nodeInfo.Protocol.Path,
			Mode: nodeInfo.Protocol.XHTTPMode,
		}
		if nodeInfo.Protocol.XHTTPExtra != "" {
			stream.SplitHTTPSettings.Extra = json.RawMessage(nodeInfo.Protocol.XHTTPExtra)
		}
	default:
		return nil, errors.New("the network type is not vail")
	}
	return stream, nil
}
