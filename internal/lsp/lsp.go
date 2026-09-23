// Package lsp is a small client for gopls: hover, definition, references
// and implementations for positions in a module, used by the code viewer
// and MCP. gopls starts on first use and restarts if it exits.
package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrUnavailable means gopls is not installed or could not start.
var ErrUnavailable = errors.New("lsp: gopls not available")

// requestTimeout bounds one request; the first can wait for gopls to load
// the workspace.
const requestTimeout = 30 * time.Second

// Location is a position range in a module file. Lines and columns are
// 1-based; columns count bytes, as Go positions do.
type Location struct {
	File      string `json:"file"`
	StartLine int    `json:"startLine"`
	StartCol  int    `json:"startCol"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	// Text is the source line at StartLine, for context.
	Text string `json:"text,omitempty"`
}

// Client talks to one gopls process for one module directory.
type Client struct {
	dir  string
	path string // gopls binary

	mu      sync.Mutex
	conn    *conn
	startMu sync.Mutex
}

// New returns a client for the module at dir. gopls is looked up in PATH
// (and $GOPATH/bin) when first needed.
func New(dir string) *Client {
	return &Client{dir: dir}
}

// Available reports whether gopls can be found.
func (c *Client) Available() bool {
	_, err := c.binary()
	return err == nil
}

func (c *Client) binary() (string, error) {
	if c.path != "" {
		return c.path, nil
	}
	if p, err := exec.LookPath("gopls"); err == nil {
		c.path = p
		return p, nil
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	if p := filepath.Join(gopath, "bin", "gopls"); fileExists(p) {
		c.path = p
		return p, nil
	}
	return "", ErrUnavailable
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// Close stops gopls. A nil client has nothing to stop.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	cn := c.conn
	c.conn = nil
	c.mu.Unlock()
	if cn == nil {
		return nil
	}
	return cn.shutdown()
}

// Hover returns gopls's hover text (Markdown) for a position.
func (c *Client) Hover(ctx context.Context, file string, line, col int) (string, error) {
	var res struct {
		Contents struct {
			Value string `json:"value"`
		} `json:"contents"`
	}
	if err := c.positionRequest(ctx, "textDocument/hover", file, line, col, nil, &res); err != nil {
		return "", err
	}
	return res.Contents.Value, nil
}

// Definition returns where the identifier at a position is defined.
func (c *Client) Definition(ctx context.Context, file string, line, col int) ([]Location, error) {
	return c.locations(ctx, "textDocument/definition", file, line, col, nil)
}

// References returns the uses of the identifier at a position, including
// its declaration.
func (c *Client) References(ctx context.Context, file string, line, col int) ([]Location, error) {
	return c.locations(ctx, "textDocument/references", file, line, col, map[string]any{"context": map[string]bool{"includeDeclaration": true}})
}

// Implementations returns the implementations of an interface (method), or
// the interfaces a type implements.
func (c *Client) Implementations(ctx context.Context, file string, line, col int) ([]Location, error) {
	return c.locations(ctx, "textDocument/implementation", file, line, col, nil)
}

func (c *Client) locations(ctx context.Context, method, file string, line, col int, extra map[string]any) ([]Location, error) {
	var raw []lspLocation
	if err := c.positionRequest(ctx, method, file, line, col, extra, &raw); err != nil {
		return nil, err
	}
	out := make([]Location, 0, len(raw))
	for _, l := range raw {
		loc, ok := c.fromLSP(l)
		if ok {
			out = append(out, loc)
		}
	}
	return out, nil
}

// positionRequest sends a textDocument/* request for a module file and
// 1-based byte position, opening the file in gopls first.
func (c *Client) positionRequest(ctx context.Context, method, file string, line, col int, extra map[string]any, result any) error {
	abs, err := c.moduleFile(file)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	lines := strings.Split(string(content), "\n")
	if line < 1 || line > len(lines) {
		return fmt.Errorf("lsp: %s has no line %d", file, line)
	}
	cn, err := c.connection(ctx)
	if err != nil {
		return err
	}
	uri := fileURI(abs)
	if err := cn.open(uri, string(content)); err != nil {
		return err
	}
	params := map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": line - 1, "character": utf16Col(lines[line-1], col-1)},
	}
	for k, v := range extra {
		params[k] = v
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return cn.call(ctx, method, params, result)
}

// moduleFile resolves a module-relative path, refusing escapes.
func (c *Client) moduleFile(file string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(file))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("lsp: %s is not a module file", file)
	}
	return filepath.Join(c.dir, clean), nil
}

type lspLocation struct {
	URI   string `json:"uri"`
	Range struct {
		Start struct{ Line, Character int } `json:"start"`
		End   struct{ Line, Character int } `json:"end"`
	} `json:"range"`
}

// fromLSP converts a location to module-relative byte columns; locations
// outside the module (standard library, dependencies) are kept with an
// absolute path.
func (c *Client) fromLSP(l lspLocation) (Location, bool) {
	u, err := url.Parse(l.URI)
	if err != nil || u.Scheme != "file" {
		return Location{}, false
	}
	path := u.Path
	loc := Location{File: path, StartLine: l.Range.Start.Line + 1, EndLine: l.Range.End.Line + 1}
	if rel, err := filepath.Rel(c.dir, path); err == nil && !strings.HasPrefix(rel, "..") {
		loc.File = filepath.ToSlash(rel)
	}
	lines := readLines(path)
	loc.StartCol = byteCol(lineAt(lines, l.Range.Start.Line), l.Range.Start.Character) + 1
	loc.EndCol = byteCol(lineAt(lines, l.Range.End.Line), l.Range.End.Character) + 1
	loc.Text = strings.TrimSpace(lineAt(lines, l.Range.Start.Line))
	return loc, true
}

func readLines(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(string(b), "\n")
}

func lineAt(lines []string, i int) string {
	if i < 0 || i >= len(lines) {
		return ""
	}
	return lines[i]
}

// utf16Col converts a byte offset in a line to UTF-16 code units (LSP).
func utf16Col(line string, byteOffset int) int {
	n := 0
	for i, r := range line {
		if i >= byteOffset {
			break
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// byteCol converts UTF-16 code units in a line to a byte offset.
func byteCol(line string, units int) int {
	n := 0
	for i, r := range line {
		if n >= units {
			return i
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return len(line)
}

func fileURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// connection returns the running gopls, starting it if needed.
func (c *Client) connection(ctx context.Context) (*conn, error) {
	c.startMu.Lock()
	defer c.startMu.Unlock()
	c.mu.Lock()
	cn := c.conn
	c.mu.Unlock()
	if cn != nil && !cn.dead() {
		return cn, nil
	}
	path, err := c.binary()
	if err != nil {
		return nil, err
	}
	cn, err = start(ctx, path, c.dir)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	c.mu.Lock()
	c.conn = cn
	c.mu.Unlock()
	return cn, nil
}

// conn is one gopls process speaking JSON-RPC over stdio.
type conn struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser

	mu      sync.Mutex
	nextID  int
	pending map[int]chan response
	opened  map[string]bool
	closed  chan struct{}
	writeMu sync.Mutex
}

type response struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func start(ctx context.Context, path, dir string) (*conn, error) {
	cmd := exec.Command(path, "serve")
	cmd.Dir = dir
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	cn := &conn{cmd: cmd, stdin: stdin, pending: map[int]chan response{}, opened: map[string]bool{}, closed: make(chan struct{})}
	go cn.read(bufio.NewReader(stdout))
	go func() { _ = cmd.Wait(); cn.fail() }()

	initCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	root := fileURI(dir)
	var initResult json.RawMessage
	err = cn.call(initCtx, "initialize", map[string]any{
		"processId":        os.Getpid(),
		"rootUri":          root,
		"workspaceFolders": []map[string]string{{"uri": root, "name": filepath.Base(dir)}},
		"capabilities": map[string]any{
			"textDocument": map[string]any{"hover": map[string]any{"contentFormat": []string{"markdown", "plaintext"}}},
		},
	}, &initResult)
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	if err := cn.notify("initialized", map[string]any{}); err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	return cn, nil
}

func (cn *conn) dead() bool {
	select {
	case <-cn.closed:
		return true
	default:
		return false
	}
}

// fail wakes every waiting call once gopls is gone.
func (cn *conn) fail() {
	cn.mu.Lock()
	defer cn.mu.Unlock()
	select {
	case <-cn.closed:
		return
	default:
	}
	close(cn.closed)
	for id, ch := range cn.pending {
		close(ch)
		delete(cn.pending, id)
	}
}

func (cn *conn) read(r *bufio.Reader) {
	defer cn.fail()
	for {
		length := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				length, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		body := make([]byte, length)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var msg struct {
			ID     *json.RawMessage `json:"id"`
			Method string           `json:"method"`
			response
		}
		if err := json.Unmarshal(body, &msg); err != nil || msg.ID == nil {
			continue // notifications from gopls (diagnostics, progress)
		}
		if msg.Method != "" {
			// A request from gopls (e.g. workspace/configuration): answer
			// with nothing so it does not wait.
			_ = cn.send(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": nil})
			continue
		}
		var id int
		if err := json.Unmarshal(*msg.ID, &id); err != nil {
			continue
		}
		cn.mu.Lock()
		ch := cn.pending[id]
		delete(cn.pending, id)
		cn.mu.Unlock()
		if ch != nil {
			ch <- msg.response
		}
	}
}

func (cn *conn) send(msg any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	cn.writeMu.Lock()
	defer cn.writeMu.Unlock()
	_, err = fmt.Fprintf(cn.stdin, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return err
}

func (cn *conn) call(ctx context.Context, method string, params, result any) error {
	cn.mu.Lock()
	if cn.dead() {
		cn.mu.Unlock()
		return fmt.Errorf("%w: gopls exited", ErrUnavailable)
	}
	cn.nextID++
	id := cn.nextID
	ch := make(chan response, 1)
	cn.pending[id] = ch
	cn.mu.Unlock()
	if err := cn.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	select {
	case res, ok := <-ch:
		if !ok {
			return fmt.Errorf("%w: gopls exited", ErrUnavailable)
		}
		if res.Error != nil {
			return fmt.Errorf("lsp: %s: %s", method, res.Error.Message)
		}
		if result == nil || len(res.Result) == 0 || string(res.Result) == "null" {
			return nil
		}
		return json.Unmarshal(res.Result, result)
	case <-ctx.Done():
		cn.mu.Lock()
		delete(cn.pending, id)
		cn.mu.Unlock()
		return fmt.Errorf("lsp: %s: %w", method, ctx.Err())
	}
}

func (cn *conn) notify(method string, params any) error {
	return cn.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

// open tells gopls about a file once.
func (cn *conn) open(uri, text string) error {
	cn.mu.Lock()
	if cn.opened[uri] {
		cn.mu.Unlock()
		return nil
	}
	cn.opened[uri] = true
	cn.mu.Unlock()
	return cn.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "go", "version": 1, "text": text},
	})
}

func (cn *conn) shutdown() error {
	if cn.dead() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = cn.call(ctx, "shutdown", nil, nil)
	_ = cn.notify("exit", nil)
	select {
	case <-cn.closed:
	case <-time.After(5 * time.Second):
		_ = cn.cmd.Process.Kill()
	}
	return nil
}
