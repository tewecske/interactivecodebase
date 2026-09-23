package cli

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	crand "crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/tewecske/interactivecodebase/internal/analysis"
	"github.com/tewecske/interactivecodebase/internal/fixture"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestRunHelpListsCommands(t *testing.T) {
	for _, arg := range []string{"help", "-h", "--help"} {
		code, stdout, _ := run(t, arg)
		if code != ExitOK {
			t.Fatalf("%s: exit code = %d, want %d", arg, code, ExitOK)
		}
		for _, c := range commands {
			if !strings.Contains(stdout, c.name) {
				t.Errorf("%s: usage does not mention %q:\n%s", arg, c.name, stdout)
			}
		}
	}
}

func TestRunWithoutArgsIsUsageError(t *testing.T) {
	code, _, stderr := run(t)
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, "Commands:") {
		t.Errorf("stderr missing usage:\n%s", stderr)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	code, _, stderr := run(t, "bogus")
	if code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr, `unknown command "bogus"`) {
		t.Errorf("stderr = %q", stderr)
	}
}

func TestRunVersion(t *testing.T) {
	code, stdout, _ := run(t, "version")
	if code != ExitOK || stdout != "icb dev\n" {
		t.Fatalf("got code %d, stdout %q", code, stdout)
	}
}

func TestSubcommandHelp(t *testing.T) {
	for _, c := range commands {
		code, _, stderr := run(t, c.name, "-h")
		if code != ExitOK {
			t.Errorf("%s -h: exit code = %d, want %d", c.name, code, ExitOK)
		}
		if !strings.Contains(stderr, c.usage) {
			t.Errorf("%s -h: usage missing %q:\n%s", c.name, c.usage, stderr)
		}
	}
}

func TestSubcommandArgumentValidation(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{"analyze missing dir", []string{"analyze"}, ExitUsage, "expected 1 argument(s), got 0"},
		{"analyze unknown flag", []string{"analyze", "-nope", dir}, ExitUsage, "flag provided but not defined"},
		{"analyze nonexistent dir", []string{"analyze", dir + "/missing"}, ExitError, "no such file or directory"},
		{"mcp not a module", []string{"mcp", dir}, ExitError, "is not inside a Go module"},
		{"analyze missing config", []string{"analyze", "-config", dir + "/nope.yaml", dir}, ExitError, "config: open"},
		{"query missing command", []string{"query", dir}, ExitUsage, "expected at least 2 argument(s), got 1"},
		{"query unknown command", []string{"query", dir, "bogus"}, ExitUsage, `unknown query command "bogus"`},
		{"query wrong arity", []string{"query", dir, "paths", "a"}, ExitUsage, "wrong number of arguments for paths"},
		{"analyze not a module", []string{"analyze", dir}, ExitError, "icb analyze:"},
		{"version extra arg", []string{"version", "x"}, ExitUsage, "expected 0 argument(s), got 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := run(t, tt.args...)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d (stderr: %s)", code, tt.code, stderr)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Errorf("stderr missing %q:\n%s", tt.want, stderr)
			}
		})
	}
}

// sharedWebapp analyzes the webapp fixture once for the whole package: a
// full analysis costs over a gigabyte under -race.
var sharedWebapp = sync.OnceValues(func() (*analysis.Result, error) {
	return analysis.Analyze(context.Background(), fixture.WebappDir(), analysis.Options{})
})

func TestMain(m *testing.M) {
	analyze := openAnalysis
	openAnalysis = func(ctx context.Context, dir string, opts analysis.Options) (*analysis.Result, func() error, error) {
		if dir != fixture.WebappDir() {
			return analyze(ctx, dir, opts)
		}
		r, err := sharedWebapp()
		return r, func() error { return nil }, err
	}
	code := m.Run()
	if r, err := sharedWebapp(); err == nil {
		_ = r.Close()
	}
	os.Exit(code)
}

