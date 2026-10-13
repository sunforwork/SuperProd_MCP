package admin

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sunforwork/SuperProd_MCP/internal/config"
	"github.com/sunforwork/SuperProd_MCP/internal/mcpserver"
	"github.com/sunforwork/SuperProd_MCP/internal/oauth"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
	"github.com/sunforwork/SuperProd_MCP/internal/webdav"
	"github.com/sunforwork/SuperProd_MCP/internal/webui"
	"golang.org/x/time/rate"
)

type runtime struct {
	mu       sync.RWMutex
	account  Account
	store    *storage.Store
	service  *service.Service
	oauth    *oauth.Server
	dav, mcp http.Handler
}
type Manager struct {
	mu          sync.RWMutex
	config      config.Config
	state       State
	key         []byte
	runtimes    map[string]*runtime
	sessionMu   sync.Mutex
	sessions    map[string]*session
	loginRate   *rate.Limiter
	Now         func() time.Time
	tokenOwners sync.Map
}
type tokenOwner struct {
	ID      string
	Expires int64
}

func Open(c config.Config) (*Manager, error) {
	s, key, err := loadState(c)
	if err != nil {
		return nil, err
	}
	m := &Manager{config: c, state: s, key: key, runtimes: map[string]*runtime{}, sessions: map[string]*session{}, loginRate: rate.NewLimiter(rate.Every(3*time.Second), 5), Now: time.Now}
	if err = m.validate(s); err != nil {
		return nil, err
	}
	for _, a := range s.Accounts {
		if err = os.MkdirAll(filepath.Dir(a.Config.Database), 0700); err != nil {
			m.Close()
			return nil, err
		}
		store, e := storage.Open(a.Config.Database)
		if e != nil {
			m.Close()
			return nil, e
		}
		rt := &runtime{store: store}
		m.runtimes[a.ID] = rt
		if err = m.bind(rt, a); err != nil {
			m.Close()
			return nil, err
		}
		if err = m.indexTokens(a.ID, store); err != nil {
			m.Close()
			return nil, err
		}
	}
	return m, nil
}
func (m *Manager) indexTokens(id string, store *storage.Store) error {
	entries, err := store.OAuthAccessIndex(context.Background())
	if err != nil {
		return err
	}
	for _, entry := range entries {
		m.tokenOwners.Store(entry.Hash, tokenOwner{ID: id, Expires: entry.Expires})
	}
	return nil
}
func (m *Manager) Close() {
	for _, rt := range m.runtimes {
		rt.store.Close()
	}
}
func canonicalFile(file string) (string, error) {
	p, err := filepath.Abs(file)
	if err != nil {
		return "", err
	}
	if resolved, e := filepath.EvalSymlinks(p); e == nil {
		p = resolved
	} else if parent, e := filepath.EvalSymlinks(filepath.Dir(p)); e == nil {
		p = filepath.Join(parent, filepath.Base(p))
	}
	if filepath.Separator == '\\' {
		p = strings.ToLower(p)
	}
	return filepath.Clean(p), nil
}
func (m *Manager) validate(s State) error {
	if !ValidUsername(s.adminName()) {
		return errors.New("管理员用户名不正确")
	}
	ids, users, clients, databases, passwords := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, a := range s.Accounts {
		c := a.Config
		if a.ID == "" || ids[a.ID] || len(a.Name) > 128 || c.WebDAVUsername == "" || len(c.WebDAVUsername) > 64 || strings.ContainsAny(c.WebDAVUsername, ":\r\n") || c.WebDAVUsername == s.adminName() || users[c.WebDAVUsername] {
			return errors.New("账号标识或用户名重复、保留或格式不正确")
		}
		if c.OAuth == nil || c.OAuth.Username != c.WebDAVUsername || clients[c.OAuth.ClientID] || c.OAuth.Issuer != m.config.OAuth.Issuer {
			return errors.New("账号 OAuth 配置必须独立且使用相同 issuer")
		}
		if err := c.OAuth.Validate(); err != nil {
			return err
		}
		if len(c.WebDAVPasswordHash) != 64 {
			return errors.New("invalid WebDAV password hash")
		}
		db, err := canonicalFile(c.Database)
		if err != nil || databases[db] {
			return errors.New("用户不能共用数据库")
		}
		pw, err := canonicalFile(c.SyncPasswordFile)
		if err != nil || passwords[pw] || pw == db {
			return errors.New("用户不能共用密码文件")
		}
		ids[a.ID], users[c.WebDAVUsername], clients[c.OAuth.ClientID], databases[db], passwords[pw] = true, true, true, true, true
	}
	if !ids["primary"] {
		return errors.New("default account missing")
	}
	return nil
}
func (m *Manager) bind(rt *runtime, a Account) error {
	password, err := config.ReadPasswordFile(a.Config.SyncPasswordFile)
	if err != nil {
		return err
	}
	svc, err := service.New(rt.store, password, a.Config.Timezone, a.Config.ExperimentalWrites)
	if err != nil {
		return err
	}
	as, err := oauth.New(*a.Config.OAuth, rt.store, m.config.AllowedHosts, password)
	if err != nil {
		return err
	}
	as.AccountID, as.DisplayName = a.ID, a.Name
	as.AccessTokenIssued = func(token string, expires int64) {
		now := time.Now().Unix()
		m.tokenOwners.Range(func(key, value any) bool {
			if value.(tokenOwner).Expires <= now {
				m.tokenOwners.Delete(key)
			}
			return true
		})
		m.tokenOwners.Store(config.Hash(token), tokenOwner{ID: a.ID, Expires: expires})
	}
	if a.ID != "primary" {
		as.CookieName = "spmcp_oauth_csrf_" + a.ID
	}

	var digest [32]byte
	b, err := hex.DecodeString(a.Config.WebDAVPasswordHash)
	if err != nil || len(b) != 32 {
		return errors.New("invalid credential hash")
	}
	copy(digest[:], b)
	rt.account, rt.service, rt.oauth = a, svc, as
	rt.dav = &webdav.Handler{Store: rt.store, Username: a.Config.WebDAVUsername, PasswordHash: digest, AllowedOrigins: m.config.AllowedOrigins}
	rt.mcp = mcpserver.OAuthHandler(mcpserver.New(svc), as.Verify, as.Challenge(), m.config.AllowedHosts, m.config.AllowedOrigins)
	return nil
}
func (m *Manager) find(id, user, client string) *runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, a := range m.state.Accounts {
		if id != "" && a.ID == id || user != "" && a.Config.WebDAVUsername == user || client != "" && a.Config.OAuth.ClientID == client {
			return m.runtimes[a.ID]
		}
	}
	return nil
}
func (m *Manager) snapshot() []*runtime {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*runtime, 0, len(m.state.Accounts))
	for _, a := range m.state.Accounts {
		out = append(out, m.runtimes[a.ID])
	}
	return out
}

