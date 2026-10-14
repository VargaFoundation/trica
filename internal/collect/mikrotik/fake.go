package mikrotik

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

// FakeMikroTik is an in-process RouterOS API server: a real TCP (or TLS)
// listener speaking the binary word protocol (length-prefixed words,
// sentences ended by an empty word, !re / !done / !trap / !fatal), serving
// the rows of a fixture for each "print" command. It records every request,
// so a test can prove the collector only reads. It is reused by the simulated
// lab.
//
// The fixtures are synthesised from the RouterOS documentation, not recorded
// on equipment: see testdata/README.md.
type FakeMikroTik struct {
	ln     net.Listener
	caPEM  []byte
	user   string
	pass   string
	tables map[string][]row

	failing   map[string]string // path -> trap message
	malformed map[string]bool
	hanging   map[string]bool
	fatal     map[string]bool
	missing   map[string]bool

	mu       sync.Mutex
	requests []FakeRequest
	conns    map[net.Conn]bool
	wg       sync.WaitGroup
	closed   bool
}

// FakeRequest is one sentence received by the fake. The password of a
// /login is replaced by "***".
type FakeRequest struct {
	Command string
	Args    []string
}

// Fixture names, one per modelled device and RouterOS version.
const (
	// FakeCRS354v78 is a CRS354-48G-4S+2Q+ on RouterOS 7.8.
	FakeCRS354v78 = "crs354-ros7.8"
	// FakeCRS326v649 is a CRS326-24G-2S+ on RouterOS 6.49 (long-term).
	FakeCRS326v649 = "crs326-ros6.49"
)

// Credentials the fake accepts unless WithCredentials says otherwise. They
// protect nothing and exist nowhere else.
const (
	FakeUser     = "trica"
	FakePassword = "fake-password"
)

//go:embed testdata/crs354-ros7.8.json testdata/crs326-ros6.49.json
var fixtureFS embed.FS

// FakeVersions lists the fixture names NewFake accepts.
func FakeVersions() []string { return []string{FakeCRS354v78, FakeCRS326v649} }

// FakeOption customises a FakeMikroTik.
type FakeOption func(*FakeMikroTik)

// WithCredentials sets the user and password /login must present.
func WithCredentials(user, password string) FakeOption {
	return func(f *FakeMikroTik) { f.user, f.pass = user, password }
}

// WithTLS serves the API over TLS (api-ssl) with a self-signed certificate
// for 127.0.0.1, ::1 and localhost; FakeMikroTik.CAPEM returns it.
func WithTLS() FakeOption { return func(f *FakeMikroTik) { f.caPEM = []byte{} } }

// FailCommand makes path answer !trap with message (a router refusing the
// command for lack of permission, say) instead of its rows.
func FailCommand(path, message string) FakeOption {
	return func(f *FakeMikroTik) { f.failing[path] = message }
}

// MalformedReply makes path answer with a sentence that is not valid API.
func MalformedReply(path string) FakeOption {
	return func(f *FakeMikroTik) { f.malformed[path] = true }
}

// Hang makes path (or "/login") never answer: the connection stays open and
// silent, as a router that stopped serving.
func Hang(path string) FakeOption { return func(f *FakeMikroTik) { f.hanging[path] = true } }

// FatalOn makes path answer !fatal and close the connection.
func FatalOn(path string) FakeOption { return func(f *FakeMikroTik) { f.fatal[path] = true } }

// WithoutCommand makes path unknown to the router ("no such command"), as on
// a version or a package that does not have it.
func WithoutCommand(path string) FakeOption { return func(f *FakeMikroTik) { f.missing[path] = true } }

// WithRows replaces the rows served for path.
func WithRows(path string, rows ...map[string]string) FakeOption {
	return func(f *FakeMikroTik) {
		out := make([]row, 0, len(rows))
		for _, r := range rows {
			out = append(out, row(r))
		}
		f.tables[path] = out
	}
}

// NewFake starts a fake serving the named fixture (see FakeVersions) on a
// free loopback port. The caller must Close it.
func NewFake(version string, opts ...FakeOption) (*FakeMikroTik, error) {
	raw, err := fixtureFS.ReadFile("testdata/" + version + ".json")
	if err != nil {
		return nil, fmt.Errorf("unknown fixture %q (known: %s)", version, strings.Join(FakeVersions(), ", "))
	}
	f := &FakeMikroTik{
		user: FakeUser, pass: FakePassword, tables: map[string][]row{},
		failing: map[string]string{}, malformed: map[string]bool{}, hanging: map[string]bool{},
		fatal: map[string]bool{}, missing: map[string]bool{}, conns: map[net.Conn]bool{},
	}
	if err := json.Unmarshal(raw, &f.tables); err != nil {
		return nil, fmt.Errorf("fixture %s: %w", version, err)
	}
	for _, o := range opts {
		o(f)
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	if f.caPEM != nil {
		cert, pemBytes, err := selfSigned()
		if err != nil {
			_ = ln.Close()
			return nil, err
		}
		f.caPEM = pemBytes
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	f.ln = ln
	f.wg.Add(1)
	go f.accept()
	return f, nil
}

// Addr is the host:port to put in the target address.
func (f *FakeMikroTik) Addr() string { return f.ln.Addr().String() }

// CAPEM is the PEM certificate of a WithTLS fake (nil otherwise), to use as
// the target's CA file.
func (f *FakeMikroTik) CAPEM() []byte { return f.caPEM }

// Requests returns every sentence received so far.
func (f *FakeMikroTik) Requests() []FakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]FakeRequest(nil), f.requests...)
}

