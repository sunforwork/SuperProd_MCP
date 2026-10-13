package setup

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sunforwork/SuperProd_MCP/internal/admin"
	"github.com/sunforwork/SuperProd_MCP/internal/config"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
	"github.com/sunforwork/SuperProd_MCP/internal/webui"
	"golang.org/x/time/rate"
)

const stageName = ".spmcp-install-stage"
const instanceName = "instance"
const cookieName = "spmcp_setup"
const ownerMagic = "spmcp-install-v1"

type Input struct {
	Issuer         string `json:"issuer"`
	AdminUsername  string `json:"adminUsername"`
	AdminPassword  string `json:"adminPassword"`
	Username       string `json:"username"`
	DisplayName    string `json:"displayName"`
	WebDAVPassword string `json:"webdavPassword"`
	OAuthPassword  string `json:"oauthPassword"`
	SyncPassword   string `json:"syncPassword"`
	ClientID       string `json:"clientId"`
	ClientSecret   string `json:"clientSecret"`
	Writes         *bool  `json:"writes"`
	DataMode       string `json:"dataMode"`
}
type owner struct {
	Magic  string `json:"magic"`
	Config string `json:"config"`
}
type session struct {
	Hash, CSRF, Origin string
	Seen               time.Time
}
type Server struct {
	mu                     sync.Mutex
	file, listen           string
	hosts                  []string
	codeHash               string
	started                time.Time
	session                *session
	manager                *admin.Manager
	Now                    func() time.Time
	limiter                *rate.Limiter
	clientID, clientSecret string
	publishFile            func(string, string) error
	log                    io.Writer
}

