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
