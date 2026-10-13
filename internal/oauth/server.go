package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/sunforwork/SuperProd_MCP/internal/config"
	"github.com/sunforwork/SuperProd_MCP/internal/httpx"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
	"github.com/sunforwork/SuperProd_MCP/internal/webui"
	"golang.org/x/time/rate"
)

const Scope = "mcp"
const cookieName = "spmcp_oauth_csrf"

type Server struct {
	Config                             config.OAuth
	Store                              *storage.Store
	Hosts                              []string
	Now                                func() time.Time
	AccountID, DisplayName, CookieName string
	AccessTokenIssued                  func(string, int64)
	loginRate, requestRate, tokenRate  *rate.Limiter
}

func (s *Server) cookieName() string {
	if s.CookieName != "" {
		return s.CookieName
	}
	return cookieName
}

type grant struct {
	ClientID   string `json:"clientId"`
	Redirect   string `json:"redirect,omitempty"`
	Resource   string `json:"resource"`
	Scope      string `json:"scope"`
	Subject    string `json:"subject"`
	Generation string `json:"generation"`
	Challenge  string `json:"challenge,omitempty"`
	State      string `json:"state,omitempty"`
	CookieHash string `json:"cookieHash,omitempty"`
	Family     string `json:"family,omitempty"`
	Used       bool   `json:"used,omitempty"`
}