func Open(file, listen string, log io.Writer) (*Server, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, err
	}
	if _, err = os.Lstat(abs); err == nil {
		if _, e := config.Load(abs); e == nil {
			finalize(abs)
		}
		return nil, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	dir := filepath.Dir(abs)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err = recoverInstallation(abs); err != nil {
		return nil, err
	}
	if _, err = os.Stat(abs); err == nil {
		finalize(abs)
		return nil, nil
	}
	if err = empty(dir); err != nil {
		return nil, err
	}
	hosts, _, err := config.NetworkDefaults(listen)
	if err != nil {
		return nil, err
	}
	code := config.Secret()
	s := &Server{file: abs, listen: listen, hosts: hosts, codeHash: config.Hash(code), started: time.Now(), Now: time.Now, limiter: rate.NewLimiter(rate.Every(3*time.Second), 5), clientID: "spmcp-" + config.Secret(), clientSecret: config.Secret(), publishFile: publish, log: log}
	fmt.Fprintf(log, "首次安装：访问 HTTPS 网站的 /setup。安装码（30 分钟有效，仅兑换一次）：%s\n", code)
	return s, nil
}
func (s *Server) Listen() string { return s.listen }
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.manager != nil {
		s.manager.Close()
	}
}
func empty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("配置缺失但数据目录非空；请恢复原配置，或为新安装指定独立空目录")
	}
	return nil
}
func owned(dir, file string) bool {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return false
	}
	marker := filepath.Join(dir, "install-owner.json")
	st, err = os.Lstat(marker)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	b, err := os.ReadFile(marker)
	var o owner
	return err == nil && json.Unmarshal(b, &o) == nil && o.Magic == ownerMagic && o.Config == file
}
func finalize(file string) {
	instance := filepath.Join(filepath.Dir(file), instanceName)
	if owned(instance, file) {
		_ = os.Remove(filepath.Join(instance, "install-owner.json"))
		_ = os.Remove(filepath.Join(instance, "config.ready.json"))
		syncDir(instance)
	}
}
func writePrivate(file string, body []byte) error {
	f, err := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(body)
	if err == nil {
		err = f.Sync()
	}
	closed := f.Close()
	if err == nil {
		err = closed
	}
	return err
}
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
}
func publish(ready, file string) error {

	if err := os.Link(ready, file); err != nil {
		return err
	}
	syncDir(filepath.Dir(file))
	return nil
}
func recoverInstallation(file string) error {
	root := filepath.Dir(file)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".setup-check-") {
			dir := filepath.Join(root, entry.Name())
			if !owned(dir, file) {
				return errors.New("存在无法确认归属的检查暂存目录，拒绝清理")
			}
			if err = os.RemoveAll(dir); err != nil {
				return err
			}
		}
	}
	stage := filepath.Join(root, stageName)
	instance := filepath.Join(root, instanceName)
	if _, err := os.Lstat(stage); err == nil {
		if !owned(stage, file) {
			return errors.New("存在无法确认归属的安装暂存目录，拒绝清理")
		}
		if err = os.RemoveAll(stage); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(instance); err == nil {
		if !owned(instance, file) {
			return errors.New("数据目录已有 instance，拒绝重新安装")
		}
		ready := filepath.Join(instance, "config.ready.json")
		c, err := config.Load(ready)
		if err != nil {
			return errors.New("安装配置尚不完整；保留文件，请检查目录权限")
		}
		if c.Database != filepath.Join(instance, "sync.db") || c.SyncPasswordFile != filepath.Join(instance, "sync-password.txt") || c.Admin == nil || c.Admin.KeyFile != filepath.Join(instance, "admin", "master.key") || c.Admin.StateFile != filepath.Join(instance, "admin", "registry.enc") {
			return errors.New("暂存配置路径不符合本次安装，拒绝恢复")
		}
		m, err := admin.Open(c)
		if err != nil {
			return err
		}
		m.Close()
		if err = publish(ready, file); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func jsonResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, status int, message string) {
	jsonResponse(w, status, map[string]string{"error": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	content, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || content != "application/json" {
		failure(w, 415, "请使用 JSON 请求")
		return false
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		failure(w, 400, "请求格式不正确或过大")
		return false
	}
	return true
}
func equal(a, b string) bool { return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1 }
func httpsOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && !strings.ContainsAny(origin, "\r\n\"\\")
}
func secret(v string) bool { return v != "" && len(v) <= 256 && !strings.ContainsAny(v, "\r\n\x00") }
func password24() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	if s.manager != nil {
		m := s.manager
		s.mu.Unlock()
		m.ServeHTTP(w, r)
		return
	}
	defer s.mu.Unlock()
	allowed := false
	for _, h := range s.hosts {
		if r.Host == h {
			allowed = true
		}
	}
	if !allowed {
		failure(w, 403, "host forbidden")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self'; script-src 'self'; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	if r.URL.Path == "/healthz" && r.Method == "GET" {
		jsonResponse(w, 200, map[string]any{"status": "ok", "installation": "required", "version": "2.3.0", "releaseReady": false})
		return
	}
	if (r.URL.Path == "/setup" || r.URL.Path == "/setup/") && r.Method == "GET" {
		webui.Render(w, "setup", nil)
		return
	}
	if r.URL.Path == "/" && r.Method == "GET" {
		http.Redirect(w, r, "/setup", 303)
		return
	}
	if r.URL.Path == "/assets/ui/style.css" || r.URL.Path == "/assets/ui/setup.js" {
		webui.ServeHTTP(w, r)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/setup/api/") {
		failure(w, 503, "服务等待首次安装，请访问 /setup")
		return
	}
	if r.URL.RawQuery != "" {
		failure(w, 400, "安装接口不接受 URL 参数")
		return
	}
	if r.URL.Path == "/setup/api/unlock" && r.Method == "POST" {
		origin := r.Header.Get("Origin")
		if !httpsOrigin(origin) {
			failure(w, 403, "请通过 HTTPS 网站进行安装")
			return
		}
		if !s.limiter.Allow() {
			failure(w, 429, "尝试过于频繁，请稍后再试")
			return
		}
		var in struct {
			Code string `json:"code"`
		}
		if !decode(w, r, &in) {
			return
		}
		if s.codeHash == "" || s.Now().Sub(s.started) >= 30*time.Minute || !equal(config.Hash(in.Code), s.codeHash) {
			failure(w, 401, "安装码不正确、已使用或已过期；重启未安装项目可获取新码")
			return
		}
		token := config.Secret()
		s.codeHash = ""
		s.session = &session{Hash: config.Hash(token), CSRF: config.Secret(), Origin: origin, Seen: s.Now()}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/setup", MaxAge: 1800, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonResponse(w, 200, map[string]string{"csrf": s.session.CSRF})
		return
	}
	c, err := r.Cookie(cookieName)
	if err != nil || s.session == nil || !equal(config.Hash(c.Value), s.session.Hash) || s.Now().Sub(s.session.Seen) >= 30*time.Minute {
		failure(w, 401, "安装会话失效；重启未安装项目后重新解锁")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.session.Origin {
		failure(w, 403, "origin forbidden")
		return
	}
	if r.Method != "GET" && (r.Header.Get("Origin") != s.session.Origin || !equal(r.Header.Get("X-CSRF-Token"), s.session.CSRF)) {
		failure(w, 403, "安装会话校验失败")
		return
	}
	s.session.Seen = s.Now()

	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: c.Value, Path: "/setup", MaxAge: 1800, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	switch {
	case r.URL.Path == "/setup/api/session" && r.Method == "GET":
		jsonResponse(w, 200, map[string]string{"csrf": s.session.CSRF})
		return
	case r.URL.Path == "/setup/api/check" && r.Method == "POST":
		if err = empty(filepath.Dir(s.file)); err == nil {
			err = probe(s.file)
		}
		if err != nil {
			failure(w, 400, "数据目录写入或 SQLite 初始化检查失败；请检查权限及是否已有资料")
			return
		}
		jsonResponse(w, 200, map[string]any{"ok": true, "version": "2.3.0", "listen": s.listen, "dataDirectory": filepath.Dir(s.file), "issuer": s.session.Origin, "clientId": s.clientID, "clientSecret": s.clientSecret})
		return
	case r.URL.Path == "/setup/api/generate" && r.Method == "POST":
		var in struct {
			Kind string `json:"kind"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Kind != "password" && in.Kind != "clientSecret" {
			failure(w, 400, "未知凭据类型")
			return
		}
		value := config.Secret()
		if in.Kind == "password" {
			value, err = password24()
		}
		if err != nil {
			failure(w, 500, "随机凭据生成失败")
			return
		}
		jsonResponse(w, 200, map[string]string{"value": value})
		return
	case (r.URL.Path == "/setup/api/validate" || r.URL.Path == "/setup/api/commit") && r.Method == "POST":
		var in Input
		if !decode(w, r, &in) {
			return
		}
		if err = s.validate(in); err != nil {
			failure(w, 400, err.Error())
			return
		}
		if r.URL.Path == "/setup/api/validate" {
			jsonResponse(w, 200, map[string]bool{"ok": true})
			return
		}
		m, err := s.install(in)
		if err != nil {
			fmt.Fprintf(s.log, "首次安装提交失败：%v\n", err)
			failure(w, 500, "安装未完成，原资料不会被覆盖；请检查目录权限、磁盘空间及项目日志")
			return
		}
		s.manager = m
		s.session = nil
		s.clientSecret = ""
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/setup", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		jsonResponse(w, 201, map[string]any{"ok": true, "connection": map[string]any{"adminURL": in.Issuer + "/admin", "adminUsername": in.AdminUsername, "mcpURL": in.Issuer + "/mcp", "issuer": in.Issuer, "authorizationURL": in.Issuer + "/oauth/authorize", "tokenURL": in.Issuer + "/oauth/token", "clientId": in.ClientID, "clientSecret": in.ClientSecret, "scope": "mcp", "tokenAuthMethod": "client_secret_basic", "redirectURI": config.ChatGPTRedirect, "oauthUsername": in.Username, "oauthPassword": in.OAuthPassword, "webdavURL": in.Issuer, "webdavFolder": "super-productivity", "webdavUsername": in.Username, "webdavPassword": in.WebDAVPassword, "syncPassword": in.SyncPassword, "writes": *in.Writes}, "dataStatus": "waiting"})
		return
	default:
		http.NotFound(w, r)
	}
}
func probe(file string) error {
	dir := filepath.Dir(file)
	temp, err := os.MkdirTemp(dir, ".setup-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temp)
	marker, _ := json.Marshal(owner{ownerMagic, file})
	if err = writePrivate(filepath.Join(temp, "install-owner.json"), marker); err != nil {
		return err
	}
	if err = writePrivate(filepath.Join(temp, "publish-test"), []byte("probe")); err != nil {
		return err
	}
	if err = os.Link(filepath.Join(temp, "publish-test"), filepath.Join(temp, "published-test")); err != nil {
		return err
	}
	store, err := storage.Open(filepath.Join(temp, "probe.db"))
	if err != nil {
		return err
	}
	store.Close()
	return nil
}
func (s *Server) validate(in Input) error {
	if !httpsOrigin(in.Issuer) || in.Issuer != s.session.Origin {
		return errors.New("公网地址必须与当前安装网站的 HTTPS 基地址一致，不包含路径")
	}
	if !admin.ValidUsername(in.AdminUsername) || !admin.ValidUsername(in.Username) || in.AdminUsername == in.Username {
		return errors.New("管理员和业务用户名需有效且不同，最多 64 字节，不能含冒号或控制字符")
	}
	if len(in.AdminPassword) < 12 || !secret(in.AdminPassword) {
		return errors.New("管理员密码需要 12–256 字节")
	}
	if !secret(in.WebDAVPassword) || !secret(in.OAuthPassword) || !secret(in.SyncPassword) || !secret(in.ClientSecret) {
		return errors.New("各项密码必须填写，最多 256 字节，不能包含换行或空字符")
	}
	if in.ClientID != s.clientID {
		return errors.New("Client ID 必须使用本次安装自动生成的值")
	}
	if len(in.DisplayName) > 128 {
		return errors.New("显示名称过长")
	}
	if in.Writes == nil {
		return errors.New("请确认 MCP 写入选项")
	}
	if in.DataMode != "new" && in.DataMode != "existing" {
		return errors.New("请选择全新使用或已有客户端数据")
	}
	return nil
}
func (s *Server) install(in Input) (*admin.Manager, error) {
	root := filepath.Dir(s.file)
	if err := empty(root); err != nil {
		return nil, err
	}
	stage := filepath.Join(root, stageName)
	instance := filepath.Join(root, instanceName)
	if err := os.Mkdir(stage, 0700); err != nil {
		return nil, err
	}
	marker, _ := json.Marshal(owner{ownerMagic, s.file})
	if err := writePrivate(filepath.Join(stage, "install-owner.json"), marker); err != nil {
		_ = os.Remove(filepath.Join(stage, "install-owner.json"))
		_ = os.Remove(stage)
		return nil, err
	}
	published := false
	defer func() {
		if !published {
			if owned(stage, s.file) {
				_ = os.RemoveAll(stage)
			}
			if owned(instance, s.file) {
				_ = os.RemoveAll(instance)
			}
		}
	}()
	cfg := config.Config{Listen: s.listen, Database: filepath.Join(instance, "sync.db"), SyncPasswordFile: filepath.Join(instance, "sync-password.txt"), Timezone: "Asia/Shanghai", WebDAVUsername: in.Username, WebDAVPasswordHash: config.Hash(in.WebDAVPassword), AllowedHosts: s.hosts, AllowedOrigins: []string{"https://app.super-productivity.com", "https://chatgpt.com", in.Issuer}, ExperimentalWrites: *in.Writes, OAuth: &config.OAuth{Issuer: in.Issuer, Username: in.Username, PasswordHash: config.Hash(in.OAuthPassword), ClientID: in.ClientID, ClientSecretHash: config.Hash(in.ClientSecret), RedirectURIs: []string{config.ChatGPTRedirect}}, Admin: &config.Admin{StateFile: filepath.Join(instance, "admin", "registry.enc"), KeyFile: filepath.Join(instance, "admin", "master.key")}}
	if err := cfg.OAuth.Validate(); err != nil {
		return nil, err
	}
	if err := writePrivate(filepath.Join(stage, "sync-password.txt"), []byte(in.SyncPassword+"\n")); err != nil {
		return nil, err
	}
	store, err := storage.Open(filepath.Join(stage, "sync.db"))
	if err != nil {
		return nil, err
	}
	store.Close()
	if err = os.Chmod(filepath.Join(stage, "sync.db"), 0600); err != nil {
		return nil, err
	}
	if err = admin.Provision(filepath.Join(stage, "admin", "master.key"), filepath.Join(stage, "admin", "registry.enc"), cfg, in.AdminUsername, in.AdminPassword, in.DisplayName, admin.Secrets{WebDAV: in.WebDAVPassword, OAuth: in.OAuthPassword, ClientSecret: in.ClientSecret}); err != nil {
		return nil, err
	}
	ready := filepath.Join(stage, "config.ready.json")
	if err = config.Save(ready, cfg, true); err != nil {
		return nil, err
	}

	if f, e := os.OpenFile(ready, os.O_RDWR, 0); e == nil {
		err = f.Sync()
		_ = f.Close()
	} else {
		err = e
	}
	if err != nil {
		return nil, err
	}
	syncDir(stage)
	if err = os.Rename(stage, instance); err != nil {
		return nil, err
	}
	syncDir(root)
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil, e
	}
	if len(entries) != 1 || entries[0].Name() != instanceName {
		return nil, errors.New("安装期间出现其他数据，拒绝发布配置")
	}
	m, err := admin.Open(cfg)
	if err != nil {
		return nil, err
	}
	if err = s.publishFile(filepath.Join(instance, "config.ready.json"), s.file); err != nil {
		m.Close()
		return nil, err
	}
	published = true
	finalize(s.file)
	return m, nil
}
