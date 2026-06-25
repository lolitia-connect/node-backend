package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/perfect-panel/ppanel-node/common/file"
	log "github.com/sirupsen/logrus"
)

func (c *XrayController) renewCertTask(_ context.Context) error {
	l, err := NewLego(c.Info)
	if err != nil {
		log.WithField("节点", c.Tag).Info("new lego error: ", err)
		return nil
	}
	err = l.RenewCert()
	if err != nil {
		log.WithField("节点", c.Tag).Info("renew cert error: ", err)
		return nil
	}
	return nil
}

// RequestCert requests or generates a TLS certificate based on CertMode.
func (c *XrayController) RequestCert() error {
	certFile := filepath.Join("/etc/PPanel-node/", c.Info.Type+strconv.Itoa(c.Info.Id)+".cer")
	keyFile := filepath.Join("/etc/PPanel-node/", c.Info.Type+strconv.Itoa(c.Info.Id)+".key")
	switch c.Info.Protocol.CertMode {
	case "none", "", "file":
	case "dns", "http":
		if file.IsExist(certFile) && file.IsExist(keyFile) {
			return nil
		}
		l, err := NewLego(c.Info)
		if err != nil {
			return fmt.Errorf("create lego object error: %s", err)
		}
		err = l.CreateCert()
		if err != nil {
			return fmt.Errorf("create lego cert error: %s", err)
		}
	case "self":
		if file.IsExist(certFile) && file.IsExist(keyFile) {
			return nil
		}
		err := generateSelfSslCertificate(c.Info.Protocol.SNI, certFile, keyFile)
		if err != nil {
			return fmt.Errorf("generate self cert error: %s", err)
		}
	default:
		return fmt.Errorf("unsupported certmode: %s", c.Info.Protocol.CertMode)
	}
	return nil
}

func generateSelfSslCertificate(domain, certPath, keyPath string) error {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tmpl := &x509.Certificate{
		Version:      3,
		SerialNumber: big.NewInt(time.Now().Unix()),
		Subject: pkix.Name{
			CommonName: domain,
		},
		DNSNames:              []string{domain},
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().AddDate(30, 0, 0),
	}
	cert, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(certPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: cert}); err != nil {
		return err
	}
	f, err = os.OpenFile(keyPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	if err = pem.Encode(f, &pem.Block{Type: "EC PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}); err != nil {
		return err
	}
	return nil
}
