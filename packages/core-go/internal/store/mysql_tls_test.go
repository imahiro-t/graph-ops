package store

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
)

// This file covers this ticket's (DFLT-00037) execution plan T-1 (store unit
// tests for the TLS mode/validation/driver-config plumbing) and T-3 (a
// hand-rolled fake MySQL server that proves no plaintext-bearing packet is
// ever sent when TLS cannot be established). Both are CI-runnable: unlike
// mysql_test.go's MySQLRepository CRUD tests, none of this needs a real
// MySQL server.

// --- T-1: NormalizeMySQLTLSMode / ValidateMySQLTLSSettings ---

func TestNormalizeMySQLTLSMode(t *testing.T) {
	valid := []struct{ in, want string }{
		{"", MySQLTLSVerifyFull},
		{MySQLTLSVerifyFull, MySQLTLSVerifyFull},
		{MySQLTLSVerifyCA, MySQLTLSVerifyCA},
		{MySQLTLSDisabled, MySQLTLSDisabled},
	}
	for _, tc := range valid {
		got, err := NormalizeMySQLTLSMode(tc.in)
		if err != nil {
			t.Errorf("NormalizeMySQLTLSMode(%q): unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("NormalizeMySQLTLSMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	// Deliberately rejected: the driver-level fallback/unverified modes
	// (F-2), and anything that isn't an exact-case, untrimmed match of one
	// of the three supported values -- there is no silent read-back to a
	// safe default for any of these (D-1).
	invalid := []string{
		"preferred", "skip-verify", "true", "false", "required",
		"VERIFY-FULL", " verify-full", "verify-full ", "Disabled",
	}
	for _, in := range invalid {
		if _, err := NormalizeMySQLTLSMode(in); err == nil {
			t.Errorf("NormalizeMySQLTLSMode(%q): expected an error, got none", in)
		}
	}
}

func TestValidateMySQLTLSSettings(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		caFile  string
		wantErr bool
	}{
		{"unset mode, no CA", "", "", false},
		{"verify-full, no CA", MySQLTLSVerifyFull, "", false},
		{"verify-full, absolute CA", MySQLTLSVerifyFull, "/tmp/ca.pem", false},
		{"verify-full, relative CA", MySQLTLSVerifyFull, "ca.pem", true},
		{"verify-ca, no CA", MySQLTLSVerifyCA, "", true},
		{"verify-ca, relative CA", MySQLTLSVerifyCA, "ca.pem", true},
		{"verify-ca, absolute CA", MySQLTLSVerifyCA, "/tmp/ca.pem", false},
		{"disabled, CA present (ignored, not an error)", MySQLTLSDisabled, "/tmp/ca.pem", false},
		{"disabled, relative CA (still rejected: caFile shape is checked before mode relevance)", MySQLTLSDisabled, "ca.pem", true},
		{"unsupported mode", "preferred", "/tmp/ca.pem", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMySQLTLSSettings(tc.mode, tc.caFile)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateMySQLTLSSettings(%q, %q) error = %v, wantErr %v", tc.mode, tc.caFile, err, tc.wantErr)
			}
		})
	}
}

// --- T-1: mysqlDriverConfig ---

// TestMySQLDriverConfig_NeverAllowsFallback pins D-3's core guarantee across
// all three supported TLS modes plus the unset-mode default: whatever else
// changes about mysqlDriverConfig in the future, AllowFallbackToPlaintext
// must never become true and TLSConfig must never become "preferred" (the
// only two driver-level knobs that can reintroduce a silent plaintext
// downgrade -- see F-2/F-3 in this ticket's execution plan).
func TestMySQLDriverConfig_NeverAllowsFallback(t *testing.T) {
	caFile := writeTempCACert(t)
	for _, mode := range []string{"", MySQLTLSVerifyFull, MySQLTLSVerifyCA, MySQLTLSDisabled} {
		t.Run("mode="+mode, func(t *testing.T) {
			cfg := Config{MySQLHost: "127.0.0.1", MySQLPort: 3306, MySQLTLSMode: mode}
			if mode == MySQLTLSVerifyCA || mode == "" {
				cfg.MySQLTLSCAFile = caFile
			}
			dc, err := mysqlDriverConfig(cfg)
			if err != nil {
				t.Fatalf("mysqlDriverConfig: %v", err)
			}
			if dc.AllowFallbackToPlaintext {
				t.Error("AllowFallbackToPlaintext = true, want false")
			}
			if dc.TLSConfig == "preferred" {
				t.Error(`TLSConfig = "preferred", want anything else`)
			}
		})
	}
}

