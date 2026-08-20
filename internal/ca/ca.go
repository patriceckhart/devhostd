package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"sync"
	"time"
)

type Authority struct {
	dir     string
	certDir string
	mu      sync.Mutex
	cert    *x509.Certificate
	key     *ecdsa.PrivateKey
	cache   map[string]*tls.Certificate
}

func Open(dir string) (*Authority, error) { return OpenWithCerts(dir, filepath.Join(dir, "certs")) }
func OpenWithCerts(dir, certDir string) (*Authority, error) {
	a := &Authority{dir: dir, certDir: certDir, cache: map[string]*tls.Certificate{}}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return nil, err
	}
	if err := a.load(); err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		if err = a.generate(); err != nil {
			return nil, err
		}
	}
	return a, nil
}
func (a *Authority) RootPath() string { return filepath.Join(a.dir, "rootCA.pem") }
func RootCommonName(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	p, _ := pem.Decode(b)
	if p == nil {
		return "", fmt.Errorf("invalid CA certificate")
	}
	c, e := x509.ParseCertificate(p.Bytes)
	if e != nil {
		return "", e
	}
	return c.Subject.CommonName, nil
}
func (a *Authority) load() error {
	cb, e := os.ReadFile(a.RootPath())
	if e != nil {
		return e
	}
	kb, e := os.ReadFile(filepath.Join(a.dir, "rootCA-key.pem"))
	if e != nil {
		return e
	}
	cp, _ := pem.Decode(cb)
	kp, _ := pem.Decode(kb)
	if cp == nil || kp == nil {
		return fmt.Errorf("invalid CA files")
	}
	a.cert, e = x509.ParseCertificate(cp.Bytes)
	if e != nil {
		return e
	}
	k, e := x509.ParsePKCS8PrivateKey(kp.Bytes)
	if e != nil {
		return e
	}
	var ok bool
	a.key, ok = k.(*ecdsa.PrivateKey)
	if !ok {
		return fmt.Errorf("CA key is not ECDSA")
	}
	return nil
}
func serial() *big.Int { n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)); return n }
func (a *Authority) generate() error {
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	u, _ := user.Current()
	host, _ := os.Hostname()
	name := "user"
	if u != nil {
		name = u.Username
	}
	now := time.Now()
	t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "devhostd local CA " + name + "@" + host}, NotBefore: now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, t, t, &k.PublicKey, k)
	if e != nil {
		return e
	}
	pk, e := x509.MarshalPKCS8PrivateKey(k)
	if e != nil {
		return e
	}
	if e = os.WriteFile(a.RootPath(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(a.dir, "rootCA-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); e != nil {
		return e
	}
	a.cert, e = x509.ParseCertificate(der)
	if e != nil {
		return e
	}
	a.key = k
	return nil
}
func (a *Authority) Certificate(host string, wildcard bool) (*tls.Certificate, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	key := host
	if wildcard {
		key += "+wild"
	}
	if c := a.cache[key]; c != nil {
		return c, nil
	}
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
	certPath, keyPath := filepath.Join(a.certDir, sum+".pem"), filepath.Join(a.certDir, sum+"-key.pem")
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		a.cache[key] = &c
		return &c, nil
	}
	k, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	names := []string{host}
	if wildcard {
		names = append(names, "*."+host)
	}
	now := time.Now()
	t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: host}, DNSNames: names, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(825 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, e := x509.CreateCertificate(rand.Reader, t, a.cert, &k.PublicKey, a.key)
	if e != nil {
		return nil, e
	}
	pk, e := x509.MarshalPKCS8PrivateKey(k)
	if e != nil {
		return nil, e
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.cert.Raw})...)
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})
	c, e := tls.X509KeyPair(certPEM, keyPEM)
	if e != nil {
		return nil, e
	}
	if e = os.WriteFile(certPath, certPEM, 0644); e != nil {
		return nil, e
	}
	if e = os.WriteFile(keyPath, keyPEM, 0600); e != nil {
		return nil, e
	}
	a.cache[key] = &c
	return &c, nil
}
