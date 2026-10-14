package mikrotik

import (
	"bufio"
	"bytes"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/VargaFoundation/trica/internal/collect"
)

func TestLengthCodecRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 0x7F, 0x80, 0x3FFF, 0x4000, 0x1FFFFF, 0x200000, 0xFFFFFFF, 0x10000000} {
		enc := encodeLength(n)
		got, err := readLength(bufio.NewReader(bytes.NewReader(enc)))
		if err != nil || got != n {
			t.Errorf("length %d: encoded %x, decoded %d, %v", n, enc, got, err)
		}
	}
	// the prefix sizes of the protocol: 1, 2, 3, 4 and 5 bytes
	for n, size := range map[int]int{0x7F: 1, 0x80: 2, 0x4000: 3, 0x200000: 4, 0x10000000: 5} {
		if got := len(encodeLength(n)); got != size {
			t.Errorf("encodeLength(%#x) is %d bytes, want %d", n, got, size)
		}
	}
	if _, err := readLength(bufio.NewReader(bytes.NewReader([]byte{0xF8}))); err == nil {
		t.Error("a reserved prefix must be refused")
	}
	if _, err := readLength(bufio.NewReader(bytes.NewReader([]byte{0xC0, 0x01}))); err == nil {
		t.Error("a truncated prefix must be refused")
	}
	if _, err := readLength(bufio.NewReader(bytes.NewReader(nil))); err == nil {
		t.Error("an empty stream must be refused")
	}
}

// TestLongWordsOverTheWire: words of 200 and 20000 bytes cross the codec
// (2- and 3-byte prefixes) in both directions.
func TestLongWordsOverTheWire(t *testing.T) {
	var buf bytes.Buffer
	long := strings.Repeat("x", 20000)
	if err := writeSentence(&buf, "/a", "="+strings.Repeat("k", 200)+"="+long); err != nil {
		t.Fatal(err)
	}
	got, err := readSentence(bufio.NewReader(&buf))
	if err != nil || len(got) != 2 || len(got[1]) != 1+200+1+20000 {
		t.Errorf("sentence = %d words, %v", len(got), err)
	}
	var big bytes.Buffer
	big.Write(encodeLength(maxWord + 1))
	if _, err := readSentence(bufio.NewReader(&big)); err == nil {
		t.Error("an oversized word must be refused")
	}
}

func rawConn(t *testing.T, f *FakeMikroTik) (net.Conn, *bufio.Reader) {
	t.Helper()
	c, err := net.DialTimeout("tcp", f.Addr(), 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	return c, bufio.NewReader(c)
}

func TestFakeProtocol(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	c, r := rawConn(t, f)
	ask := func(words ...string) [][]string {
		t.Helper()
		if err := writeSentence(c, words...); err != nil {
			t.Fatal(err)
		}
		var out [][]string
		for {
			s, err := readSentence(r)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
			if s[0] == "!done" {
				return out
			}
		}
	}

	// before login nothing is served
	if got := ask(cmdIdentity); got[0][0] != "!trap" || !strings.Contains(got[0][1], "not logged in") {
		t.Errorf("before login: %v", got)
	}
	// wrong password
	if got := ask("/login", "=name="+FakeUser, "=password=nope"); got[0][0] != "!trap" || !strings.Contains(got[0][1], "invalid user name or password") {
		t.Errorf("bad login: %v", got)
	}
	// right password, and tags are echoed
	if got := ask("/login", "=name="+FakeUser, "=password="+FakePassword); len(got) != 1 || got[0][0] != "!done" {
		t.Errorf("login: %v", got)
	}
	got := ask(cmdIdentity, ".tag=7")
	if !reflect.DeepEqual(got, [][]string{{"!re", "=name=sw-a1", ".tag=7"}, {"!done", ".tag=7"}}) {
		t.Errorf("identity = %v", got)
	}
	// an unknown command is a trap, like a router that lacks the package
	if got := ask("/no/such/print"); got[0][0] != "!trap" || !strings.Contains(got[0][1], "no such command") {
		t.Errorf("unknown: %v", got)
	}
	// attribute words come back in a stable (sorted) order
	got = ask(cmdRouterboard)
	if len(got) != 2 || !sortedWords(got[0][1:]) {
		t.Errorf("routerboard = %v", got)
	}

	reqs := f.Requests()
	if len(reqs) != 6 || reqs[2].Args[1] != "=password=***" {
		t.Errorf("requests = %+v", reqs)
	}
	f.Reset()
	if len(f.Requests()) != 0 {
		t.Error("Reset keeps requests")
	}
}

func sortedWords(w []string) bool {
	for i := 1; i < len(w); i++ {
		if w[i-1] > w[i] {
			return false
		}
	}
	return true
}

func TestFakeDropsOversizedWord(t *testing.T) {
	f := startFake(t, FakeCRS354v78)
	c, r := rawConn(t, f)
	if _, err := c.Write(encodeLength(maxWord + 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReadByte(); err == nil {
		t.Error("the connection should have been closed")
	}
}

func TestFakeOptions(t *testing.T) {
	if _, err := NewFake("nope"); err == nil || !strings.Contains(err.Error(), "unknown fixture") {
		t.Errorf("unknown fixture: %v", err)
	}
	f := startFake(t, FakeCRS354v78,
		WithRows(cmdIdentity, map[string]string{"name": "renamed"}),
		WithCredentials("alice", "s3cret"), Hang("/never"))
	b := runOnce(t, newTarget(t, f.Addr(), "", func(tg *collect.Target) { tg.Username = "alice" }))
	if b.Health.OK { // the env password is still the default one
		t.Error("custom credentials must be enforced")
	}
	if f.CAPEM() != nil {
		t.Error("a plain fake has no certificate")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestEveryFixtureServesEveryCommand(t *testing.T) {
	for _, v := range FakeVersions() {
		f := startFake(t, v)
		for cmd := range readCommands {
			if _, ok := f.tables[cmd]; !ok {
				t.Errorf("%s: no rows (even empty) for %s", v, cmd)
			}
		}
		for cmd := range f.tables {
			if !readCommands[cmd] {
				t.Errorf("%s: fixture serves %s, which the collector never sends", v, cmd)
			}
		}
	}
}
