package admin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sunforwork/SuperProd_MCP/internal/config"
	"github.com/sunforwork/SuperProd_MCP/internal/webui"
)

const sessionCookie = "spmcp_admin_session"

type session struct {
	CSRF          string
	Generation    string
	Created, Seen time.Time
}

func secureEqual(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func (m *Manager) currentSession(r *http.Request) (string, *session) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", nil
	}
	key := config.Hash(c.Value)
	m.mu.RLock()
	generation := m.state.generation()
	m.mu.RUnlock()
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	s := m.sessions[key]
	now := m.Now()
	if s == nil {
		return "", nil
	}
	if s.Generation != generation || now.Sub(s.Seen) >= 30*time.Minute || now.Sub(s.Created) >= 8*time.Hour {
		delete(m.sessions, key)
		return "", nil
	}
	copy := *s
	return key, &copy
}
func (m *Manager) touchSession(key string) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	if s := m.sessions[key]; s != nil {
		s.Seen = m.Now()
	}
}
func adminCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path, MaxAge: maxAge, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}
func (m *Manager) adminHTTP(w http.ResponseWriter, r *http.Request) {

	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
		defer func() { _, _ = io.Copy(io.Discard, r.Body) }()
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if origin := r.Header.Get("Origin"); origin != "" && origin != m.config.OAuth.Issuer {
		respond(w, 403, map[string]string{"error": "origin forbidden"})
		return
	}
	if r.URL.Path == "/admin/login" {
		m.loginHTTP(w, r)
		return
	}
	key, s := m.currentSession(r)
	if s == nil {
		if r.URL.Path == "/admin" && r.Method == "GET" {
			http.Redirect(w, r, "/admin/login", 303)
		} else {
			respond(w, 401, map[string]string{"error": "请登录管理员账号"})
		}
		return
	}
	if r.URL.Path == "/admin" || r.URL.Path == "/admin/" {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		m.touchSession(key)
		m.mu.RLock()
		name := m.state.adminName()
		m.mu.RUnlock()
		webui.Render(w, "admin", map[string]string{"Username": name})
		return
	}
	if r.URL.Path == "/admin/api/session" && r.Method == "GET" {
		m.mu.RLock()
		name := m.state.adminName()
		m.mu.RUnlock()
		respond(w, 200, map[string]string{"username": name, "csrf": s.CSRF})
		return
	}
	if r.Method != "GET" && !secureEqual(r.Header.Get("X-CSRF-Token"), s.CSRF) {
		respond(w, 403, map[string]string{"error": "管理会话校验失败，请刷新页面"})
		return
	}
	if r.Method != "GET" {
		m.touchSession(key)
	}
	switch {
	case r.URL.Path == "/admin/api/activity" && r.Method == "POST":
		respond(w, 200, map[string]bool{"ok": true})
	case r.URL.Path == "/admin/api/logout" && r.Method == "POST":
		m.sessionMu.Lock()
		delete(m.sessions, key)
		m.sessionMu.Unlock()
		adminCookie(w, sessionCookie, "", "/admin", -1)
		respond(w, 200, map[string]bool{"ok": true})
	case r.URL.Path == "/admin/api/username" && r.Method == "POST":
		var in struct {
			Username string `json:"username"`
			Current  string `json:"currentPassword"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if !m.loginRate.Allow() {
			respond(w, 429, map[string]string{"error": "请稍后再试"})
			return
		}
		m.mu.Lock()
		if !checkPassword(m.state.AdminHash, in.Current) {
			m.mu.Unlock()
			respond(w, 403, map[string]string{"error": "当前管理员密码不正确"})
			return
		}
		next := cloneState(m.state)
		next.Version = 2
		next.AdminUsername = in.Username
		err := m.validate(next)
		if err == nil {
			err = saveState(m.config.Admin.StateFile, m.key, next)
		}
		if err == nil {
			m.state = next
		}
		m.mu.Unlock()
		if err != nil {
			adminError(w, err)
			return
		}
		m.sessionMu.Lock()
		m.sessions = map[string]*session{}
		m.sessionMu.Unlock()
		adminCookie(w, sessionCookie, "", "/admin", -1)
		respond(w, 200, map[string]bool{"ok": true})
	case r.URL.Path == "/admin/api/password" && r.Method == "POST":
		var in struct {
			Current string `json:"currentPassword"`
			New     string `json:"newPassword"`
		}
		if !readJSON(w, r, &in) {
			return
		}
		if !m.loginRate.Allow() {
			respond(w, 429, map[string]string{"error": "请稍后再试"})
			return
		}
		m.mu.Lock()
		if !checkPassword(m.state.AdminHash, in.Current) {
			m.mu.Unlock()
			respond(w, 403, map[string]string{"error": "当前管理员密码不正确"})
			return
		}
		hash, err := passwordHash(in.New)
		if err == nil {
			next := cloneState(m.state)
			next.AdminHash = hash
			err = saveState(m.config.Admin.StateFile, m.key, next)
			if err == nil {
				m.state = next
			}
		}
		m.mu.Unlock()
		if err != nil {
			adminError(w, err)
			return
		}
		m.sessionMu.Lock()
		m.sessions = map[string]*session{}
		m.sessionMu.Unlock()
		adminCookie(w, sessionCookie, "", "/admin", -1)
		respond(w, 200, map[string]bool{"ok": true})
	case r.URL.Path == "/admin/api/users" && r.Method == "GET":
		v, err := m.Users(r.Context())
		if err != nil {
			adminError(w, errors.New("无法读取存储用量"))
			return
		}
		respond(w, 200, v)
	case r.URL.Path == "/admin/api/users" && r.Method == "POST":
		var in Change
		if !readJSON(w, r, &in) {
			return
		}
		id, err := m.Create(in)
		if err != nil {
			adminError(w, err)
			return
		}
		respond(w, 201, map[string]string{"id": id})
	case strings.HasPrefix(r.URL.Path, "/admin/api/users/") && r.Method == "POST":
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/admin/api/users/"), "/")
		if len(parts) == 2 && parts[1] == "status" {
			rt := m.find(parts[0], "", "")
			if rt == nil {
				respond(w, 404, map[string]string{"error": "用户不存在"})
				return
			}
			rt.mu.RLock()
			defer rt.mu.RUnlock()
			u, err := rt.store.Usage(r.Context())
			if err != nil {
				adminError(w, errors.New("无法读取文件状态"))
				return
			}
			if u.CurrentFiles == 0 {
				respond(w, 200, map[string]string{"status": "waiting", "message": "等待客户端首次上传，尚未验证同步密码"})
				return
			}
			d, err := rt.service.Read(r.Context())
			if err != nil {
				respond(w, 200, map[string]string{"status": "unreadable", "message": "文件尚不可读取：请检查同步密码、文件完整性及支持版本"})
				return
			}
			respond(w, 200, map[string]any{"status": "readable", "message": "同步文件已解密并可读取", "fileVersion": d.Version, "revision": d.File.Revision})
			return
		}
		if len(parts) == 2 && parts[1] == "secrets" {
			v, err := m.Connection(r.Context(), parts[0])
			if err != nil {
				adminError(w, err)
				return
			}
			respond(w, 200, v)
			return
		}
		if len(parts) != 1 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}
		var in Change
		if !readJSON(w, r, &in) {
			return
		}
		if err := m.Update(r.Context(), parts[0], in); err != nil {
			adminError(w, err)
			return
		}
		respond(w, 200, map[string]bool{"ok": true})
	default:
		http.NotFound(w, r)
	}
}
func (m *Manager) loginHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		csrf := config.Secret()
		adminCookie(w, "spmcp_admin_login", csrf, "/admin/login", 600)
		webui.Render(w, "login", map[string]string{"CSRF": csrf})
		return
	}
	if r.Method != "POST" {
		w.WriteHeader(405)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4<<10)
	if r.ParseForm() != nil {
		respond(w, 400, map[string]string{"error": "invalid form"})
		return
	}
	for _, v := range r.PostForm {
		if len(v) != 1 {
			respond(w, 400, map[string]string{"error": "invalid form"})
			return
		}
	}
	c, err := r.Cookie("spmcp_admin_login")
	if err != nil || !secureEqual(c.Value, r.PostForm.Get("csrf")) {
		respond(w, 403, map[string]string{"error": "登录会话失效，请刷新页面"})
		return
	}
	if !m.loginRate.Allow() {
		respond(w, 429, map[string]string{"error": "请稍后再试"})
		return
	}
	m.mu.RLock()
	hash := m.state.AdminHash
	name := m.state.adminName()
	generation := m.state.generation()
	m.mu.RUnlock()
	if !secureEqual(r.PostForm.Get("username"), name) || !checkPassword(hash, r.PostForm.Get("password")) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(401)
		webui.Render(w, "login", map[string]string{"CSRF": c.Value, "Username": r.PostForm.Get("username"), "Error": "用户名或密码不正确"})
		return
	}
	m.mu.RLock()
	unchanged := m.state.generation() == generation
	m.mu.RUnlock()
	if !unchanged {
		respond(w, 401, map[string]string{"error": "管理员密码已更新，请重新登录"})
		return
	}
	token := config.Secret()
	now := m.Now()
	m.sessionMu.Lock()
	for key, s := range m.sessions {
		if now.Sub(s.Seen) >= 30*time.Minute || now.Sub(s.Created) >= 8*time.Hour {
			delete(m.sessions, key)
		}
	}
	if len(m.sessions) >= 128 {
		m.sessionMu.Unlock()
		respond(w, 429, map[string]string{"error": "管理会话过多，请稍后再试"})
		return
	}
	m.sessions[config.Hash(token)] = &session{CSRF: config.Secret(), Generation: generation, Created: now, Seen: now}
	m.sessionMu.Unlock()
	adminCookie(w, sessionCookie, token, "/admin", 8*3600)
	adminCookie(w, "spmcp_admin_login", "", "/admin/login", -1)
	http.Redirect(w, r, "/admin", 303)
}
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || t != "application/json" {
		respond(w, 415, map[string]string{"error": "JSON required"})
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		respond(w, 400, map[string]string{"error": "invalid JSON"})
		return false
	}
	return true
}
func adminError(w http.ResponseWriter, err error) {
	respond(w, 400, map[string]string{"error": err.Error()})
}
func physicalSize(file string) int64 {
	f, e := os.Stat(file)
	if e == nil {
		return f.Size()
	}
	return 0
}
func (m *Manager) Users(ctx context.Context) (map[string]any, error) {
	users := []map[string]any{}
	var current, history, physical int64
	for _, rt := range m.snapshot() {
		rt.mu.RLock()
		a := rt.account
		u, err := rt.store.Usage(ctx)
		if err != nil {
			rt.mu.RUnlock()
			return nil, err
		}
		db := physicalSize(a.Config.Database)
		wal := physicalSize(a.Config.Database + "-wal")
		shm := physicalSize(a.Config.Database + "-shm")
		rt.mu.RUnlock()
		data := "empty"
		if u.CurrentFiles > 0 {
			data = "files_present"
		}
		users = append(users, map[string]any{"id": a.ID, "name": a.Name, "username": a.Config.WebDAVUsername, "enabled": a.Enabled, "dataStatus": data, "currentBytes": u.CurrentBytes, "currentFiles": u.CurrentFiles, "historyBytes": u.HistoryBytes, "historyVersions": u.HistoryVersions, "databaseBytes": db, "walBytes": wal, "shmBytes": shm, "physicalBytes": db + wal + shm})
		current += u.CurrentBytes
		history += u.HistoryBytes
		physical += db + wal + shm
	}
	return map[string]any{"users": users, "totals": map[string]int64{"currentBytes": current, "historyBytes": history, "physicalBytes": physical}, "managementBytes": physicalSize(m.config.Admin.StateFile) + physicalSize(m.config.Admin.KeyFile)}, nil
}
func (m *Manager) Connection(ctx context.Context, id string) (map[string]any, error) {
	m.mu.RLock()
	rt := m.runtimes[id]
	m.mu.RUnlock()
	if rt == nil {
		return nil, errors.New("用户不存在")
	}
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	a := rt.account
	pw, err := config.ReadPasswordFile(a.Config.SyncPasswordFile)
	if err != nil {
		return nil, errors.New("无法读取该账号同步密码")
	}
	login := a.Secrets.OAuth
	if a.Config.OAuth.UseSyncPassword {
		login = pw
	}
	origin := m.config.OAuth.Issuer
	return map[string]any{"username": a.Config.WebDAVUsername, "displayName": a.Name, "mcpURL": origin + "/mcp", "issuer": origin, "authorizationURL": origin + "/oauth/authorize", "tokenURL": origin + "/oauth/token", "scope": "mcp", "tokenAuthMethod": "client_secret_basic", "redirectURI": a.Config.OAuth.RedirectURIs[0], "clientId": a.Config.OAuth.ClientID, "clientSecret": a.Secrets.ClientSecret, "oauthUsername": a.Config.OAuth.Username, "oauthPassword": login, "webdavURL": origin, "webdavFolder": "super-productivity", "webdavUsername": a.Config.WebDAVUsername, "webdavPassword": a.Secrets.WebDAV, "syncPassword": pw}, nil
}
