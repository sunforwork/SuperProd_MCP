package config

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Listen             string   `json:"listen"`
	Database           string   `json:"database"`
	Timezone           string   `json:"timezone"`
	WebDAVUsername     string   `json:"webdavUsername"`
	WebDAVPasswordHash string   `json:"webdavPasswordHash"`
	MCPKeyHashes       []string `json:"mcpKeyHashes"`
	AllowedHosts       []string `json:"allowedHosts"`
	AllowedOrigins     []string `json:"allowedOrigins"`
	ExperimentalWrites bool     `json:"experimentalWrites"`
	SyncPasswordFile   string   `json:"syncPasswordFile,omitempty"`
	OAuth              *OAuth   `json:"oauth,omitempty"`
	Admin              *Admin   `json:"admin,omitempty"`
}

type Admin struct {
	StateFile string `json:"stateFile"`
	KeyFile   string `json:"keyFile"`
}

func Secret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func Load(file string) (Config, error) {
	var c Config
	b, e := os.ReadFile(file)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, errors.New("invalid configuration")
	}
	if c.Listen == "" || c.Database == "" || c.WebDAVUsername == "" || len(c.AllowedHosts) == 0 {
		return c, errors.New("incomplete configuration")
	}
	for _, h := range append(c.MCPKeyHashes, c.WebDAVPasswordHash) {
		b, e := hex.DecodeString(h)
		if e != nil || len(b) != 32 {
			return c, errors.New("invalid credential hash")
		}
	}
	if !filepath.IsAbs(c.Database) {
		c.Database = filepath.Join(filepath.Dir(file), c.Database)
	}
	if c.SyncPasswordFile != "" && !filepath.IsAbs(c.SyncPasswordFile) {
		c.SyncPasswordFile = filepath.Join(filepath.Dir(file), c.SyncPasswordFile)
	}
	if c.OAuth != nil {
		if e := c.OAuth.Validate(); e != nil {
			return c, e
		}
	}
	if c.Admin != nil {
		if c.Admin.StateFile == "" || c.Admin.KeyFile == "" {
			return c, errors.New("incomplete admin configuration")
		}
		if !filepath.IsAbs(c.Admin.StateFile) {
			c.Admin.StateFile = filepath.Join(filepath.Dir(file), c.Admin.StateFile)
		}
		if !filepath.IsAbs(c.Admin.KeyFile) {
			c.Admin.KeyFile = filepath.Join(filepath.Dir(file), c.Admin.KeyFile)
		}
	}
	return c, nil
}

func NetworkDefaults(listen string) ([]string, string, error) {
	host, port, e := net.SplitHostPort(listen)
	if e != nil {
		return nil, "", errors.New("listen must be an IP or localhost followed by a port")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return nil, "", errors.New("listen port must be 1..65535")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && ip == nil {
		return nil, "", errors.New("listen host must be an IP or localhost")
	}
	hosts := []string{net.JoinHostPort("127.0.0.1", port), net.JoinHostPort("localhost", port), net.JoinHostPort("::1", port)}
	endpointHost := host
	if ip != nil && ip.IsUnspecified() {
		endpointHost = "127.0.0.1"
		if ip.To4() == nil {
			endpointHost = "::1"
		}
	}
	if host != "localhost" && ip != nil && !ip.IsUnspecified() {
		candidate := net.JoinHostPort(host, port)
		found := false
		for _, value := range hosts {
			if value == candidate {
				found = true
			}
		}
		if !found {
			hosts = append(hosts, candidate)
		}
	}
	return hosts, "http://" + net.JoinHostPort(endpointHost, port) + "/mcp", nil
}
func Save(file string, c Config, exclusive bool) error {
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return e
	}
	if exclusive {
		f, e := os.OpenFile(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		defer f.Close()
		_, e = f.Write(append(b, '\n'))
		return e
	}
	tmp := file + ".new"
	if e = os.WriteFile(tmp, append(b, '\n'), 0600); e != nil {
		return e
	}
	return os.Rename(tmp, file)
}
func SyncPassword(c Config) (string, error) {
	file := os.Getenv("SP_SYNC_PASSWORD_FILE")
	if file == "" {
		if password := os.Getenv("SP_SYNC_PASSWORD"); password != "" {
			return password, nil
		}
		file = c.SyncPasswordFile
	}
	if file != "" {
		return ReadPasswordFile(file)
	}
	return "", errors.New("set syncPasswordFile in configuration, SP_SYNC_PASSWORD_FILE or SP_SYNC_PASSWORD")
}
func ReadPasswordFile(file string) (string, error) {
	if file == "" {
		return "", errors.New("syncPasswordFile must be configured for OAuth login")
	}
	b, e := os.ReadFile(file)
	if e != nil {
		return "", e
	}
	password := strings.TrimRight(string(b), "\r\n")
	if password == "" {
		return "", errors.New("sync password file is empty")
	}
	return password, nil
}
