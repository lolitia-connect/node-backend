package core

import (
	"strings"
	"testing"

	"github.com/perfect-panel/ppanel-node/api/panel"
)

func TestValidateServerConfigRejectsMissingTransport(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:   "vless",
		Enable: true,
		Port:   443,
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err == nil || !strings.Contains(err.Error(), "transport is required") {
		t.Fatalf("ValidateServerConfig() error = %v, want transport error", err)
	}
}

func TestValidateServerConfigAcceptsMultipleMieruInstances(t *testing.T) {
	protocols := []panel.Protocol{
		{ID: "1", Type: "mieru", Enable: true, Port: 8443, Transport: "TCP"},
		{ID: "2", Type: "mieru", Enable: true, Port: 8444, Transport: "UDP"},
		{ID: "1", Type: "vless", Enable: true, Port: 443, Transport: "tcp"},
	}
	if err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	}); err != nil {
		t.Fatalf("ValidateServerConfig() error = %v", err)
	}
}

func TestValidateServerConfigAcceptsVlessEncryption(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:                 "vless",
		Enable:               true,
		Port:                 443,
		Transport:            "tcp",
		Encryption:           "mlkem768x25519plus",
		EncryptionMode:       "native",
		EncryptionTicket:     "600s",
		EncryptionPrivateKey: "private-key",
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err != nil {
		t.Fatalf("ValidateServerConfig() error = %v", err)
	}
}

func TestValidateServerConfigRejectsInvalidVlessEncryptionMode(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:                 "vless",
		Enable:               true,
		Port:                 443,
		Transport:            "tcp",
		Encryption:           "mlkem768x25519plus",
		EncryptionMode:       "bad",
		EncryptionTicket:     "600s",
		EncryptionPrivateKey: "private-key",
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported encryption_mode") {
		t.Fatalf("ValidateServerConfig() error = %v, want encryption mode error", err)
	}
}

func TestValidateServerConfigAcceptsTLSFileCertWithoutSNI(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:      "vless",
		Enable:    true,
		Port:      443,
		Transport: "tcp",
		Security:  "tls",
		CertMode:  "file",
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err != nil {
		t.Fatalf("ValidateServerConfig() error = %v", err)
	}
}

func TestValidateServerConfigRejectsTLSManagedCertWithoutSNI(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:      "vless",
		Enable:    true,
		Port:      443,
		Transport: "tcp",
		Security:  "tls",
		CertMode:  "dns",
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err == nil || !strings.Contains(err.Error(), "sni is required") {
		t.Fatalf("ValidateServerConfig() error = %v, want sni error", err)
	}
}

func TestValidateServerConfigRejectsRealityWithoutPort(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:              "vless",
		Enable:            true,
		Port:              443,
		Transport:         "tcp",
		Security:          "reality",
		SNI:               "example.com",
		RealityServerAddr: "example.com",
		RealityPrivateKey: "private-key",
		RealityShortID:    "short-id",
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err == nil || !strings.Contains(err.Error(), "reality_server_port") {
		t.Fatalf("ValidateServerConfig() error = %v, want reality port error", err)
	}
}

func TestValidateServerConfigAcceptsRealityWithSNIAsDestHost(t *testing.T) {
	protocols := []panel.Protocol{{
		Type:              "vless",
		Enable:            true,
		Port:              443,
		Transport:         "tcp",
		Security:          "reality",
		SNI:               "example.com",
		RealityServerPort: 443,
		RealityPrivateKey: "private-key",
		RealityShortID:    "short-id",
	}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{
		Data: &panel.Data{Protocols: &protocols},
	})
	if err != nil {
		t.Fatalf("ValidateServerConfig() error = %v", err)
	}
}

func TestValidateServerConfigRejectsNativeTLSManagedCertWithoutSNI(t *testing.T) {
	protocols := []panel.Protocol{{Type: "tuic", Enable: true, Port: 443, CertMode: "dns"}}
	err := ValidateServerConfig(&panel.ServerConfigResponse{Data: &panel.Data{Protocols: &protocols}})
	if err == nil || !strings.Contains(err.Error(), "sni is required") {
		t.Fatalf("ValidateServerConfig() error = %v, want SNI validation error", err)
	}
}

func TestValidateServerConfigSkipsUnsupportedProtocol(t *testing.T) {
	protocols := []panel.Protocol{{Type: "wireguard"}}
	if err := ValidateServerConfig(&panel.ServerConfigResponse{Data: &panel.Data{Protocols: &protocols}}); err != nil {
		t.Fatal(err)
	}
}

func TestValidateServerConfigRejectsRemovedShadowsocksCipher(t *testing.T) {
	for _, cipher := range []string{"none", "plain"} {
		protocols := []panel.Protocol{{Type: "shadowsocks", Enable: true, Port: 443, Cipher: cipher}}
		if err := ValidateServerConfig(&panel.ServerConfigResponse{Data: &panel.Data{Protocols: &protocols}}); err == nil {
			t.Fatalf("accepted removed cipher %q", cipher)
		}
	}
}
