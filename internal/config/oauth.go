package config

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

const ChatGPTRedirect = "https://chatgpt.com/connector_platform_oauth_redirect"

type OAuth struct {
	Issuer            string   `json:"issuer"`
	Username          string   `json:"username"`
	PasswordHash      string   `json:"passwordHash,omitempty"`
	UseSyncPassword   bool     `json:"useSyncPassword,omitempty"`
	ClientID          string   `json:"clientId"`
	ClientSecretHash  string   `json:"clientSecretHash"`
	RedirectURIs      []string `json:"redirectUris"`
	SessionGeneration string   `json:"sessionGeneration,omitempty"`
}

func (c OAuth) Resource() string   { return c.Issuer + "/mcp" }
func (c OAuth) Generation() string { b, _ := json.Marshal(c); return Hash(string(b)) }
func (c OAuth) Validate() error {
	u, e := url.Parse(c.Issuer)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(c.Issuer, "\r\n\"\\") {
		return errors.New("OAuth issuer must be a canonical HTTPS origin without path, query or credentials")
	}
	if c.Username == "" || c.ClientID == "" || len(c.RedirectURIs) == 0 {
		return errors.New("incomplete OAuth configuration")
	}
	digests := []string{c.ClientSecretHash}
	if !c.UseSyncPassword {
		digests = append(digests, c.PasswordHash)
	}
	for _, digest := range digests {
		b, e := hex.DecodeString(digest)
		if e != nil || len(b) != 32 {
			return errors.New("invalid OAuth credential hash")
		}
	}
	seen := map[string]bool{}
	for _, redirect := range c.RedirectURIs {
		r, e := url.Parse(redirect)
		if e != nil || r.Scheme != "https" || r.Hostname() == "" || r.User != nil || r.Fragment != "" || seen[redirect] || strings.ContainsAny(redirect, "*\\\r\n") {
			return errors.New("OAuth redirects must be distinct exact HTTPS URLs")
		}
		if _, e = url.ParseQuery(r.RawQuery); e != nil {
			return errors.New("invalid OAuth redirect query")
		}
		for _, key := range []string{"code", "error", "state", "iss"} {
			if r.Query().Has(key) {
				return errors.New("OAuth redirect contains reserved response parameters")
			}
		}
		seen[redirect] = true
	}
	return nil
}