// TestAnalyzeEndToEnd runs one real analysis through the command, the way
// the binary does, without the shared result.
func TestAnalyzeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/tiny\n\ngo 1.26.0\n",
		"main.go": "package main\n\nimport \"os\"\n\nfunc main() { run() }\n\nfunc run() { os.Exit(0) }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	code, stdout, stderr := run(t, "analyze", dir)
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"module example.com/tiny", "1 packages, 2 functions in the module", "calls 2"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAnalyzeWebapp(t *testing.T) {
	code, stdout, stderr := run(t, "analyze", fixture.WebappDir())
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	for _, want := range []string{"module example.com/webapp", "11 packages", "nodes: method", "dispatches_to 8", "implements 3"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestAnalyzeWebappJSON(t *testing.T) {
	code, stdout, stderr := run(t, "analyze", "-json", fixture.WebappDir())
	if code != ExitOK {
		t.Fatalf("exit code = %d, stderr: %s", code, stderr)
	}
	var got struct {
		Module string `json:"module"`
		Stats  struct {
			Packages int   `json:"packages"`
			TotalMS  int64 `json:"totalMs"`
		} `json:"stats"`
		Counts struct {
			Edges map[string]int `json:"edges"`
		} `json:"counts"`
	}
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("%v:\n%s", err, stdout)
	}
	if got.Module != "example.com/webapp" || got.Stats.Packages != 11 || got.Stats.TotalMS <= 0 || got.Counts.Edges["calls"] == 0 {
		t.Errorf("summary = %+v", got)
	}
}

func TestQueryWebapp(t *testing.T) {
	dir := fixture.WebappDir()
	tests := []struct {
		name  string
		args  []string
		code  int
		out   string   // expected in stdout (or stderr on failure)
		flags []string // query flags, before the directory
	}{
		{"callees by ID without kind", []string{"callees", "(*example.com/webapp/internal/web.noteHandler).create"}, ExitOK,
			"calls\tmethod:(*example.com/webapp/internal/web.noteHandler).user\tinternal/web/handlers.go:", nil},
		{"callers by unique search", []string{"callers", "Authenticator).Authenticate"}, ExitOK, "web.requireAdmin$1", nil},
		{"paths through interface dispatch", []string{"paths", "noteHandler).create", "sql.Tx).ExecContext"}, ExitOK,
			"-dispatches_to-> method:(*example.com/webapp/internal/store/postgres.NoteRepository).Create", nil},
		{"search", []string{"search", "SMTPMailer"}, ExitOK, "type:example.com/webapp/internal/mail.SMTPMailer", nil},
		{"flow", []string{"flow", "POST /{lang}/notes"}, ExitOK,
			"sink.sql (*sql.Tx).QueryRowContext \"INSERT INTO notes (owner_id, title, body, created_at) VALUES ($1, $2, $3, $4) RETURNING id\"", nil},
		{name: "flow method", flags: []string{"-method", "GET"}, args: []string{"flow", "POST /{lang}/sign-in"}, code: ExitOK, out: "table sessions (select"},
		{name: "flow bad prune", flags: []string{"-prune", "bogus"}, args: []string{"flow", "POST /{lang}/notes"}, code: ExitUsage, out: `invalid -prune "bogus"`},
		{"page", []string{"page", "GET /{lang}/notes/{id}"}, ExitOK, "POST /{lang}/notes/{id}/share\thx-post\ttemplates/note.html:", nil},
		{"routes", []string{"routes"}, ExitOK,
			"POST /{lang}/admin/reindex\tadmin\texample.com/webapp/internal/web.requireAdmin > (*example.com/webapp/internal/web.adminHandler).reindex\tinternal/web/router.go:", nil},
		{"ambiguous node", []string{"callees", "Create"}, ExitError, "matches several nodes; use an ID", nil},
		{"unknown node", []string{"callees", "nosuchthing"}, ExitError, `no node matches "nosuchthing"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append(append(append([]string{"query"}, tt.flags...), dir), tt.args...)
			code, stdout, stderr := run(t, args...)
			if code != tt.code {
				t.Fatalf("exit code = %d, want %d; stderr: %s", code, tt.code, stderr)
			}
			if out := stdout + stderr; !strings.Contains(out, tt.out) {
				t.Errorf("output missing %q:\n%s", tt.out, out)
			}
		})
	}
}

// syncBuffer is a bytes.Buffer safe for one writer and polling readers.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestServe(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"serve", "-addr", "127.0.0.1:0", "-token", "s3cret", fixture.WebappDir()}, &stdout, &stderr)
	}()

	var base string
	deadline := time.Now().Add(30 * time.Second)
	for base == "" {
		if time.Now().After(deadline) {
			t.Fatalf("server did not start; stdout %q stderr %q", stdout.String(), stderr.String())
		}
		if _, after, ok := strings.Cut(stdout.String(), "serving "); ok {
			base = strings.Fields(after)[0]
			break
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited with %d: %s", code, stderr.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	unauth, err := http.Get(base + "/api/summary")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Errorf("without a token: status %d", unauth.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/api/summary", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"module": "example.com/webapp"`) {
		t.Errorf("summary %d %s", resp.StatusCode, body)
	}
	// The same server speaks MCP over streamable HTTP at /mcp.
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(),
		&mcp.StreamableClientTransport{Endpoint: base + "/mcp", MaxRetries: -1, HTTPClient: bearerClient("s3cret")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "list_tables"})
	if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "4 tables") {
		t.Errorf("MCP over HTTP: %v %+v", err, res)
	}
	_ = cs.Close()

	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit code %d after shutdown: %s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not shut down")
	}
}