func (m *Manager) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if !contains(m.config.AllowedHosts, r.Host) {
		http.Error(w, "host forbidden", 403)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/assets/ui/") {
		webui.ServeHTTP(w, r)
		return
	}
	if r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
		m.adminHTTP(w, r)
		return
	}
	if r.URL.Path == "/setup" || r.URL.Path == "/setup/" {
		http.Redirect(w, r, "/admin/login", 303)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/setup/") {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/healthz" {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		respond(w, 200, map[string]any{"status": "ok", "releaseReady": false, "multiUser": true, "installation": "ready", "version": "2.3.0"})
		return
	}
	if strings.HasPrefix(r.URL.Path, "/.well-known/oauth-") || r.URL.Path == "/.well-known/openid-configuration" {
		m.mu.RLock()
		rt := m.runtimes["primary"]
		m.mu.RUnlock()
		rt.mu.RLock()
		defer rt.mu.RUnlock()
		rt.oauth.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/oauth/") {
		m.oauthHTTP(w, r)
		return
	}
	if r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/") {
		if r.URL.Path != "/mcp" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(r.Header.Values("Authorization")) == 1 && len(r.Header.Values("X-API-Key")) == 0 && len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			key := config.Hash(parts[1])
			if entry, ok := m.tokenOwners.Load(key); ok {
				owner := entry.(tokenOwner)
				if owner.Expires > time.Now().Unix() {
					if rt := m.find(owner.ID, "", ""); rt != nil {
						rt.mu.RLock()
						if rt.account.Enabled {
							rt.mcp.ServeHTTP(w, r)
							rt.mu.RUnlock()
							return
						}
						rt.mu.RUnlock()
					}
				} else {
					m.tokenOwners.Delete(key)
				}
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+m.config.OAuth.Issuer+`/.well-known/oauth-protected-resource/mcp", scope="mcp"`)
		http.Error(w, "authentication required", 401)
		return
	}
	if r.Method == "OPTIONS" {
		m.mu.RLock()
		rt := m.runtimes["primary"]
		m.mu.RUnlock()
		rt.mu.RLock()
		defer rt.mu.RUnlock()
		rt.dav.ServeHTTP(w, r)
		return
	}
	user, _, ok := r.BasicAuth()
	if ok {
		if rt := m.find("", user, ""); rt != nil {
			rt.mu.RLock()
			if rt.account.Enabled && rt.account.Config.WebDAVUsername == user {
				rt.dav.ServeHTTP(w, r)
				rt.mu.RUnlock()
				return
			}
			rt.mu.RUnlock()
		}
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="Super Productivity", charset="UTF-8"`)
	http.Error(w, "authentication required", 401)
}
func (m *Manager) oauthHTTP(w http.ResponseWriter, r *http.Request) {
	client, account := "", ""
	switch {
	case r.URL.Path == "/oauth/authorize" && r.Method == "GET":
		client = r.URL.Query().Get("client_id")
	case r.Method == "POST":
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", 400)
			return
		}
		if r.URL.Path == "/oauth/authorize" {
			account = r.PostForm.Get("account_id")
			if account == "" {
				account = "primary"
			}
		} else {
			client = r.PostForm.Get("client_id")
			if id, _, ok := r.BasicAuth(); ok {
				client = id
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if rt := m.find(account, "", client); rt != nil {
		rt.mu.RLock()
		if rt.account.Enabled && ((client != "" && rt.account.Config.OAuth.ClientID == client) || (account != "" && rt.account.ID == account)) {
			rt.oauth.ServeHTTP(w, r)
			rt.mu.RUnlock()
			return
		}
		rt.mu.RUnlock()
	}
	respond(w, 400, map[string]string{"error": "invalid_client", "error_description": "Client unavailable."})
}

type Change struct {
	Name               string `json:"name"`
	Username           string `json:"username"`
	Enabled            *bool  `json:"enabled"`
	WebDAVPassword     string `json:"webdavPassword"`
	OAuthPassword      string `json:"oauthPassword"`
	SyncPassword       string `json:"syncPassword"`
	ClientSecret       string `json:"clientSecret"`
	CredentialMode     string `json:"credentialMode"`
	RotateClientSecret bool   `json:"rotateClientSecret"`
}

func validSecret(v string) bool {
	return v != "" && len(v) <= 256 && !strings.ContainsAny(v, "\r\n\x00")
}
func (m *Manager) Create(ch Change) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ValidUsername(ch.Username) {
		return "", errors.New("请填写用户名")
	}
	id := uuid.NewString()
	dir := filepath.Join(filepath.Dir(m.config.Admin.StateFile), "users", id)
	secrets := Secrets{WebDAV: ch.WebDAVPassword, OAuth: ch.OAuthPassword, ClientSecret: config.Secret()}
	pw := ch.SyncPassword
	if secrets.WebDAV == "" {
		secrets.WebDAV = config.Secret()
	}
	if secrets.OAuth == "" {
		secrets.OAuth = config.Secret()
	}
	if pw == "" {
		pw = config.Secret()
	}
	if !validSecret(secrets.WebDAV) || !validSecret(secrets.OAuth) || !validSecret(pw) {
		return "", errors.New("密码格式不正确")
	}
	c := m.state.Accounts[0].Config
	c.Admin = nil
	c.Database = filepath.Join(dir, "sync.db")
	c.SyncPasswordFile = filepath.Join(dir, "sync-password.txt")
	c.WebDAVUsername = ch.Username
	c.WebDAVPasswordHash = config.Hash(secrets.WebDAV)
	c.MCPKeyHashes = nil
	c.OAuth = &config.OAuth{Issuer: m.config.OAuth.Issuer, Username: ch.Username, PasswordHash: config.Hash(secrets.OAuth), ClientID: "spmcp-" + config.Secret(), ClientSecretHash: config.Hash(secrets.ClientSecret), RedirectURIs: append([]string(nil), m.config.OAuth.RedirectURIs...)}
	name := ch.Name
	if name == "" {
		name = ch.Username
	}
	a := Account{ID: id, Name: name, Enabled: true, Config: c, Secrets: secrets}
	next := cloneState(m.state)
	next.Accounts = append(next.Accounts, a)
	if err := m.validate(next); err != nil {
		return "", err
	}
	if err := atomicWrite(c.SyncPasswordFile, []byte(pw+"\n")); err != nil {
		return "", err
	}
	store, err := storage.Open(c.Database)
	if err != nil {
		return "", err
	}
	rt := &runtime{store: store}
	if err = m.bind(rt, a); err != nil {
		store.Close()
		return "", err
	}
	if err = saveState(m.config.Admin.StateFile, m.key, next); err != nil {
		store.Close()
		return "", err
	}
	m.state = next
	m.runtimes[id] = rt
	return id, nil
}
func (m *Manager) Update(ctx context.Context, id string, ch Change) error {
	rt := m.find(id, "", "")
	if rt == nil {
		return errors.New("用户不存在")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	a := cloneState(State{Accounts: []Account{rt.account}}).Accounts[0]
	revoke := false
	if ch.Name != "" {
		a.Name = ch.Name
	}
	if ch.Username != "" && ch.Username != a.Config.WebDAVUsername {
		if !ValidUsername(ch.Username) {
			return errors.New("用户名格式不正确")
		}
		a.Config.WebDAVUsername = ch.Username
		a.Config.OAuth.Username = ch.Username
		revoke = true
	}
	if ch.Enabled != nil {
		if *ch.Enabled != a.Enabled {
			revoke = true
		}
		a.Enabled = *ch.Enabled
	}
	if ch.CredentialMode != "" && ch.CredentialMode != "reset" && ch.CredentialMode != "record" {
		return errors.New("invalid credential mode")
	}
	record := ch.CredentialMode == "record"
	for _, item := range []struct {
		value string
		hash  *string
		saved *string
	}{{ch.WebDAVPassword, &a.Config.WebDAVPasswordHash, &a.Secrets.WebDAV}, {ch.OAuthPassword, &a.Config.OAuth.PasswordHash, &a.Secrets.OAuth}, {ch.ClientSecret, &a.Config.OAuth.ClientSecretHash, &a.Secrets.ClientSecret}} {
		if item.value == "" {
			continue
		}
		if !validSecret(item.value) {
			return errors.New("密码格式不正确")
		}
		expected := *item.hash
		if item.saved == &a.Secrets.OAuth && a.Config.OAuth.UseSyncPassword {
			pw, e := config.ReadPasswordFile(a.Config.SyncPasswordFile)
			if e != nil {
				return e
			}
			expected = config.Hash(pw)
		}
		if record {
			if config.Hash(item.value) != expected {
				return errors.New("补录凭据与当前配置不匹配")
			}
			*item.saved = item.value
		} else {
			*item.hash = config.Hash(item.value)
			*item.saved = item.value
			revoke = true
			if item.saved == &a.Secrets.OAuth {
				a.Config.OAuth.UseSyncPassword = false
			}
		}
	}
	if ch.RotateClientSecret {
		a.Secrets.ClientSecret = config.Secret()
		a.Config.OAuth.ClientSecretHash = config.Hash(a.Secrets.ClientSecret)
		revoke = true
	}
	var oldPassword []byte
	if ch.SyncPassword != "" {
		if !validSecret(ch.SyncPassword) {
			return errors.New("加密密码格式不正确")
		}
		files, e := rt.store.List(ctx, "/super-productivity")
		if e != nil {
			return e
		}
		codec := p.NewCodec(ch.SyncPassword)
		for _, file := range files {
			if file.Dir {
				continue
			}
			f, e := rt.store.Get(ctx, file.Path)
			if e != nil {
				return e
			}
			if _, _, e = codec.Decode(f.Body, true); e != nil {
				return errors.New("密码无法解密现有同步文件；不支持已有数据整库换密")
			}
		}
		oldPassword, e = os.ReadFile(a.Config.SyncPasswordFile)
		if e != nil {
			return e
		}
		if a.Config.OAuth.UseSyncPassword && strings.TrimRight(string(oldPassword), "\r\n") != ch.SyncPassword {
			revoke = true
		}
	}

	if revoke {
		a.Config.OAuth.SessionGeneration = config.Secret()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	next := cloneState(m.state)
	for i := range next.Accounts {
		if next.Accounts[i].ID == id {
			next.Accounts[i] = a
		}
	}
	if err := m.validate(next); err != nil {
		return err
	}
	if oldPassword != nil {
		if err := atomicWrite(a.Config.SyncPasswordFile, []byte(ch.SyncPassword+"\n")); err != nil {
			return err
		}
	}
	replacement := &runtime{store: rt.store}
	if err := m.bind(replacement, a); err != nil {
		if oldPassword != nil {
			_ = atomicWrite(a.Config.SyncPasswordFile, oldPassword)
		}
		return err
	}
	if err := saveState(m.config.Admin.StateFile, m.key, next); err != nil {
		if oldPassword != nil {
			_ = atomicWrite(a.Config.SyncPasswordFile, oldPassword)
		}
		return err
	}
	if revoke {
		if err := rt.store.RevokeOAuth(ctx); err != nil {
			a.Enabled = false
			replacement.account.Enabled = false
			for i := range next.Accounts {
				if next.Accounts[i].ID == id {
					next.Accounts[i].Enabled = false
				}
			}
			_ = saveState(m.config.Admin.StateFile, m.key, next)
			m.state = next
			rt.account = replacement.account
			rt.service = replacement.service
			rt.oauth = replacement.oauth
			rt.dav = replacement.dav
			rt.mcp = replacement.mcp
			return errors.New("授权撤销失败，账号已停用")
		}
	}
	m.state = next
	rt.account = replacement.account
	rt.service = replacement.service
	rt.oauth = replacement.oauth
	rt.dav = replacement.dav
	rt.mcp = replacement.mcp
	return nil
}
func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
func respond(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
