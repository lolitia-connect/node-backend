package node

import (
	"testing"

	"github.com/perfect-panel/ppanel-node/api/panel"
)

type reloadTestService struct{}

func (reloadTestService) Prepare() error { return nil }
func (reloadTestService) Start() error   { return nil }
func (reloadTestService) Stop() error    { return nil }

func testProtocol(id string, port int) *panel.NodeInfo {
	return &panel.NodeInfo{Type: "vless", Id: 7, Protocol: &panel.Protocol{
		ID: id, Type: "vless", Enable: true, Port: port, Transport: "tcp",
	}}
}

func TestPlanProtocolReloadOnlyReplacesChangedProtocol(t *testing.T) {
	unchanged := protocolInstance{info: testProtocol("1", 443), service: reloadTestService{}}
	oldChanged := protocolInstance{info: testProtocol("2", 8443), service: reloadTestService{}}
	newUnchanged := protocolInstance{info: testProtocol("1", 443), service: reloadTestService{}}
	newChanged := protocolInstance{info: testProtocol("2", 9443), service: reloadTestService{}}

	merged, toStart, toStop := planProtocolReload(
		[]protocolInstance{unchanged, oldChanged},
		[]protocolInstance{newUnchanged, newChanged},
	)
	if len(toStart) != 1 || toStart[0].info.Protocol.ID != "2" {
		t.Fatalf("toStart = %#v, want only protocol 2", toStart)
	}
	if len(toStop) != 1 || toStop[0].info.Protocol.ID != "2" {
		t.Fatalf("toStop = %#v, want only protocol 2", toStop)
	}
	if merged[0].service != unchanged.service {
		t.Fatal("unchanged protocol service was replaced")
	}
}

func TestProtocolReloadPlanHandlesDisableAndRemove(t *testing.T) {
	oldEnabled := protocolInstance{info: testProtocol("1", 443), service: reloadTestService{}}
	oldRemoved := protocolInstance{info: testProtocol("2", 8443), service: reloadTestService{}}
	disabled := testProtocol("1", 443)
	disabled.Protocol.Enable = false

	merged, toStart, toStop := planProtocolReload(
		[]protocolInstance{oldEnabled, oldRemoved},
		[]protocolInstance{{info: disabled, service: reloadTestService{}}},
	)
	if len(toStart) != 0 || len(toStop) != 2 {
		t.Fatalf("disable/remove plan: start=%d stop=%d", len(toStart), len(toStop))
	}
	if len(merged) != 1 || merged[0].info.Protocol.Enable {
		t.Fatal("disabled protocol was not retained as disabled")
	}
}

func TestProtocolIdentityFallsBackToType(t *testing.T) {
	if protocolIdentity(testProtocol("", 443).Protocol) != (protocolKey{typ: "vless", id: "vless"}) {
		t.Fatal("empty protocol ID should fall back to protocol type")
	}
}
