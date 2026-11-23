// Package smtptest là máy chủ SMTP giả cho test (chỉ dùng từ file _test.go): kiểm giao thức, TLS, AUTH, phong bì và dữ liệu nhận được mà không cần mạng thật.
package smtptest

import (
	"bufio"
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

// Server là máy chủ SMTP tối thiểu để kiểm giao thức và an toàn (dùng trong test của pkg/mailer/smtp và các app).
type Server struct {
	ln     net.Listener
	tlsCfg *tls.Config
	// Pool chứa chứng chỉ tự ký của máy chủ (đặt vào RootCAs của client).
	Pool *x509.CertPool
	// Implicit: TLS ngay từ đầu. StartTLS: quảng bá STARTTLS. Auths: cơ chế AUTH quảng bá ("PLAIN LOGIN", "LOGIN", ""). Greet: trễ trước lời chào.
	// RejectTo: RCPT tới địa chỉ này bị từ chối.
	Implicit, StartTLS bool
	Auths              string
	Greet              time.Duration
	// CmdDelay: trễ trước mỗi phản hồi sau lời chào.
	CmdDelay time.Duration
	RejectTo string
	mu       sync.Mutex
	conns    int
	authed   []string // "PLAIN user:pass" hoặc "LOGIN user:pass"
	sawTLS   []bool   // mỗi lần AUTH/MAIL: kết nối có TLS hay không
	mails    []Received
	cmds     []string
}

// Received là một thư đã nhận: phong bì, thân (đã bỏ dot-stuffing) và kết nối có TLS hay không.
type Received struct {
	From string
	To   []string
	Data string
	TLS  bool
}

var (
	certOnce sync.Once
	certVal  tls.Certificate
	certPool *x509.CertPool
)

// newCert dùng chung một chứng chỉ tự ký cho mọi máy chủ giả trong test (các test nối tới nhiều máy chủ cùng RootCAs).
func newCert(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	certOnce.Do(func() { certVal, certPool = makeCert(t) })
	return certVal, certPool
}

func makeCert(t testing.TB) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "127.0.0.1"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pool
}

// Start dựng máy chủ trên 127.0.0.1 với cổng ngẫu nhiên; mut chỉnh các tùy chọn trước khi lắng nghe. Mặc định: STARTTLS và AUTH PLAIN LOGIN.
func Start(t testing.TB, mut func(*Server)) *Server {
	t.Helper()
	cert, pool := newCert(t)
	s := &Server{Pool: pool, tlsCfg: &tls.Config{Certificates: []tls.Certificate{cert}}, StartTLS: true, Auths: "PLAIN LOGIN"}
	if mut != nil {
		mut(s)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.ln = ln
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns++
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	return s
}

// angle lấy địa chỉ trong <...> của lệnh MAIL FROM/RCPT TO (bỏ tham số như BODY=8BITMIME).
func angle(line string) string {
	i, j := strings.Index(line, "<"), strings.Index(line, ">")
	if i < 0 || j < i {
		return ""
	}
	return line[i+1 : j]
}

// Port là cổng đang lắng nghe; Close dừng máy chủ.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// CertPEM là chứng chỉ tự ký của máy chủ ở dạng PEM (để ghi ra tệp cho cấu hình CA của app).
func (s *Server) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.tlsCfg.Certificates[0].Certificate[0]})
}

// Close dừng lắng nghe.
func (s *Server) Close() { _ = s.ln.Close() }

// Snapshot là trạng thái đã ghi: số kết nối, thư nhận được, thông tin AUTH ("PLAIN user:pass"), các lệnh, và mỗi lần AUTH có TLS hay không.
type Snapshot struct {
	Conns  int
	Mails  []Received
	Authed []string
	Cmds   []string
	SawTLS []bool
}

// Snapshot trả bản sao trạng thái.
func (s *Server) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Snapshot{Conns: s.conns, Mails: append([]Received(nil), s.mails...), Authed: append([]string(nil), s.authed...), Cmds: append([]string(nil), s.cmds...), SawTLS: append([]bool(nil), s.sawTLS...)}
}

func (s *Server) serve(raw net.Conn) {
	defer raw.Close()
	var conn net.Conn = raw
	isTLS := false
	if s.Implicit {
		tc := tls.Server(raw, s.tlsCfg)
		if tc.Handshake() != nil {
			return
		}
		conn, isTLS = tc, true
	}
	if s.Greet > 0 {
		time.Sleep(s.Greet)
	}
	r := textproto.NewReader(bufio.NewReader(conn))
	say := func(f string, a ...any) { fmt.Fprintf(conn, f+"\r\n", a...) }
	say("220 fake ESMTP")
	var from string
	var to []string
	for {
		line, err := r.ReadLine()
		if err != nil {
			return
		}
		if s.CmdDelay > 0 {
			time.Sleep(s.CmdDelay)
		}
		verb := strings.ToUpper(strings.Fields(line + " x")[0])
		s.mu.Lock()
		s.cmds = append(s.cmds, verb)
		s.mu.Unlock()
		switch verb {
		case "EHLO", "HELO":
			say("250-fake")
			if s.StartTLS && !isTLS {
				say("250-STARTTLS")
			}
			if s.Auths != "" {
				say("250-AUTH %s", s.Auths)
			}
			say("250 8BITMIME")
		case "STARTTLS":
			say("220 go ahead")
			tc := tls.Server(conn, s.tlsCfg)
			if tc.Handshake() != nil {
				return
			}
			conn, isTLS = tc, true
			r = textproto.NewReader(bufio.NewReader(conn))
		case "AUTH":
			f := strings.Fields(line)
			s.mu.Lock()
			s.sawTLS = append(s.sawTLS, isTLS)
			s.mu.Unlock()
			switch strings.ToUpper(f[1]) {
			case "PLAIN":
				b, _ := base64.StdEncoding.DecodeString(f[2])
				p := strings.Split(string(b), "\x00")
				s.mu.Lock()
				s.authed = append(s.authed, "PLAIN "+p[1]+":"+p[2])
				s.mu.Unlock()
				say("235 ok")
			case "LOGIN":
				say("334 %s", base64.StdEncoding.EncodeToString([]byte("Username:")))
				u, _ := r.ReadLine()
				say("334 %s", base64.StdEncoding.EncodeToString([]byte("Password:")))
				p, _ := r.ReadLine()
				ub, _ := base64.StdEncoding.DecodeString(u)
				pb, _ := base64.StdEncoding.DecodeString(p)
				s.mu.Lock()
				s.authed = append(s.authed, "LOGIN "+string(ub)+":"+string(pb))
				s.mu.Unlock()
				say("235 ok")
			default:
				say("504 unsupported")
			}
		case "MAIL":
			from = angle(line)
			to = nil
			say("250 ok")
		case "RCPT":
			a := angle(line)
			if s.RejectTo != "" && a == s.RejectTo {
				say("550 no such user")
				continue
			}
			to = append(to, a)
			say("250 ok")
		case "DATA":
			say("354 go")
			var b bytes.Buffer
			dr := r.DotReader()
			_, _ = io.Copy(&b, dr)
			s.mu.Lock()
			s.mails = append(s.mails, Received{From: from, To: to, Data: b.String(), TLS: isTLS})
			s.mu.Unlock()
			say("250 queued")
		case "RSET", "NOOP":
			say("250 ok")
		case "QUIT":
			say("221 bye")
			return
		default:
			say("502 unknown")
		}
	}
}