func New(c config.OAuth, store *storage.Store, hosts []string, syncPassword ...string) (*Server, error) {
	if e := c.Validate(); e != nil {
		return nil, e
	}
	if c.UseSyncPassword {
		if len(syncPassword) != 1 || syncPassword[0] == "" {
			return nil, errors.New("OAuth requires the sync password file")
		}
		c.PasswordHash = config.Hash(syncPassword[0])
	}
	return &Server{Config: c, Store: store, Hosts: hosts, Now: time.Now,
		loginRate:   rate.NewLimiter(rate.Every(3*time.Second), 10),
		requestRate: rate.NewLimiter(rate.Every(time.Second), 30),
		tokenRate:   rate.NewLimiter(rate.Every(time.Second), 60)}, nil
}
func (s *Server) Challenge() string {
	return `Bearer resource_metadata="` + s.Config.Issuer + `/.well-known/oauth-protected-resource/mcp", scope="` + Scope + `"`
}
func contains(list []string, value string) bool {
	for _, v := range list {
		if v == value {
			return true
		}
	}
	return false
}
func secureEqual(a, b string) bool         { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func recordKey(kind, secret string) string { return kind + ":" + config.Hash(secret) }
func encode(v grant) []byte                { b, _ := json.Marshal(v); return b }
func decode(r storage.OAuthRecord, now int64, generation string) (grant, error) {
	var v grant
	if r.Expires <= now || json.Unmarshal(r.Value, &v) != nil || v.Generation != generation {
		return v, errors.New("invalid grant")
	}
	return v, nil
}
func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func oauthError(w http.ResponseWriter, status int, code, description string) {
	jsonResponse(w, status, map[string]string{"error": code, "error_description": description})
}
func unique(v url.Values) bool {
	for _, values := range v {
		if len(values) != 1 {
			return false
		}
	}
	return true
}
func pkceChallenge(value string) bool {
	b, e := base64.RawURLEncoding.DecodeString(value)
	return e == nil && len(value) == 43 && len(b) == 32
}
func validVerifier(v string) bool {
	if len(v) < 43 || len(v) > 128 {
		return false
	}
	for _, c := range v {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-._~", c)) {
			return false
		}
	}
	return true
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	if !contains(s.Hosts, r.Host) {
		httpx.Reject(w, r, "host forbidden", 403)
		return
	}
	if len(r.URL.RawQuery) > 8192 {
		httpx.Reject(w, r, "request too large", 414)
		return
	}
	switch r.URL.Path {
	case "/.well-known/oauth-authorization-server", "/.well-known/openid-configuration":
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		jsonResponse(w, 200, map[string]any{
			"issuer": s.Config.Issuer, "authorization_endpoint": s.Config.Issuer + "/oauth/authorize",
			"token_endpoint": s.Config.Issuer + "/oauth/token", "revocation_endpoint": s.Config.Issuer + "/oauth/revoke",
			"response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"},
			"code_challenge_methods_supported": []string{"S256"}, "scopes_supported": []string{Scope},
			"token_endpoint_auth_methods_supported":          []string{"client_secret_basic", "client_secret_post"},
			"revocation_endpoint_auth_methods_supported":     []string{"client_secret_basic", "client_secret_post"},
			"authorization_response_iss_parameter_supported": true,
		})
	case "/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp":
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		jsonResponse(w, 200, map[string]any{"resource": s.Config.Resource(), "authorization_servers": []string{s.Config.Issuer}, "scopes_supported": []string{Scope}, "bearer_methods_supported": []string{"header"}, "resource_name": "Super Productivity MCP"})
	case "/oauth/authorize":
		if r.Method == "GET" {
			s.authorize(w, r)
		} else if r.Method == "POST" {
			s.approve(w, r)
		} else {
			w.WriteHeader(405)
		}
	case "/oauth/token", "/oauth/revoke":
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		if !s.tokenRate.Allow() {
			oauthError(w, 429, "temporarily_unavailable", "Try again later.")
			return
		}
		if !s.parseForm(w, r) {
			return
		}
		if !s.authenticateClient(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="OAuth client"`)
			oauthError(w, 401, "invalid_client", "Client authentication failed.")
			return
		}
		if r.URL.Path == "/oauth/revoke" {
			s.revoke(w, r)
		} else {
			s.token(w, r)
		}
	default:
		httpx.Reject(w, r, "not found", 404)
	}
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if !s.requestRate.Allow() {
		oauthError(w, 429, "temporarily_unavailable", "Try again later.")
		return
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil || !unique(q) || q.Get("client_id") != s.Config.ClientID || !contains(s.Config.RedirectURIs, q.Get("redirect_uri")) {
		oauthError(w, 400, "invalid_request", "Client and exact redirect URI must be registered.")
		return
	}
	v := grant{ClientID: q.Get("client_id"), Redirect: q.Get("redirect_uri"), Resource: q.Get("resource"), Scope: q.Get("scope"), State: q.Get("state"), Challenge: q.Get("code_challenge"), Generation: s.Config.Generation(), Subject: "oauth:" + config.Hash(s.Config.Username)}
	if v.Scope == "" {
		v.Scope = Scope
	}
	if q.Get("response_type") != "code" {
		s.redirect(w, r, v, "", "unsupported_response_type")
		return
	}
	if v.Resource != s.Config.Resource() {
		s.redirect(w, r, v, "", "invalid_target")
		return
	}
	if v.Scope != Scope {
		s.redirect(w, r, v, "", "invalid_scope")
		return
	}
	if q.Get("code_challenge_method") != "S256" || !pkceChallenge(v.Challenge) || v.State == "" || len(v.State) > 1024 {
		s.redirect(w, r, v, "", "invalid_request")
		return
	}
	id, csrf := config.Secret(), config.Secret()
	v.CookieHash = config.Hash(csrf)
	expires := s.Now().Add(10 * time.Minute).Unix()
	e = s.Store.Transaction(r.Context(), func(tx *storage.Tx) error {
		if e := tx.OAuthPrune(s.Now().Unix()); e != nil {
			return e
		}
		count, e := tx.OAuthPendingCount()
		if e != nil {
			return e
		}
		if count >= 1024 {
			return errors.New("too many pending requests")
		}
		return tx.OAuthPut(recordKey("request", id), encode(v), expires)
	})
	if e != nil {
		oauthError(w, 503, "temporarily_unavailable", "Authorization storage unavailable.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: csrf, Path: "/oauth/authorize", MaxAge: 600, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	s.render(w, id, v, "", http.StatusOK)
}

func (s *Server) formPolicy(v grant) string {
	const start = "default-src 'none'; form-action 'self'"
	const end = "; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'"
	callback, err := url.Parse(v.Redirect)
	if err != nil || !contains(s.Config.RedirectURIs, v.Redirect) || strings.ContainsAny(callback.Host, " \t\r\n;\"'") {
		return start + end
	}

	return start + " " + callback.Scheme + "://" + callback.Host + end
}

func (s *Server) render(w http.ResponseWriter, id string, v grant, message string, status int, submittedUsername ...string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", s.formPolicy(v))
	w.Header().Set("X-Frame-Options", "DENY")
	help := "请使用此账号的授权密码。WebDAV 密码另行配置。"
	if s.Config.UseSyncPassword {
		help = "此账号的授权密码与同步加密密码相同，WebDAV 密码另行配置。"
	}
	name := s.DisplayName
	if name == "" {
		name = s.Config.Username
	}
	w.WriteHeader(status)
	username := ""
	if len(submittedUsername) > 0 {
		username = submittedUsername[0]
	}
	webui.Render(w, "oauth", map[string]string{"ID": id, "Client": v.ClientID, "Redirect": v.Redirect, "Scope": v.Scope, "Error": message, "PasswordHelp": help, "AccountID": s.AccountID, "Name": name, "Username": username})
}
func (s *Server) parseForm(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.RawQuery != "" {
		oauthError(w, 400, "invalid_request", "POST parameters must be in the request body.")
		return false
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/x-www-form-urlencoded" {
		oauthError(w, 400, "invalid_request", "Use a form-encoded request body.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if r.ParseForm() != nil || !unique(r.PostForm) {
		oauthError(w, 400, "invalid_request", "Malformed or repeated form parameters.")
		return false
	}
	return true
}
func (s *Server) approve(w http.ResponseWriter, r *http.Request) {
	if !s.parseForm(w, r) {
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.Config.Issuer {
		oauthError(w, 403, "invalid_request", "Origin does not match this authorization server.")
		return
	}
	id := r.PostForm.Get("request_id")
	record, e := s.Store.OAuthGet(r.Context(), recordKey("request", id))
	if e != nil {
		oauthError(w, 400, "invalid_request", "Authorization request expired or already completed.")
		return
	}
	v, e := decode(record, s.Now().Unix(), s.Config.Generation())
	cookie, ce := r.Cookie(s.cookieName())
	if e != nil || ce != nil || !secureEqual(v.CookieHash, config.Hash(cookie.Value)) {
		oauthError(w, 403, "invalid_request", "Authorization session verification failed.")
		return
	}
	decision := r.PostForm.Get("decision")
	if decision != "allow" && decision != "deny" {
		oauthError(w, 400, "invalid_request", "Choose authorize or cancel.")
		return
	}
	if decision == "allow" {
		if !s.loginRate.Allow() {
			oauthError(w, 429, "temporarily_unavailable", "Try again later.")
			return
		}
		if !secureEqual(config.Hash(r.PostForm.Get("username")), config.Hash(s.Config.Username)) || !secureEqual(config.Hash(r.PostForm.Get("password")), s.Config.PasswordHash) {
			s.render(w, id, v, "用户名或 OAuth 登录密码不正确。", http.StatusUnauthorized, r.PostForm.Get("username"))
			return
		}
	}
	code := config.Secret()
	e = s.Store.Transaction(r.Context(), func(tx *storage.Tx) error {
		row, e := tx.OAuthGet(recordKey("request", id))
		if e != nil {
			return e
		}
		if _, e = decode(row, s.Now().Unix(), s.Config.Generation()); e != nil {
			return e
		}
		if e = tx.OAuthDelete(recordKey("request", id)); e != nil {
			return e
		}
		if decision == "deny" {
			return nil
		}
		stored := v
		stored.CookieHash = ""
		stored.State = ""
		return tx.OAuthPut(recordKey("code", code), encode(stored), s.Now().Add(2*time.Minute).Unix())
	})
	if e != nil {
		oauthError(w, 400, "invalid_request", "Authorization request expired or already completed.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: "", Path: "/oauth/authorize", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	if decision == "deny" {
		s.redirect(w, r, v, "", "access_denied")
	} else {
		s.redirect(w, r, v, code, "")
	}
}
func (s *Server) redirect(w http.ResponseWriter, r *http.Request, v grant, code, err string) {
	w.Header().Set("Content-Security-Policy", s.formPolicy(v))
	u, _ := url.Parse(v.Redirect)
	q := u.Query()
	if code != "" {
		q.Set("code", code)
	} else {
		q.Set("error", err)
	}
	if v.State != "" {
		q.Set("state", v.State)
	}
	q.Set("iss", s.Config.Issuer)
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusSeeOther)
}
func (s *Server) authenticateClient(r *http.Request) bool {
	var id, secret string
	if len(r.Header.Values("Authorization")) > 1 {
		return false
	}
	if r.Header.Get("Authorization") != "" {
		var ok bool
		id, secret, ok = r.BasicAuth()
		if !ok || r.PostForm.Has("client_secret") {
			return false
		}
		if bodyID := r.PostForm.Get("client_id"); bodyID != "" && bodyID != id {
			return false
		}
	} else {
		id, secret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	return secureEqual(id, s.Config.ClientID) && secureEqual(config.Hash(secret), s.Config.ClientSecretHash)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	kind := r.PostForm.Get("grant_type")
	if kind != "authorization_code" && kind != "refresh_token" {
		oauthError(w, 400, "unsupported_grant_type", "Use authorization_code or refresh_token.")
		return
	}
	access, refresh := "spat_"+config.Secret(), "sprt_"+config.Secret()
	var out grant
	var expires int64
	var invalid bool
	e := s.Store.Transaction(r.Context(), func(tx *storage.Tx) error {
		now := s.Now().Unix()
		secret, recordType := r.PostForm.Get("code"), "code"
		if kind == "refresh_token" {
			secret, recordType = r.PostForm.Get("refresh_token"), "refresh"
		}
		row, e := tx.OAuthGet(recordKey(recordType, secret))
		if e == storage.ErrNotFound {
			invalid = true
			return nil
		}
		if e != nil {
			return e
		}
		v, e := decode(row, now, s.Config.Generation())
		if e != nil || v.ClientID != s.Config.ClientID || v.Resource != s.Config.Resource() {
			invalid = true
			return nil
		}
		if resource := r.PostForm.Get("resource"); resource != v.Resource && !(kind == "refresh_token" && resource == "") {
			invalid = true
			return nil
		}
		if kind == "authorization_code" {
			verifier := r.PostForm.Get("code_verifier")
			digest := sha256.Sum256([]byte(verifier))
			if r.PostForm.Get("redirect_uri") != v.Redirect || !validVerifier(verifier) || !secureEqual(base64.RawURLEncoding.EncodeToString(digest[:]), v.Challenge) {
				invalid = true
				return nil
			}
			if e = tx.OAuthDelete(recordKey("code", secret)); e != nil {
				return e
			}
			v.Family = config.Secret()
			expires = s.Now().Add(30 * 24 * time.Hour).Unix()
			if e = tx.OAuthPut(recordKey("family", v.Family), encode(v), expires); e != nil {
				return e
			}
		} else {
			family, e := tx.OAuthGet(recordKey("family", v.Family))
			if e == storage.ErrNotFound {
				invalid = true
				return nil
			}
			if e != nil {
				return e
			}
			if _, e = decode(family, now, s.Config.Generation()); e != nil {
				invalid = true
				return nil
			}
			if v.Used {
				invalid = true
				return tx.OAuthDelete(recordKey("family", v.Family))
			}
			if scope := r.PostForm.Get("scope"); scope != "" && scope != v.Scope {
				invalid = true
				return nil
			}
			expires = family.Expires
			v.Used = true
			if e = tx.OAuthPut(recordKey("refresh", secret), encode(v), row.Expires); e != nil {
				return e
			}
			v.Used = false
		}
		v.Challenge = ""
		v.Redirect = ""
		v.State = ""
		v.CookieHash = ""
		out = v
		if e = tx.OAuthPut(recordKey("access", access), encode(v), min(s.Now().Add(15*time.Minute).Unix(), expires)); e != nil {
			return e
		}
		return tx.OAuthPut(recordKey("refresh", refresh), encode(v), expires)
	})
	if e != nil {
		oauthError(w, 503, "temporarily_unavailable", "Token storage unavailable.")
		return
	}
	if invalid {
		oauthError(w, 400, "invalid_grant", "Grant expired, consumed or failed verification.")
		return
	}
	if s.AccessTokenIssued != nil {
		s.AccessTokenIssued(access, min(s.Now().Unix()+900, expires))
	}
	jsonResponse(w, 200, map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": min(int64(900), expires-s.Now().Unix()), "refresh_token": refresh, "scope": out.Scope})
}

func (s *Server) Verify(ctx context.Context, token string) (string, error) {
	if !strings.HasPrefix(token, "spat_") || len(token) != 48 {
		return "", errors.New("invalid access token")
	}
	var subject string
	e := s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		r, e := tx.OAuthGet(recordKey("access", token))
		if e != nil {
			return e
		}
		v, e := decode(r, s.Now().Unix(), s.Config.Generation())
		if e != nil {
			return e
		}
		if v.ClientID != s.Config.ClientID || v.Resource != s.Config.Resource() || v.Scope != Scope || v.Subject != "oauth:"+config.Hash(s.Config.Username) {
			return errors.New("invalid token binding")
		}
		family, e := tx.OAuthGet(recordKey("family", v.Family))
		if e != nil {
			return e
		}
		if _, e = decode(family, s.Now().Unix(), s.Config.Generation()); e != nil {
			return e
		}
		subject = v.Subject
		return nil
	})
	return subject, e
}
func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	token := r.PostForm.Get("token")
	e := s.Store.Transaction(r.Context(), func(tx *storage.Tx) error {
		for _, kind := range []string{"access", "refresh"} {
			row, e := tx.OAuthGet(recordKey(kind, token))
			if e == storage.ErrNotFound {
				continue
			}
			if e != nil {
				return e
			}
			v, e := decode(row, s.Now().Unix(), s.Config.Generation())
			if e != nil || v.ClientID != s.Config.ClientID {
				continue
			}
			return tx.OAuthDelete(recordKey("family", v.Family))
		}
		return nil
	})
	if e != nil {
		oauthError(w, 503, "temporarily_unavailable", "Token storage unavailable.")
		return
	}
	jsonResponse(w, 200, map[string]any{})
}