// Reset forgets the recorded requests.
func (f *FakeMikroTik) Reset() {
	f.mu.Lock()
	f.requests = nil
	f.mu.Unlock()
}

// Close stops the listener and drops every connection.
func (f *FakeMikroTik) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	err := f.ln.Close()
	for c := range f.conns {
		_ = c.Close()
	}
	f.mu.Unlock()
	f.wg.Wait()
	return err
}

func (f *FakeMikroTik) accept() {
	defer f.wg.Done()
	for {
		c, err := f.ln.Accept()
		if err != nil {
			return
		}
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			_ = c.Close()
			return
		}
		f.conns[c] = true
		f.wg.Add(1)
		f.mu.Unlock()
		go func() {
			defer f.wg.Done()
			f.serve(c)
			f.mu.Lock()
			delete(f.conns, c)
			f.mu.Unlock()
		}()
	}
}

func (f *FakeMikroTik) record(cmd string, args []string) {
	masked := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "=password=") {
			a = "=password=***"
		}
		masked[i] = a
	}
	f.mu.Lock()
	f.requests = append(f.requests, FakeRequest{Command: cmd, Args: masked})
	f.mu.Unlock()
}

func (f *FakeMikroTik) serve(c net.Conn) {
	defer c.Close()
	br := bufio.NewReader(c)
	loggedIn := false
	for {
		words, err := readSentence(br)
		if err != nil {
			return
		}
		if len(words) == 0 {
			continue
		}
		cmd, args := words[0], words[1:]
		f.record(cmd, args)
		var tag string
		attrs := map[string]string{}
		for _, a := range args {
			if t, ok := strings.CutPrefix(a, ".tag="); ok {
				tag = t
			} else if kv, ok := strings.CutPrefix(a, "="); ok {
				k, v, _ := strings.Cut(kv, "=")
				attrs[k] = v
			}
		}
		reply := func(words ...string) bool {
			if tag != "" {
				words = append(words, ".tag="+tag)
			}
			return writeSentence(c, words...) == nil
		}
		if f.hanging[cmd] {
			_, _ = io.Copy(io.Discard, c) // until the peer or Close drops the connection
			return
		}
		switch {
		case cmd == "/login":
			if attrs["name"] == f.user && attrs["password"] == f.pass {
				loggedIn = true
				if !reply("!done") {
					return
				}
			} else if !reply("!trap", "=message=invalid user name or password (6)") || !reply("!done") {
				return
			}
		case !loggedIn:
			if !reply("!trap", "=message=not logged in") || !reply("!done") {
				return
			}
		case f.fatal[cmd]:
			// The documented !fatal carries a bare message word, then the
			// router closes the connection.
			_ = writeSentence(c, "!fatal", "session terminated on request")
			return
		case f.malformed[cmd]:
			if !reply("!re", "this-is-not-an-attribute") || !reply("!done") {
				return
			}
		case f.failing[cmd] != "":
			if !reply("!trap", "=message="+f.failing[cmd]) || !reply("!done") {
				return
			}
		default:
			rows, known := f.tables[cmd]
			if !known || f.missing[cmd] {
				if !reply("!trap", "=message=no such command or directive") || !reply("!done") {
					return
				}
				continue
			}
			for _, r := range rows {
				words := []string{"!re"}
				keys := make([]string, 0, len(r))
				for k := range r {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					words = append(words, "="+k+"="+r[k])
				}
				if !reply(words...) {
					return
				}
			}
			if !reply("!done") {
				return
			}
		}
	}
}

// maxWord bounds a word received by the fake: nothing a client sends here is
// larger.
const maxWord = 1 << 20

// readSentence reads words up to the empty word that ends a sentence.
func readSentence(r *bufio.Reader) ([]string, error) {
	var words []string
	for {
		n, err := readLength(r)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return words, nil
		}
		if n > maxWord {
			return nil, errors.New("word too long")
		}
		b := make([]byte, n)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		words = append(words, string(b))
	}
}

// readLength decodes the variable-length word prefix: 1 to 5 bytes, the
// number of leading one bits of the first byte giving the extra bytes.
func readLength(r *bufio.Reader) (int, error) {
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	extra := 0
	n := int(b)
	switch {
	case b&0x80 == 0:
	case b&0xC0 == 0x80:
		extra, n = 1, int(b&0x3F)
	case b&0xE0 == 0xC0:
		extra, n = 2, int(b&0x1F)
	case b&0xF0 == 0xE0:
		extra, n = 3, int(b&0x0F)
	case b == 0xF0:
		extra, n = 4, 0
	default:
		return 0, errors.New("bad length prefix")
	}
	for i := 0; i < extra; i++ {
		c, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		n = n<<8 | int(c)
	}
	return n, nil
}

func encodeLength(n int) []byte {
	switch {
	case n < 0x80:
		return []byte{byte(n)}
	case n < 0x4000:
		n |= 0x8000
		return []byte{byte(n >> 8), byte(n)}
	case n < 0x200000:
		n |= 0xC00000
		return []byte{byte(n >> 16), byte(n >> 8), byte(n)}
	case n < 0x10000000:
		n |= 0xE0000000
		return []byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
	}
	return []byte{0xF0, byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
}

func writeSentence(w io.Writer, words ...string) error {
	var buf []byte
	for _, word := range words {
		buf = append(buf, encodeLength(len(word))...)
		buf = append(buf, word...)
	}
	buf = append(buf, 0)
	_, err := w.Write(buf)
	return err
}

// selfSigned makes a throwaway certificate for the loopback addresses.
func selfSigned() (tls.Certificate, []byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "fake-mikrotik"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pemBytes, nil
}
