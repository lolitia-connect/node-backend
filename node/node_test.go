package node

import (
	"testing"

	"github.com/perfect-panel/ppanel-node/api/panel"
	"github.com/perfect-panel/ppanel-node/conf"
)

func TestNewSkipsUnsupportedProtocols(t *testing.T) {
	protocols := []panel.Protocol{
		{Type: "wireguard", Enable: true, Port: 51820},
		{Type: "vless", Enable: true, Port: 443, Transport: "tcp"},
	}
	node, err := New(nil, &conf.Conf{ApiConfig: conf.ServerApiConfig{
		ApiHost: "https://panel.example", ServerId: 7,
		ACMEEmail: "admin@example.test", ACMECADirURL: "https://acme.example.test/directory",
	}}, &panel.ServerConfigResponse{Data: &panel.Data{Protocols: &protocols}})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if len(node.controllers) != 1 || node.controllers[0].info.Type != "vless" {
		t.Fatalf("controllers = %+v, want only supported vless protocol", node.controllers)
	}
	if got := node.controllers[0].info.ACMEEmail; got != "admin@example.test" {
		t.Fatalf("ACMEEmail = %q, want admin@example.test", got)
	}
	if got := node.controllers[0].info.ACMECADirURL; got != "https://acme.example.test/directory" {
		t.Fatalf("ACMECADirURL = %q, want configured ACME directory", got)
	}
}

func TestNewKeepsMultipleProtocolInstancesAndMieru(t *testing.T) {
	protocols := []panel.Protocol{
		{ID: "1", Type: "vless", Enable: true, Port: 443, Transport: "tcp"},
		{ID: "2", Type: "vless", Enable: true, Port: 8443, Transport: "tcp"},
		{ID: "1", Type: "mieru", Enable: true, Port: 8444, Transport: "tcp"},
	}
	node, err := New(nil, conf.New(), &panel.ServerConfigResponse{Data: &panel.Data{Protocols: &protocols}})
	if err != nil {
		t.Fatal(err)
	}
	if len(node.controllers) != 2 || len(node.mieruNodes) != 1 {
		t.Fatalf("controllers = %d, mieru = %d", len(node.controllers), len(node.mieruNodes))
	}
	for index, controller := range node.controllers {
		if got := controller.apiClient.Client.QueryParam.Get("protocol_id"); got != protocols[index].ID {
			t.Fatalf("protocol_id = %q, want %q", got, protocols[index].ID)
		}
	}
	if node.controllers[0].buildNodeTag(node.controllers[0].info) == node.controllers[1].buildNodeTag(node.controllers[1].info) {
		t.Fatal("different protocol instances share a node tag")
	}
	if node.mieruNodes[0].traffic != node.traffic {
		t.Fatal("mieru is not using the shared traffic retry queue")
	}
}
