package cloudplugin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

func ReadAccessToken(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	value := strings.TrimSpace(string(b))
	if len(value) != 48 || !strings.HasPrefix(value, "spat_") {
		return "", errors.New("expected an OAuth access token file")
	}
	return value, nil
}

func Discover(ctx context.Context, endpoint string) (p.Object, error) {
	return discover(ctx, endpoint, &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }})
}
func discover(ctx context.Context, endpoint string, client *http.Client) (p.Object, error) {
	if e := Endpoint(endpoint); e != nil {
		return nil, e
	}
	u, _ := url.Parse(endpoint)
	origin := u.Scheme + "://" + u.Host
	fetch := func(path string) (map[string]any, error) {
		req, _ := http.NewRequestWithContext(ctx, "GET", origin+path, nil)
		response, e := client.Do(req)
		if e != nil {
			return nil, errors.New("OAuth HTTPS metadata request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return nil, errors.New("OAuth metadata is unavailable; enable OAuth and proxy its discovery routes")
		}
		var value map[string]any
		e = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&value)
		if e != nil {
			return nil, errors.New("invalid OAuth metadata document")
		}
		return value, nil
	}
	resource, e := fetch("/.well-known/oauth-protected-resource/mcp")
	if e != nil {
		return nil, e
	}
	authServers, ok := resource["authorization_servers"].([]any)
	if resource["resource"] != endpoint || !ok || len(authServers) != 1 || authServers[0] != origin {
		return nil, errors.New("OAuth protected-resource binding differs")
	}
	metadata, e := fetch("/.well-known/oauth-authorization-server")
	if e != nil {
		return nil, e
	}
	if metadata["issuer"] != origin || metadata["authorization_endpoint"] != origin+"/oauth/authorize" || metadata["token_endpoint"] != origin+"/oauth/token" || metadata["authorization_response_iss_parameter_supported"] != true {
		return nil, errors.New("OAuth issuer or endpoints differ")
	}
	methods, ok := metadata["code_challenge_methods_supported"].([]any)
	if !ok || len(methods) != 1 || methods[0] != "S256" {
		return nil, errors.New("OAuth S256 PKCE is not advertised")
	}
	req, _ := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	response, e := client.Do(req)
	if e != nil {
		return nil, errors.New("OAuth MCP challenge request failed")
	}
	defer response.Body.Close()
	io.Copy(io.Discard, io.LimitReader(response.Body, 2<<20))
	if response.StatusCode != 401 || !strings.Contains(response.Header.Get("WWW-Authenticate"), `resource_metadata="`+origin+`/.well-known/oauth-protected-resource/mcp"`) {
		return nil, errors.New("MCP does not advertise its OAuth resource metadata challenge")
	}
	return p.Object{"endpoint": endpoint, "issuer": origin, "authentication": "OAuth authorization code + S256 PKCE", "clientRegistration": "predefined client; enter generated Client ID and Client Secret in ChatGPT", "discoveryVerified": true, "authenticatedToolsVerified": false, "verified": "HTTPS, discovery and unauthenticated challenge only; user authorization and authenticated tool calls remain separate"}, nil
}
