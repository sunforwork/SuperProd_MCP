package main

import (
	"archive/zip"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	assets "github.com/sunforwork/SuperProd_MCP"
	"github.com/sunforwork/SuperProd_MCP/internal/admin"
	"github.com/sunforwork/SuperProd_MCP/internal/cloudplugin"
	"github.com/sunforwork/SuperProd_MCP/internal/config"
	"github.com/sunforwork/SuperProd_MCP/internal/mcpserver"
	"github.com/sunforwork/SuperProd_MCP/internal/oauth"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
	"github.com/sunforwork/SuperProd_MCP/internal/setup"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
	"github.com/sunforwork/SuperProd_MCP/internal/webdav"
	"github.com/sunforwork/SuperProd_MCP/internal/webui"
	"io"
	iofs "io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "Error:", e)
		os.Exit(1)
	}
}
func emit(v any) { b, _ := json.MarshalIndent(v, "", "  "); fmt.Println(string(b)) }
func run() error {
	if len(os.Args) < 2 {
		return errors.New("usage: spmcp init|enable-oauth|enable-admin|rotate-oauth-client|serve|import|inspect|export|rotate-key|probe|probe-oauth|plugin-source|plugin-package|licenses [flags]")
	}
	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	cfgPath := fs.String("config", "data/config.json", "configuration path")
	input := fs.String("input", "", "encrypted sync file or uploaded ZIP")
	output := fs.String("output", "", "output file (never overwrite)")
	rev := fs.Int64("revision", 0, "historical revision to export")
	host := fs.String("mcp-host", "", "optional legacy Host allowlist entry; Baota deployment does not need this")
	listen := fs.String("listen", "127.0.0.1:8787", "listen address for init or first installation; existing config takes precedence")
	user := fs.String("webdav-user", "SUN", "WebDAV username")
	mcpURL := fs.String("mcp-url", "", "deployed HTTPS MCP URL; never stored in the binary")
	keyFile := fs.String("api-key-file", "", "private API key file or init credentials JSON")
	accessFile := fs.String("access-token-file", "", "optional private OAuth access token for authenticated probe")
	issuer := fs.String("issuer", "", "canonical HTTPS origin for OAuth discovery; TLS stays in Baota")
	redirectURI := fs.String("redirect-uri", config.ChatGPTRedirect, "exact redirect URI from ChatGPT connection management")
	adminPasswordFile := fs.String("admin-password-file", "", "private file containing initial admin password; enable-admin only")
	accountUser := fs.String("user", "", "managed account username or ID for import/inspect/export; default primary")
	if e := fs.Parse(os.Args[2:]); e != nil {
		return e
	}
	switch cmd {
	case "licenses":
		var writer io.Writer = os.Stdout
		if *output != "" {
			f, err := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			defer f.Close()
			writer = f
		}
		for _, name := range []string{"LICENSE", "NOTICE", "THIRD_PARTY_NOTICES.md"} {
			b, err := assets.Files.ReadFile(name)
			if err != nil {
				return err
			}
			if _, err = fmt.Fprintf(writer, "\n===== %s =====\n%s\n", name, b); err != nil {
				return err
			}
		}
		return iofs.WalkDir(assets.Files, "licenses", func(name string, d iofs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			b, err := assets.Files.ReadFile(name)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(writer, "\n===== %s =====\n%s\n", name, b)
			return err
		})
	case "enable-admin":
		c, e := config.Load(*cfgPath)
		if e != nil {
			return e
		}
		password, e := config.ReadPasswordFile(*adminPasswordFile)
		if e != nil {
			return e
		}
		if e = admin.Enable(*cfgPath, c, password); e != nil {
			return e
		}
		emit(p.Object{"adminUsername": admin.Username, "adminURL": c.OAuth.Issuer + "/admin", "note": "Restart the Baota Go project. Original database and OAuth credentials are retained. Store the initial password privately; it is not included in the binary."})
		return nil
	case "rotate-oauth-client":
		c, e := config.Load(*cfgPath)
		if e != nil {
			return e
		}
		if c.OAuth == nil {
			return errors.New("OAuth not enabled")
		}
		if c.Admin != nil {
			return errors.New("managed accounts: reset OAuth client credentials in /admin")
		}
		secret := config.Secret()
		c.OAuth.ClientSecretHash = config.Hash(secret)
		if e = config.Save(*cfgPath, c, false); e != nil {
			return e
		}
		emit(p.Object{"clientId": c.OAuth.ClientID, "clientSecret": secret, "note": "Restart the Baota Go project to revoke existing OAuth sessions, then update the ChatGPT OAuth Client Secret and reconnect. The login password remains in sync-password.txt."})
		return nil
	case "enable-oauth":
		c, e := config.Load(*cfgPath)
		if e != nil {
			return e
		}
		if c.OAuth != nil {
			return errors.New("OAuth already enabled; keep existing credentials and configuration")
		}
		if c.SyncPasswordFile == "" {
			c.SyncPasswordFile = filepath.Join(filepath.Dir(*cfgPath), "sync-password.txt")
		}
		if _, e = config.ReadPasswordFile(c.SyncPasswordFile); e != nil {
			return e
		}
		secret := config.Secret()
		c.OAuth = &config.OAuth{Issuer: strings.TrimRight(*issuer, "/"), Username: c.WebDAVUsername, UseSyncPassword: true, ClientID: "spmcp-" + config.Secret(), ClientSecretHash: config.Hash(secret), RedirectURIs: []string{*redirectURI}}
		if e = c.OAuth.Validate(); e != nil {
			return e
		}
		if e = config.Save(*cfgPath, c, false); e != nil {
			return e
		}
		emit(p.Object{"issuer": c.OAuth.Issuer, "mcpURL": c.OAuth.Resource(), "clientId": c.OAuth.ClientID, "clientSecret": secret, "oauthUsername": c.OAuth.Username, "oauthLoginPasswordSource": c.SyncPasswordFile, "redirectUri": *redirectURI, "note": "Restart the Baota Go project. Choose OAuth and enter clientId/clientSecret in ChatGPT. Sign in with the sync-password.txt contents. Existing sync files and WebDAV credentials are unchanged; legacy MCP keys are no longer accepted in OAuth mode."})
		return nil
	case "probe", "probe-oauth", "plugin-package", "plugin-source":
		endpoint := *mcpURL
		var report p.Object
		if cmd == "probe-oauth" || cmd == "plugin-package" {
			if *keyFile != "" {
				return errors.New("ChatGPT uses OAuth; --api-key-file is not accepted for OAuth discovery or cloud plugin packaging")
			}
			var err error
			report, err = cloudplugin.Discover(context.Background(), endpoint)
			if err != nil {
				return err
			}
			if *accessFile != "" {
				token, err := cloudplugin.ReadAccessToken(*accessFile)
				if err != nil {
					return err
				}
				verified, err := cloudplugin.Verify(context.Background(), endpoint, token)
				if err != nil {
					return err
				}
				report["authenticatedToolsVerified"] = true
				report["authenticatedProbe"] = verified
			}
		} else if cmd != "plugin-source" {
			key, e := cloudplugin.ReadKey(*keyFile)
			if e != nil {
				return e
			}
			report, e = cloudplugin.Verify(context.Background(), endpoint, key)
			if e != nil {
				return e
			}
		} else {
			endpoint = ""
		}
		if cmd == "probe" || cmd == "probe-oauth" {
			emit(report)
			return nil
		}
		b, e := cloudplugin.Build(endpoint)
		if e != nil {
			return e
		}
		if e = cloudplugin.Save(*output, b); e != nil {
			return e
		}
		emit(p.Object{"archive": *output, "bytes": len(b), "containsCredentials": false, "remoteConnectionIncluded": endpoint != "", "verification": report})
		return nil
	case "init":
		mcpKey, davPassword := config.Secret(), config.Secret()
		hosts, address, e := config.NetworkDefaults(*listen)
		if e != nil {
			return e
		}
		if *host != "" {
			if strings.ContainsAny(*host, "/\\?# ") {
				return errors.New("invalid mcp-host")
			}
			hosts = append(hosts, *host)
		}
		c := config.Config{Listen: *listen, Database: "sync.db", SyncPasswordFile: "sync-password.txt", Timezone: "Asia/Shanghai", WebDAVUsername: *user, WebDAVPasswordHash: config.Hash(davPassword), MCPKeyHashes: []string{config.Hash(mcpKey)}, AllowedHosts: hosts, AllowedOrigins: []string{"https://app.super-productivity.com", "https://chatgpt.com"}}
		if e := config.Save(*cfgPath, c, true); e != nil {
			return e
		}
		if *host != "" {
			address = "https://" + *host + "/mcp"
		}
		emit(p.Object{"config": *cfgPath, "webdavUsername": *user, "webdavPassword": davPassword, "mcpKey": mcpKey, "mcpURL": address, "syncPasswordFile": filepath.Join(filepath.Dir(*cfgPath), c.SyncPasswordFile), "note": "Store credentials privately. Put the existing sync encryption password in syncPasswordFile. Domain and TLS are managed by the reverse proxy. Experimental writes are disabled."})
		return nil
	case "rotate-key":
		c, e := config.Load(*cfgPath)
		if e != nil {
			return e
		}
		if c.Admin != nil {
			return errors.New("managed accounts use OAuth; API key rotation is unavailable")
		}
		key := config.Secret()
		c.MCPKeyHashes = []string{config.Hash(key)}
		if e = config.Save(*cfgPath, c, false); e != nil {
			return e
		}
		emit(p.Object{"mcpKey": key, "path": "/mcp", "authentication": "Authorization: Bearer <mcpKey>", "previousKeysRevoked": true})
		return nil
	}
	if cmd == "serve" {
		installer, err := setup.Open(*cfgPath, *listen, os.Stdout)
		if err != nil {
			return err
		}
		if installer != nil {
			defer installer.Close()
			return serveHTTP(context.Background(), installer.Listen(), installer)
		}
	}
	c, e := config.Load(*cfgPath)
	if e != nil {
		return e
	}
	if c.Admin != nil {
		if cmd == "serve" {
			managed, e := admin.Open(c)
			if e != nil {
				return e
			}
			defer managed.Close()
			return serveHTTP(context.Background(), c.Listen, managed)
		}
		if c, e = admin.EffectiveConfig(c, *accountUser); e != nil {
			return e
		}

		if os.Getenv("SP_SYNC_PASSWORD") != "" || os.Getenv("SP_SYNC_PASSWORD_FILE") != "" {
			return errors.New("managed CLI requires account password files; remove global SP_SYNC_PASSWORD overrides")
		}
	}
	password, e := config.SyncPassword(c)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(c.Database), 0700); e != nil {
		return e
	}
	store, e := storage.Open(c.Database)
	if e != nil {
		return e
	}
	defer store.Close()
	svc, e := service.New(store, password, c.Timezone, c.ExperimentalWrites)
	if e != nil {
		return e
	}
	ctx := context.Background()
	switch cmd {
	case "inspect":
		if *input != "" {
			files, e := readInput(*input)
			if e != nil {
				return e
			}
			for name, b := range files {
				d, _, e := svc.Codec.Decode(b, true)
				if e != nil {
					return e
				}
				emit(summary(name, d))
			}
			return nil
		}
		emit(svc.Capabilities(ctx))
		return nil
	case "import":
		if *input == "" {
			return errors.New("--input required")
		}
		files, e := readInput(*input)
		if e != nil {
			return e
		}
		if _, ok := files["sync-data.json"]; !ok {
			return errors.New("main sync file missing")
		}
		for _, b := range files {
			if _, _, e = svc.Codec.Decode(b, true); e != nil {
				return e
			}
		}
		e = store.Transaction(ctx, func(tx *storage.Tx) error {
			for name, b := range files {
				if _, e := tx.Put("/super-productivity/"+name, b, false, storage.Conditions{NoneMatch: "*"}); e != nil {
					return e
				}
			}
			return nil
		})
		if e != nil {
			return e
		}
		emit(p.Object{"importedFiles": len(files), "source": "uploaded/local backup; not a verified current cloud snapshot"})
		return nil
	case "export":
		if *output == "" {
			return errors.New("--output required")
		}
		var f storage.File
		if *rev > 0 {
			f, e = store.AtRevision(ctx, *rev)
		} else {
			f, e = store.Get(ctx, service.MainPath)
		}
		if e != nil {
			return e
		}
		out, e := os.OpenFile(*output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, e = out.Write(f.Body)
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
		emit(p.Object{"path": *output, "revision": f.Revision, "etag": f.ETag})
		return nil
	case "serve":
		hash, e := hex.DecodeString(c.WebDAVPasswordHash)
		if e != nil {
			return e
		}
		var dh [32]byte
		copy(dh[:], hash)
		dav := &webdav.Handler{Store: store, Username: c.WebDAVUsername, PasswordHash: dh, AllowedOrigins: c.AllowedOrigins}
		mcpHandler := mcpserver.Handler(mcpserver.New(svc), func() []string {
			fresh, e := config.Load(*cfgPath)
			if e != nil {
				return nil
			}
			return fresh.MCPKeyHashes
		}, c.AllowedHosts, c.AllowedOrigins)
		var authorization *oauth.Server
		if c.OAuth != nil {
			loginPassword, err := config.ReadPasswordFile(c.SyncPasswordFile)
			if err != nil {
				return err
			}
			authorization, e = oauth.New(*c.OAuth, store, c.AllowedHosts, loginPassword)
			if e != nil {
				return e
			}
			mcpHandler = mcpserver.OAuthHandler(mcpserver.New(svc), authorization.Verify, authorization.Challenge(), c.AllowedHosts, c.AllowedOrigins)
		}
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			switch {
			case strings.HasPrefix(r.URL.Path, "/assets/ui/"):
				webui.ServeHTTP(w, r)
			case strings.HasPrefix(r.URL.Path, "/oauth/") || strings.HasPrefix(r.URL.Path, "/.well-known/oauth-") || r.URL.Path == "/.well-known/openid-configuration":
				if authorization == nil {
					http.Error(w, "OAuth not configured", 404)
					return
				}
				authorization.ServeHTTP(w, r)
			case r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/"):
				mcpHandler.ServeHTTP(w, r)
			case strings.HasPrefix(r.URL.Path, "/setup"):
				http.NotFound(w, r)
			case r.URL.Path == "/healthz":
				if r.Method != "GET" {
					w.WriteHeader(405)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				io.WriteString(w, `{"status":"ok","installation":"ready","version":"2.3.0","releaseReady":false}`)
			default:
				dav.ServeHTTP(w, r)
			}
		})
		return serveHTTP(ctx, c.Listen, handler)
	default:
		return errors.New("unknown command")
	}
}
func serveHTTP(ctx context.Context, listen string, handler http.Handler) error {
	server := &http.Server{Addr: listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10, ErrorLog: log.New(io.Discard, "", 0)}
	stop, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()
	go func() {
		<-stop.Done()
		shutdown, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = server.Shutdown(shutdown)
	}()
	fmt.Println("Super Productivity MCP listening at", listen, "(development build; request URLs are not logged)")
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
func summary(name string, d p.Object) p.Object {
	state := p.Obj(d["state"])
	return p.Object{"file": name, "version": d["version"], "schemaVersion": d["schemaVersion"], "syncVersion": d["syncVersion"], "tasks": len(p.Arr(p.Obj(state["task"])["ids"])), "recentOps": len(p.Arr(d["recentOps"]))}
}
func readInput(file string) (map[string][]byte, error) {
	if strings.EqualFold(filepath.Ext(file), ".zip") {
		z, e := zip.OpenReader(file)
		if e != nil {
			return nil, e
		}
		defer z.Close()
		out := map[string][]byte{}
		for _, f := range z.File {
			base := filepath.Base(f.Name)
			if base != "sync-data.json" && base != "sync-data.json.bak" {
				continue
			}
			if _, ok := out[base]; ok {
				return nil, errors.New("ambiguous ZIP entries")
			}
			r, e := f.Open()
			if e != nil {
				return nil, e
			}
			b, e := io.ReadAll(io.LimitReader(r, webdav.MaxFileSize+1))
			r.Close()
			if e != nil {
				return nil, e
			}
			if len(b) > webdav.MaxFileSize {
				return nil, errors.New("ZIP entry too large")
			}
			out[base] = b
		}
		return out, nil
	}
	f, e := os.Open(file)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	b, e := io.ReadAll(io.LimitReader(f, webdav.MaxFileSize+1))
	if e != nil {
		return nil, e
	}
	if len(b) > webdav.MaxFileSize {
		return nil, errors.New("file too large")
	}
	return map[string][]byte{"sync-data.json": b}, nil
}