func TestMySQLDriverConfig_Disabled(t *testing.T) {
	dc, err := mysqlDriverConfig(Config{MySQLHost: "127.0.0.1", MySQLTLSMode: MySQLTLSDisabled})
	if err != nil {
		t.Fatalf("mysqlDriverConfig: %v", err)
	}
	if dc.TLS != nil {
		t.Errorf("TLS = %+v, want nil for disabled mode", dc.TLS)
	}
}

func TestMySQLDriverConfig_VerifyFull(t *testing.T) {
	dc, err := mysqlDriverConfig(Config{MySQLHost: "db.example.com", MySQLTLSMode: MySQLTLSVerifyFull})
	if err != nil {
		t.Fatalf("mysqlDriverConfig: %v", err)
	}
	if dc.TLS == nil {
		t.Fatal("TLS = nil, want a non-nil *tls.Config for verify-full")
	}
	if dc.TLS.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = true, want false for verify-full")
	}
	if dc.TLS.ServerName != "db.example.com" {
		t.Errorf("ServerName = %q, want %q", dc.TLS.ServerName, "db.example.com")
	}
	if dc.TLS.MinVersion < tls.VersionTLS12 {
		t.Errorf("MinVersion = %x, want at least TLS 1.2", dc.TLS.MinVersion)
	}
	if dc.TLS.RootCAs != nil {
		t.Error("RootCAs should be nil (fall back to the OS trust store) when no CA file is configured")
	}
}

func TestMySQLDriverConfig_VerifyFullWithCA(t *testing.T) {
	caFile := writeTempCACert(t)
	dc, err := mysqlDriverConfig(Config{MySQLHost: "db.example.com", MySQLTLSMode: MySQLTLSVerifyFull, MySQLTLSCAFile: caFile})
	if err != nil {
		t.Fatalf("mysqlDriverConfig: %v", err)
	}
	if dc.TLS.RootCAs == nil {
		t.Error("RootCAs = nil, want the configured CA pool to be set")
	}
}

func TestMySQLDriverConfig_VerifyCA(t *testing.T) {
	caFile := writeTempCACert(t)
	dc, err := mysqlDriverConfig(Config{MySQLHost: "127.0.0.1", MySQLTLSMode: MySQLTLSVerifyCA, MySQLTLSCAFile: caFile})
	if err != nil {
		t.Fatalf("mysqlDriverConfig: %v", err)
	}
	if dc.TLS == nil {
		t.Fatal("TLS = nil, want a non-nil *tls.Config for verify-ca")
	}
	if !dc.TLS.InsecureSkipVerify {
		t.Error("InsecureSkipVerify = false, want true for verify-ca (Go's own hostname check is replaced by VerifyConnection)")
	}
	if dc.TLS.VerifyConnection == nil {
		t.Error("VerifyConnection = nil, want the manual chain-only verifier to be set")
	}
}

func TestMySQLDriverConfig_VerifyCAWithoutCAFileErrors(t *testing.T) {
	if _, err := mysqlDriverConfig(Config{MySQLHost: "127.0.0.1", MySQLTLSMode: MySQLTLSVerifyCA}); err == nil {
		t.Fatal("expected an error for verify-ca with no CA file configured")
	}
}

func TestMySQLDriverConfig_UnsupportedModeErrors(t *testing.T) {
	if _, err := mysqlDriverConfig(Config{MySQLHost: "127.0.0.1", MySQLTLSMode: "preferred"}); err == nil {
		t.Fatal("expected an error for an unsupported TLS mode")
	}
}

// --- T-1: CA file loading errors ---

