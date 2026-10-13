package admin

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/sunforwork/SuperProd_MCP/internal/config"
	"golang.org/x/crypto/argon2"
)

const Username = "SUNAdmin"
const stateAAD = "spmcp-admin-registry-v1"

type Secrets struct {
	WebDAV       string `json:"webdav,omitempty"`
	OAuth        string `json:"oauth,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}
type Account struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Enabled bool          `json:"enabled"`
	Config  config.Config `json:"config"`
	Secrets Secrets       `json:"secrets"`
}
type State struct {
	Version       int       `json:"version"`
	AdminUsername string    `json:"adminUsername,omitempty"`
	AdminHash     string    `json:"adminHash"`
	Accounts      []Account `json:"accounts"`
}

func passwordHash(password string) (string, error) {
	if len(password) < 12 || len(password) > 256 {
		return "", errors.New("管理员密码需要 12–256 个字节")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, 3, 65536, 1, 32)
	return base64.RawStdEncoding.EncodeToString(salt) + ":" + base64.RawStdEncoding.EncodeToString(key), nil
}
func checkPassword(hash, password string) bool {
	parts := strings.Split(hash, ":")
	if len(parts) != 2 || len(password) > 256 {
		return false
	}
	salt, e1 := base64.RawStdEncoding.DecodeString(parts[0])
	key, e2 := base64.RawStdEncoding.DecodeString(parts[1])
	if e1 != nil || e2 != nil || len(salt) != 16 || len(key) != 32 {
		return false
	}
	return subtle.ConstantTimeCompare(key, argon2.IDKey([]byte(password), salt, 3, 65536, 1, 32)) == 1
}
func cloneState(s State) State {
	b, _ := json.Marshal(s)
	var out State
	_ = json.Unmarshal(b, &out)
	return out
}
func loadState(c config.Config) (State, []byte, error) {
	var s State
	if c.Admin == nil {
		return s, nil, errors.New("admin not enabled")
	}
	key, err := os.ReadFile(c.Admin.KeyFile)
	if err != nil || len(key) != 32 {
		return s, nil, errors.New("admin master key missing or invalid")
	}
	b, err := os.ReadFile(c.Admin.StateFile)
	if err != nil {
		return s, nil, err
	}
	block, _ := aes.NewCipher(key)
	a, _ := cipher.NewGCM(block)
	if len(b) < a.NonceSize()+a.Overhead() {
		return s, nil, errors.New("invalid encrypted admin registry")
	}
	plain, err := a.Open(nil, b[:a.NonceSize()], b[a.NonceSize():], []byte(stateAAD))
	if err != nil || json.Unmarshal(plain, &s) != nil || (s.Version != 1 && s.Version != 2) || len(s.Accounts) == 0 {
		return s, nil, errors.New("admin registry authentication or version failed")
	}
	if s.Version == 1 && s.AdminUsername != "" && s.AdminUsername != Username {
		return s, nil, errors.New("invalid legacy administrator name")
	}
	if s.Version == 2 && !ValidUsername(s.AdminUsername) {
		return s, nil, errors.New("invalid administrator name")
	}
	return s, key, nil
}
func saveState(file string, key []byte, s State) error {
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	a, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	return atomicWrite(file, a.Seal(nonce, nonce, b, []byte(stateAAD)))
}
func atomicWrite(file string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(file), ".spmcp-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(name, file); err != nil {
		return err
	}

	if d, err := os.Open(filepath.Dir(file)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func Enable(configFile string, c config.Config, password string) error {
	if c.Admin != nil {
		return errors.New("admin already enabled")
	}
	if c.OAuth == nil {
		return errors.New("enable OAuth first")
	}
	var pathErr error
	configFile, pathErr = filepath.Abs(configFile)
	if pathErr != nil {
		return pathErr
	}
	c.Database, pathErr = filepath.Abs(c.Database)
	if pathErr != nil {
		return pathErr
	}
	c.SyncPasswordFile, pathErr = filepath.Abs(c.SyncPasswordFile)
	if pathErr != nil {
		return pathErr
	}
	if _, err := config.ReadPasswordFile(c.SyncPasswordFile); err != nil {
		return err
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	dir := filepath.Join(filepath.Dir(configFile), "admin")
	c.Admin = &config.Admin{StateFile: filepath.Join(dir, "registry.enc"), KeyFile: filepath.Join(dir, "master.key")}
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if _, err = os.Stat(c.Admin.StateFile); !os.IsNotExist(err) {
		return errors.New("admin registry already exists or is inaccessible")
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	f, err := os.OpenFile(c.Admin.KeyFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(key)
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err == nil {
		err = ce
	}
	if err != nil {
		return err
	}
	business := c
	business.Admin = nil
	s := State{Version: 1, AdminHash: hash, Accounts: []Account{{ID: "primary", Name: c.WebDAVUsername, Enabled: true, Config: business}}}
	if err = saveState(c.Admin.StateFile, key, s); err != nil {
		return err
	}
	if err = config.Save(configFile, c, false); err != nil {
		return fmt.Errorf("admin initialized but config publication failed: %w", err)
	}
	return nil
}

func EffectiveConfig(c config.Config, user string) (config.Config, error) {
	s, _, err := loadState(c)
	if err != nil {
		return c, err
	}
	if user == "" {
		user = "primary"
	}
	for _, a := range s.Accounts {
		if a.ID == user || a.Config.WebDAVUsername == user {
			return a.Config, nil
		}
	}
	return c, errors.New("account not found")
}

func ValidUsername(name string) bool {
	if name == "" || len(name) > 64 || !utf8.ValidString(name) || strings.TrimSpace(name) != name || strings.Contains(name, ":") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}
func (s State) adminName() string {
	if s.Version == 1 {
		return Username
	}
	return s.AdminUsername
}
func (s State) generation() string { return config.Hash(s.adminName() + "\x00" + s.AdminHash) }

func Provision(keyFile, stateFile string, c config.Config, administrator, password, displayName string, secrets Secrets) error {
	if !ValidUsername(administrator) || !ValidUsername(c.WebDAVUsername) || administrator == c.WebDAVUsername {
		return errors.New("管理员和业务用户名需有效且不同")
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	if c.OAuth == nil || c.OAuth.UseSyncPassword {
		return errors.New("新安装需要独立 OAuth 密码")
	}
	if displayName == "" {
		displayName = c.WebDAVUsername
	}
	if len(displayName) > 128 {
		return errors.New("显示名称过长")
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return err
	}
	if _, err = os.Stat(keyFile); !os.IsNotExist(err) {
		return errors.New("管理主密钥已存在或不可访问")
	}
	if _, err = os.Stat(stateFile); !os.IsNotExist(err) {
		return errors.New("管理注册表已存在或不可访问")
	}
	if err = atomicWrite(keyFile, key); err != nil {
		return err
	}
	business := c
	business.Admin = nil
	return saveState(stateFile, key, State{Version: 2, AdminUsername: administrator, AdminHash: hash, Accounts: []Account{{ID: "primary", Name: displayName, Enabled: true, Config: business, Secrets: secrets}}})
}
