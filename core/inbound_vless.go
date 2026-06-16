package core

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/perfect-panel/ppanel-node/api/panel"
	coreConf "github.com/xtls/xray-core/infra/conf"
)

func buildVLess(nodeInfo *panel.NodeInfo, inbound *coreConf.InboundDetourConfig) error {
	inbound.Protocol = "vless"
	var err error
	decryption := "none"
	if nodeInfo.Protocol.Encryption != "" && nodeInfo.Protocol.Encryption != "none" {
		switch nodeInfo.Protocol.Encryption {
		case "mlkem768x25519plus":
			parts := []string{
				"mlkem768x25519plus",
				nodeInfo.Protocol.EncryptionMode,
				nodeInfo.Protocol.EncryptionTicket + "s",
			}
			if nodeInfo.Protocol.EncryptionServerPadding != "" {
				parts = append(parts, nodeInfo.Protocol.EncryptionServerPadding)
			}
			parts = append(parts, nodeInfo.Protocol.EncryptionPrivateKey)
			decryption = strings.Join(parts, ".")
		default:
			return fmt.Errorf("vless decryption method %s is not support", nodeInfo.Protocol.Encryption)
		}
	}
	s, err := json.Marshal(&coreConf.VLessInboundConfig{
		Decryption: decryption,
	})
	if err != nil {
		return fmt.Errorf("marshal vless config error: %s", err)
	}
	inbound.Settings = (*json.RawMessage)(&s)
	stream, err := buildTransportSetting(nodeInfo)
	if err != nil {
		return err
	}
	inbound.StreamSetting = stream
	return nil
}