func TestMysqlTLSCertPool_Errors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		_, err := mysqlTLSCertPool(filepath.Join(t.TempDir(), "does-not-exist.pem"))
		if err == nil {
			t.Fatal("expected an error for a missing CA file")
		}
	})

	t.Run("not PEM", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "not-pem.pem")
		if err := os.WriteFile(path, []byte("this is not a certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := mysqlTLSCertPool(path)
		if err == nil {
			t.Fatal("expected an error for a non-PEM CA file")
		}
		if strings.Contains(err.Error(), "this is not a certificate") {
			t.Errorf("error message must not echo the CA file's content: %v", err)
		}
	})

	t.Run("valid CA", func(t *testing.T) {
		path := writeTempCACert(t)
		pool, err := mysqlTLSCertPool(path)
		if err != nil {
			t.Fatalf("mysqlTLSCertPool: %v", err)
		}
		if pool == nil {
			t.Fatal("expected a non-nil pool for a valid CA file")
		}
	})
}

// --- T-3: fake MySQL server, proving no fallback occurs ---

// testCA is a self-signed CA generated once per test for issuing leaf
// certificates signed by it (or, for the "wrong CA" scenario, simply not
// used to sign the leaf presented).
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	der  []byte
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating CA key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "graph-ops test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating CA certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parsing CA certificate: %v", err)
	}
	return &testCA{cert: cert, key: key, der: der}
}

// writeFile writes ca's certificate as a PEM file under t.TempDir() and
// returns its absolute path, suitable for Config.MySQLTLSCAFile.
func (ca *testCA) writeFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.der})
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("writing CA file: %v", err)
	}
	return path
}

