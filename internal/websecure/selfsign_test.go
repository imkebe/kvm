package websecure

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"testing"

	"github.com/rs/zerolog"
)

type certificateTestConn struct {
	net.Conn
	address net.Addr
}

func (c certificateTestConn) LocalAddr() net.Addr { return c.address }

func TestSelfSignedCertificateWithoutSNI(t *testing.T) {
	for _, test := range []struct {
		name, hostname string
		connection     net.Conn
	}{
		{"IPv6 local address", "2001:db8::1", certificateTestConn{address: &net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 443}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			log := zerolog.Nop()
			store := NewCertStore(t.TempDir(), &log)
			signer := NewSelfSigner(store, &log, "jetkvm.local", "JetKVM", "Test", "Test CA")
			certificate, err := signer.GetCertificate(&tls.ClientHelloInfo{Conn: test.connection})
			if err != nil || certificate == nil {
				t.Fatalf("GetCertificate: %v", err)
			}
			leaf, err := x509.ParseCertificate(certificate.Certificate[0])
			if err != nil {
				t.Fatal(err)
			}
			if err := leaf.VerifyHostname(test.hostname); err != nil {
				t.Fatalf("certificate does not cover the requested address: %v", err)
			}
		})
	}
}