func TestMCPCommand(t *testing.T) {
	serverT, clientT := mcp.NewInMemoryTransports()
	saved := mcpTransport
	mcpTransport = func() mcp.Transport { return serverT }
	t.Cleanup(func() { mcpTransport = saved })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() { done <- Run(ctx, []string{"mcp", fixture.WebappDir()}, &stdout, &stderr) }()

	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_routes", Arguments: map[string]any{"access": "admin"}})
	if err != nil || res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "2 routes") {
		t.Errorf("list_routes: %v %+v", err, res)
	}
	_ = cs.Close()
	cancel()
	select {
	case code := <-done:
		if code != ExitOK {
			t.Errorf("exit code %d: %s", code, stderr.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("icb mcp did not stop")
	}
	if stdout.String() != "" {
		t.Errorf("icb mcp wrote to stdout outside the protocol: %q", stdout.String())
	}
}

// bearerClient sends a bearer token with every request.
func bearerClient(token string) *http.Client {
	return &http.Client{Transport: roundTripper(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.Header.Set("Authorization", "Bearer "+token)
		return http.DefaultTransport.RoundTrip(r)
	})}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestServeFlags(t *testing.T) {
	dir := fixture.WebappDir()
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"serve", "-insecure-no-auth", "-addr", "0.0.0.0:0", dir}, "only allowed on a loopback address"},
		{[]string{"serve", "-insecure-no-auth", "-addr", ":0", dir}, "only allowed on a loopback address"},
		{[]string{"serve", "-tls-cert", "c.pem", dir}, "-tls-cert and -tls-key go together"},
	}
	for _, tt := range tests {
		code, _, stderr := run(t, tt.args...)
		if code != ExitError || !strings.Contains(stderr, tt.want) {
			t.Errorf("%v: %d %q", tt.args, code, stderr)
		}
	}
}

func TestIsLoopback(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true, "localhost:1": true, "[::1]:80": true,
		"0.0.0.0:8080": false, ":8080": false, "192.168.1.5:80": false, "bad": false,
	} {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v", addr, got)
		}
	}
}

func TestServeTLS(t *testing.T) {
	certFile, keyFile, pool := selfSigned(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var stdout, stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"serve", "-addr", "127.0.0.1:0", "-tls-cert", certFile, "-tls-key", keyFile, fixture.WebappDir()}, &stdout, &stderr)
	}()
	var open string
	deadline := time.Now().Add(30 * time.Second)
	for open == "" && time.Now().Before(deadline) {
		if _, after, ok := strings.Cut(stdout.String(), "icb: open "); ok {
			open = strings.Fields(after)[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.HasPrefix(open, "https://") || !strings.Contains(open, "?token=") {
		t.Fatalf("no https sign-in URL with a generated token in %q (%s)", stdout.String(), stderr.String())
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := client.Get(open) // sets the cookie, redirects to /
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	api, err := client.Get(strings.Split(open, "?")[0] + "api/summary")
	if err != nil {
		t.Fatal(err)
	}
	_ = api.Body.Close()
	if resp.StatusCode != 200 || api.StatusCode != 200 {
		t.Errorf("sign-in %d, API with cookie %d", resp.StatusCode, api.StatusCode)
	}
	u, _ := url.Parse(open)
	for _, c := range jar.Cookies(u) {
		if c.Name == "icb_token" {
			return
		}
	}
	t.Error("no icb_token cookie after sign-in")
}

// selfSigned writes a certificate for 127.0.0.1 and its key.
func selfSigned(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), crand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "icb test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:              []string{"localhost"},
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(crand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}