// issueLeaf issues a leaf certificate signed by ca. dnsNames may be empty,
// mirroring MySQL 8's auto-generated server certificate (F-5), which has no
// SAN at all.
func (ca *testCA) issueLeaf(t *testing.T, dnsNames ...string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating leaf key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "fake-mysql"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatalf("creating leaf certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// writeTempCACert is a convenience for tests (mostly the plain unit tests
// above) that just need *some* valid, parseable CA file and don't care
// about its content otherwise.
func writeTempCACert(t *testing.T) string {
	t.Helper()
	return newTestCA(t).writeFile(t)
}

// fakeMySQLResult is what a fakeMySQLServer run reports about the one
// connection it handled.
type fakeMySQLResult struct {
	// tlsAttempted is true iff the server advertised SSL support and the
	// client sent an SSLRequest packet (i.e. a TLS handshake was
	// attempted at all).
	tlsAttempted bool
	// tlsHandshakeErr is the server side's tls.Conn.Handshake() error, or
	// nil if no TLS handshake was attempted or it succeeded.
	tlsHandshakeErr error
	// bytesAfterReady is how many bytes the server received on the
	// connection after it was ready to receive the client's
	// authentication packet: immediately after the greeting for a
	// plaintext connection, or after a successful TLS handshake for a
	// TLS one. Zero means the client sent nothing before giving up (the
	// ErrNoTLS case) or before the read deadline below expired.
	bytesAfterReady int
	err             error
}

// mysqlCapabilityFlags mirrors the subset of go-sql-driver/mysql's
// unexported clientFlag bits this fake server's greeting needs to set
// (const.go). Duplicated here (rather than depended on) because they are
// unexported in that package and this file is intentionally a
// protocol-level double, not a user of the driver's internals.
const (
	mysqlCapClientProtocol41 uint32 = 1 << 9
	mysqlCapClientSSL        uint32 = 1 << 11
	mysqlCapClientSecureConn uint32 = 1 << 15
	mysqlCapClientPluginAuth uint32 = 1 << 19
)

// writeMySQLPacket frames payload as a MySQL protocol packet: a 3-byte
// little-endian length, a 1-byte sequence number, then the payload.
func writeMySQLPacket(w io.Writer, seq byte, payload []byte) error {
	header := make([]byte, 4)
	header[0] = byte(len(payload))
	header[1] = byte(len(payload) >> 8)
	header[2] = byte(len(payload) >> 16)
	header[3] = seq
	if _, err := w.Write(header); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// readMySQLPacket reads one framed packet's payload (see writeMySQLPacket).
func readMySQLPacket(r io.Reader) ([]byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}
	length := int(header[0]) | int(header[1])<<8 | int(header[2])<<16
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
	}
	return payload, nil
}

// buildGreeting builds a MySQL protocol-10 handshake initialization packet
// (the "greeting"), matching what go-sql-driver/mysql's readHandshakePacket
// (packets.go) parses. scramble must be exactly 20 bytes.
func buildGreeting(capabilityFlags uint32, scramble [20]byte, pluginName string) []byte {
	var buf bytes.Buffer
	buf.WriteByte(10) // protocol version
	buf.WriteString("8.0.40-graph-ops-fake")
	buf.WriteByte(0)
	connID := make([]byte, 4)
	binary.LittleEndian.PutUint32(connID, 1)
	buf.Write(connID)
	buf.Write(scramble[:8])
	buf.WriteByte(0) // filler
	buf.WriteByte(byte(capabilityFlags))
	buf.WriteByte(byte(capabilityFlags >> 8))
	buf.WriteByte(0x2d) // character set (arbitrary; unused by the client here)
	buf.WriteByte(0x02) // status flags (SERVER_STATUS_AUTOCOMMIT), low byte
	buf.WriteByte(0x00) // status flags, high byte
	buf.WriteByte(byte(capabilityFlags >> 16))
	buf.WriteByte(byte(capabilityFlags >> 24))
	buf.WriteByte(21) // length of auth-plugin-data (8 + 13)
	buf.Write(make([]byte, 10))
	buf.Write(scramble[8:20])
	buf.WriteByte(0) // 13th byte: null terminator of the scramble's 2nd part
	buf.WriteString(pluginName)
	buf.WriteByte(0)
	return buf.Bytes()
}

// runFakeMySQLServer starts a one-shot TCP listener that plays the server
// side of exactly one connection attempt, far enough into the MySQL
// protocol handshake to observe whether the client ever sends its
// authentication packet (which carries the password, scrambled but usable
// against a server that knows it -- see this ticket's execution plan, F-2)
// -- without implementing enough of the protocol to actually authenticate
// anyone. advertiseSSL controls the greeting's capability flags; when true
// and the client attempts TLS, serverTLSConfig is used for the server side
// of the handshake (nil is only valid when advertiseSSL is false).
//
// It returns the listener's address and a channel that receives exactly one
// fakeMySQLResult once the one connection it handles has been fully
// processed (or the accept itself failed/timed out).
func runFakeMySQLServer(t *testing.T, advertiseSSL bool, serverTLSConfig *tls.Config) (addr string, resultCh <-chan fakeMySQLResult) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listening: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	ch := make(chan fakeMySQLResult, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- fakeMySQLResult{err: err}
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))

		ch <- serveFakeMySQLConn(conn, advertiseSSL, serverTLSConfig)
	}()
	return ln.Addr().String(), ch
}

func serveFakeMySQLConn(conn net.Conn, advertiseSSL bool, serverTLSConfig *tls.Config) fakeMySQLResult {
	capFlags := mysqlCapClientProtocol41 | mysqlCapClientSecureConn | mysqlCapClientPluginAuth
	if advertiseSSL {
		capFlags |= mysqlCapClientSSL
	}
	var scramble [20]byte
	for i := range scramble {
		scramble[i] = byte(i + 1)
	}
	greeting := buildGreeting(capFlags, scramble, "mysql_native_password")
	if err := writeMySQLPacket(conn, 0, greeting); err != nil {
		return fakeMySQLResult{err: err}
	}

	var result fakeMySQLResult
	readyReader := io.Reader(conn)

	if advertiseSSL {
		// The client only ever sends an SSLRequest packet when it wants
		// TLS (see writeHandshakeResponsePacket in the driver -- this is
		// gated on cfg.TLS != nil, i.e. every mode except "disabled").
		// mysqlTestScenario chooses, per case, whether to reach this
		// branch at all.
		sslRequest, err := readMySQLPacket(conn)
		if err != nil {
			// Nothing arrived (e.g. because the driver decided not to
			// pursue TLS at all in this scenario) -- report as no bytes
			// received rather than as an error, so the assertions in
			// each test case can be written uniformly.
			return result
		}
		_ = sslRequest
		result.tlsAttempted = true

		tlsConn := tls.Server(conn, serverTLSConfig)
		if err := tlsConn.Handshake(); err != nil {
			result.tlsHandshakeErr = err
			return result
		}
		readyReader = tlsConn
	}

	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 4096)
	n, err := readyReader.Read(buf)
	result.bytesAfterReady = n
	if err != nil && !errors.Is(err, io.EOF) && !os.IsTimeout(err) {
		result.err = err
	}
	return result
}

