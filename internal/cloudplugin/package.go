package cloudplugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	assets "github.com/sunforwork/SuperProd_MCP"
	"github.com/sunforwork/SuperProd_MCP/internal/compat"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func Endpoint(value string) error {
	u, e := url.Parse(value)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "/mcp" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return errors.New("MCP URL must be https://your-host/mcp, without credentials, query or fragment")
	}
	return nil
}
func ReadKey(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	key := strings.TrimSpace(string(b))
	if strings.HasPrefix(key, "{") {
		var v struct {
			MCPKey string `json:"mcpKey"`
		}
		if json.Unmarshal(b, &v) != nil {
			return "", errors.New("invalid credentials file")
		}
		key = v.MCPKey
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(key)
	if len(key) != 43 || decodeErr != nil || len(decoded) != 32 {
		return "", errors.New("expected the generated 256-bit MCP API key or init credentials JSON")
	}
	return key, nil
}

type keyTransport struct {
	key, endpoint string
	base          http.RoundTripper
}

func (t keyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != t.endpoint {
		return nil, errors.New("refusing to send API key to another endpoint")
	}
	c := r.Clone(r.Context())
	c.Header.Set("Authorization", "Bearer "+t.key)
	return t.base.RoundTrip(c)
}

func Verify(ctx context.Context, endpoint, key string) (p.Object, error) {
	if e := Endpoint(endpoint); e != nil {
		return nil, e
	}
	return verify(ctx, endpoint, key, &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}
func verify(ctx context.Context, endpoint, key string, base *http.Client) (p.Object, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	resp, e := base.Do(request)
	if e != nil {
		return nil, errors.New("HTTPS verification failed; check DNS, trusted certificate and proxy")
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 2<<20))
	resp.Body.Close()
	if resp.StatusCode != 401 {
		return nil, errors.New("unauthenticated MCP request must return 401; proxy/authentication configuration differs")
	}
	transport := base.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	authed := *base
	authed.Transport = keyTransport{key: key, endpoint: endpoint, base: transport}
	authed.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client := mcp.NewClient(&mcp.Implementation{Name: "spmcp-cloud-probe", Version: "0.1.0"}, nil)
	session, e := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: &authed}, nil)
	if e != nil {
		return nil, errors.New("authenticated MCP initialization failed")
	}
	defer session.Close()
	list, e := session.ListTools(ctx, nil)
	if e != nil {
		return nil, errors.New("MCP tool discovery failed")
	}
	found := false
	for _, tool := range list.Tools {
		if tool.Name == "get_capabilities" && tool.Annotations != nil && tool.Annotations.ReadOnlyHint {
			found = true
		}
	}
	if !found {
		return nil, errors.New("expected read-only get_capabilities tool missing")
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "get_capabilities", Arguments: p.Object{}})
	if e != nil || result.IsError {
		return nil, errors.New("MCP capabilities probe failed")
	}
	b, e := json.Marshal(result.StructuredContent)
	if e != nil {
		return nil, e
	}
	cap, e := p.Parse(b)
	if e != nil || cap["upstreamCommit"] != compat.Commit {
		return nil, errors.New("cloud server compatibility baseline differs from this plugin binary")
	}
	return p.Object{"endpoint": endpoint, "toolCount": len(list.Tools), "upstreamCommit": compat.Commit, "releaseReady": p.Bool(cap["releaseReady"]), "writeEnabled": p.Bool(cap["writeEnabled"]), "verified": "HTTPS, authentication, discovery and read-only capabilities; ChatGPT installation remains separate"}, nil
}
func Build(endpoint string) ([]byte, error) {
	if endpoint != "" {
		if e := Endpoint(endpoint); e != nil {
			return nil, e
		}
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	add := func(name string, b []byte) error {
		f, e := z.Create("super-productivity-mcp/" + name)
		if e != nil {
			return e
		}
		_, e = f.Write(b)
		return e
	}
	if e := fs.WalkDir(assets.Files, "plugin", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, e := assets.Files.ReadFile(path)
		if e != nil {
			return e
		}
		return add(strings.TrimPrefix(path, "plugin/"), b)
	}); e != nil {
		return nil, e
	}
	for _, name := range []string{"LICENSE", "NOTICE"} {
		b, e := assets.Files.ReadFile(name)
		if e != nil {
			return nil, e
		}
		if name == "NOTICE" {
			b = append(b, []byte("\nArchive scope: this plugin contains project metadata and Skills only.\nThe Go license collection accompanies the separately distributed server;\nno dependency implementation is bundled here.\n")...)
		}
		if e = add(name, b); e != nil {
			return nil, e
		}
	}
	if endpoint != "" {
		b, e := json.MarshalIndent(p.Object{"$schema": "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json", "mcpServers": p.Object{"super-productivity": p.Object{"type": "streamable-http", "url": endpoint}}}, "", "  ")
		if e != nil {
			return nil, e
		}
		if e = add("mcp.json", append(b, '\n')); e != nil {
			return nil, e
		}
	}
	if e := z.Close(); e != nil {
		return nil, e
	}
	return buf.Bytes(), nil
}
func Save(path string, b []byte) error {
	if path == "" {
		return errors.New("--output is required")
	}
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		os.Remove(path)
		return e
	}
	if ce != nil {
		os.Remove(path)
		return ce
	}
	return nil
}
