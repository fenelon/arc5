package email

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// A server whose certificate does not verify is refused, before any
// password is sent: verification is never switched off.
func TestDialRefusesAnUntrustedCertificate(t *testing.T) {
	mem := imapmemserver.New()
	mem.AddUser(imapmemserver.NewUser("me@example.org", "app-password"))
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:      imap.CapSet{imap.CapIMAP4rev1: {}},
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{selfSigned(t)}},
		Logger:    quietLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	defer srv.Close()
	port, _ := strconv.Atoi(strings.TrimPrefix(ln.Addr().String(), "127.0.0.1:"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := dialIMAP(ctx, account{Host: "127.0.0.1", Port: port, Username: "me@example.org", Password: "app-password"})
	if err == nil {
		c.Close()
		t.Fatal("a self-signed certificate was accepted")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Errorf("error = %v", err)
	}

	// Nothing listening: an error, not a hang.
	closed, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := closed.Addr().String()
	closed.Close()
	p, _ := strconv.Atoi(strings.TrimPrefix(addr, "127.0.0.1:"))
	if _, err := dialIMAP(ctx, account{Host: "127.0.0.1", Port: p}); err == nil {
		t.Error("dialing nothing succeeded")
	}
}