// TestMySQLDriverConfig_NoFallback_SSLNotAdvertised covers scenario S-a of
// this ticket's execution plan: a server that does not advertise SSL
// support. With any TLS-requiring mode (verify-full, verify-ca), the
// driver must fail with ErrNoTLS *before* sending anything else -- in
// particular, before sending the packet that carries the (scrambled but
// exploitable, per F-2) password. This is what proves AllowFallbackToPlaintext's
// false setting (D-3) actually has the intended effect end-to-end, not just
// as a struct field.
func TestMySQLDriverConfig_NoFallback_SSLNotAdvertised(t *testing.T) {
	for _, mode := range []string{MySQLTLSVerifyFull, MySQLTLSVerifyCA} {
		t.Run("mode="+mode, func(t *testing.T) {
			caFile := writeTempCACert(t)
			addr, resultCh := runFakeMySQLServer(t, false, nil)
			host, port := splitHostPort(t, addr)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := PingMySQL(ctx, Config{
				MySQLHost: host, MySQLPort: port,
				MySQLTLSMode: mode, MySQLTLSCAFile: caFile,
			})
			if !errors.Is(err, mysqldriver.ErrNoTLS) {
				t.Errorf("PingMySQL error = %v, want errors.Is(err, mysqldriver.ErrNoTLS)", err)
			}

			result := <-resultCh
			if result.bytesAfterReady != 0 {
				t.Errorf("server received %d bytes after the greeting; want 0 (no auth packet should ever be sent when TLS can't be established)", result.bytesAfterReady)
			}
		})
	}
}

// TestMySQLDriverConfig_Disabled_SendsAuthPacket is S-a's control: with
// mode disabled against the very same "no SSL" server, the client *does*
// proceed to send its authentication packet (this is expected, intended
// plaintext behavior for an explicitly-disabled TLS mode) -- demonstrating
// that serveFakeMySQLConn's "0 bytes received" assertion above is actually
// exercising something (S-b).
func TestMySQLDriverConfig_Disabled_SendsAuthPacket(t *testing.T) {
	addr, resultCh := runFakeMySQLServer(t, false, nil)
	host, port := splitHostPort(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	// An error is expected here (this fake server never completes the
	// protocol), but it must not be ErrNoTLS, and the server must have
	// seen bytes.
	err := PingMySQL(ctx, Config{MySQLHost: host, MySQLPort: port, MySQLTLSMode: MySQLTLSDisabled})
	if errors.Is(err, mysqldriver.ErrNoTLS) {
		t.Errorf("PingMySQL unexpectedly failed with ErrNoTLS for the disabled mode: %v", err)
	}

	result := <-resultCh
	if result.bytesAfterReady == 0 {
		t.Error("server received 0 bytes; want the client's plaintext auth packet for the disabled mode")
	}
}

// TestMySQLDriverConfig_NoFallback_HostnameMismatch covers scenario S-c: the
// server advertises SSL and presents a certificate signed by the configured
// CA, but the certificate names a different host (mirroring F-5: MySQL 8's
// auto-generated certificate has no SAN at all, so a real deployment fails
// the exact same way under verify-full). The handshake itself must fail --
// hostname verification is Go's own default TLS client behavior for
// verify-full -- and the auth packet, which would carry the password, must
// never be sent.
func TestMySQLDriverConfig_NoFallback_HostnameMismatch(t *testing.T) {
	ca := newTestCA(t)
	caFile := ca.writeFile(t)
	leaf := ca.issueLeaf(t, "other.example") // never matches an IP-literal ServerName

	addr, resultCh := runFakeMySQLServer(t, true, &tls.Config{Certificates: []tls.Certificate{leaf}})
	host, port := splitHostPort(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := PingMySQL(ctx, Config{
		MySQLHost: host, MySQLPort: port,
		MySQLTLSMode: MySQLTLSVerifyFull, MySQLTLSCAFile: caFile,
	})
	if err == nil {
		t.Fatal("expected an error for a certificate that does not name the dialed host")
	}

	result := <-resultCh
	if !result.tlsAttempted {
		t.Fatal("server never saw an SSLRequest packet; want the client to have attempted TLS")
	}
	if result.tlsHandshakeErr == nil {
		t.Error("server-side TLS handshake succeeded; want it to fail (client should reject the hostname mismatch)")
	}
	if result.bytesAfterReady != 0 {
		t.Errorf("server received %d bytes after a failed handshake; want 0", result.bytesAfterReady)
	}
}

// TestMySQLDriverConfig_NoFallback_WrongCA covers scenario S-d: verify-ca
// with a certificate signed by a CA other than the one configured. The
// manual chain verification in mysqlVerifyCAOnly must reject it.
func TestMySQLDriverConfig_NoFallback_WrongCA(t *testing.T) {
	trustedCA := newTestCA(t)
	caFile := trustedCA.writeFile(t)
	otherCA := newTestCA(t)
	leaf := otherCA.issueLeaf(t) // signed by a CA the client does not trust

	addr, resultCh := runFakeMySQLServer(t, true, &tls.Config{Certificates: []tls.Certificate{leaf}})
	host, port := splitHostPort(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := PingMySQL(ctx, Config{
		MySQLHost: host, MySQLPort: port,
		MySQLTLSMode: MySQLTLSVerifyCA, MySQLTLSCAFile: caFile,
	})
	if err == nil {
		t.Fatal("expected an error for a certificate signed by an untrusted CA")
	}

	result := <-resultCh
	if result.tlsHandshakeErr == nil {
		t.Error("server-side TLS handshake succeeded; want it to fail (client should reject the untrusted CA)")
	}
	if result.bytesAfterReady != 0 {
		t.Errorf("server received %d bytes after a failed handshake; want 0", result.bytesAfterReady)
	}
}

// TestMySQLDriverConfig_VerifyCA_AcceptsSANLessCert covers scenario S-e:
// verify-ca against a certificate from the trusted CA that has no SAN at
// all (F-5's MySQL-8-auto-generated-certificate case). The TLS handshake
// must succeed -- proving verify-ca's whole reason to exist actually works,
// not just that it rejects everything -- and the client proceeds to send
// its (now TLS-protected) auth packet.
func TestMySQLDriverConfig_VerifyCA_AcceptsSANLessCert(t *testing.T) {
	ca := newTestCA(t)
	caFile := ca.writeFile(t)
	leaf := ca.issueLeaf(t) // no DNSNames, like MySQL 8's auto-generated cert

	addr, resultCh := runFakeMySQLServer(t, true, &tls.Config{Certificates: []tls.Certificate{leaf}})
	host, port := splitHostPort(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	// An error is still expected (the fake server stops responding once
	// it has read the auth packet), but it must not be a TLS error.
	_ = PingMySQL(ctx, Config{
		MySQLHost: host, MySQLPort: port,
		MySQLTLSMode: MySQLTLSVerifyCA, MySQLTLSCAFile: caFile,
	})

	result := <-resultCh
	if result.tlsHandshakeErr != nil {
		t.Errorf("server-side TLS handshake failed: %v; want success for a SAN-less certificate under verify-ca", result.tlsHandshakeErr)
	}
	if result.bytesAfterReady == 0 {
		t.Error("server received 0 bytes after a successful TLS handshake; want the client's (encrypted) auth packet")
	}
}

// splitHostPort splits addr (as returned by net.Listener.Addr().String())
// into a host and an int port, failing the test on any parse error.
func splitHostPort(t *testing.T, addr string) (string, int) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("splitting %q: %v", addr, err)
	}
	port, err := parsePort(portStr)
	if err != nil {
		t.Fatalf("parsing port %q: %v", portStr, err)
	}
	return host, port
}
