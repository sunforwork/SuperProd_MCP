package webdav

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sunforwork/SuperProd_MCP/internal/httpx"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
)

const MaxFileSize = 64 << 20

type Handler struct {
	Store          *storage.Store
	Username       string
	PasswordHash   [32]byte
	AllowedOrigins []string
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("DAV", "1")
	w.Header().Set("Allow", "GET, HEAD, PUT, DELETE, PROPFIND, MKCOL, OPTIONS")
	if origin := r.Header.Get("Origin"); origin != "" {
		allowed := false
		for _, v := range h.AllowedOrigins {
			if v == origin {
				allowed = true
				break
			}
		}
		if !allowed {
			httpx.Reject(w, r, "origin forbidden", 403)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Expose-Headers", "ETag, Content-Length, Last-Modified, DAV")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, PUT, DELETE, PROPFIND, MKCOL, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Depth, If-Match, If-None-Match")
	}
	if r.Method == "OPTIONS" {
		w.WriteHeader(204)
		return
	}
	user, pass, ok := r.BasicAuth()
	ph := sha256.Sum256([]byte(pass))
	uh := sha256.Sum256([]byte(user))
	expectedUser := sha256.Sum256([]byte(h.Username))
	if !ok || subtle.ConstantTimeCompare(ph[:], h.PasswordHash[:])&subtle.ConstantTimeCompare(uh[:], expectedUser[:]) != 1 {
		w.Header().Set("WWW-Authenticate", `Basic realm="Super Productivity", charset="UTF-8"`)
		httpx.Reject(w, r, "authentication required", 401)
		return
	}
	p, e := storage.CleanPath(r.URL.Path)
	if e != nil {
		http.Error(w, "invalid path", 400)
		return
	}
	conditions := storage.Conditions{Match: r.Header.Get("If-Match"), NoneMatch: r.Header.Get("If-None-Match")}
	switch r.Method {
	case "GET", "HEAD":
		f, e := h.Store.Get(r.Context(), p)
		if e != nil {
			fail(w, e)
			return
		}
		if f.Dir {
			http.Error(w, "collection", 405)
			return
		}
		headers(w, f)
		if conditions.Match != "" && !match(conditions.Match, f.ETag, false) {
			w.WriteHeader(412)
			return
		}
		if match(conditions.NoneMatch, f.ETag, true) {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(f.Body)))
		if r.Method == "GET" {
			w.Write(f.Body)
		}
	case "PUT":
		r.Body = http.MaxBytesReader(w, r.Body, MaxFileSize)
		body, e := io.ReadAll(r.Body)
		if e != nil {
			http.Error(w, "body too large or unreadable", 413)
			return
		}
		var f storage.File
		created := false
		e = h.Store.Transaction(r.Context(), func(tx *storage.Tx) error {
			_, e := tx.Get(p)
			created = errors.Is(e, storage.ErrNotFound)
			f, e = tx.Put(p, body, false, conditions)
			return e
		})
		if e != nil {
			fail(w, e)
			return
		}
		headers(w, f)
		if created {
			w.WriteHeader(201)
		} else {
			w.WriteHeader(204)
		}
	case "DELETE":
		e = h.Store.Transaction(r.Context(), func(tx *storage.Tx) error { return tx.Delete(p, conditions) })
		if e != nil {
			fail(w, e)
			return
		}
		w.WriteHeader(204)
	case "MKCOL":
		b, e := io.ReadAll(io.LimitReader(r.Body, 1))
		if e != nil || len(b) > 0 {
			http.Error(w, "MKCOL body unsupported", 415)
			return
		}
		e = h.Store.Transaction(r.Context(), func(tx *storage.Tx) error {
			if _, e := tx.Get(p); e == nil {
				return storage.ErrConflict
			} else if e != storage.ErrNotFound {
				return e
			}
			_, e := tx.Put(p, nil, true, storage.Conditions{})
			return e
		})
		if e != nil {
			if errors.Is(e, storage.ErrConflict) {
				http.Error(w, "collection exists or parent missing", 405)
			} else {
				fail(w, e)
			}
			return
		}
		w.WriteHeader(201)
	case "PROPFIND":
		f, e := h.Store.Get(r.Context(), p)
		if e != nil {
			fail(w, e)
			return
		}
		depth := r.Header.Get("Depth")
		if depth == "" {
			depth = "infinity"
		}
		if depth != "0" && depth != "1" {
			http.Error(w, "only Depth 0 and 1 supported", 403)
			return
		}

		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if e != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		if len(body) > 0 {
			var req struct{ XMLName xml.Name }
			if e = xml.Unmarshal(body, &req); e != nil || req.XMLName.Local != "propfind" {
				http.Error(w, "invalid XML", 400)
				return
			}
		}
		fs := []storage.File{f}
		if depth == "1" && f.Dir {
			fs, e = h.Store.List(r.Context(), p)
			if e != nil {
				fail(w, e)
				return
			}
		}
		var out strings.Builder
		out.WriteString(`<?xml version="1.0" encoding="utf-8"?><d:multistatus xmlns:d="DAV:">`)
		escape := func(s string) string { var b strings.Builder; xml.EscapeText(&b, []byte(s)); return b.String() }
		for _, v := range fs {
			u := url.URL{Path: v.Path}
			href := u.EscapedPath()
			if v.Dir && !strings.HasSuffix(href, "/") {
				href += "/"
			}
			out.WriteString("<d:response><d:href>" + escape(href) + "</d:href><d:propstat><d:prop><d:resourcetype>")
			if v.Dir {
				out.WriteString("<d:collection/>")
			}
			out.WriteString("</d:resourcetype><d:getetag>" + escape(v.ETag) + "</d:getetag><d:getlastmodified>" + time.UnixMilli(v.Modified).UTC().Format(http.TimeFormat) + "</d:getlastmodified><d:getcontentlength>" + strconv.FormatInt(v.Size, 10) + "</d:getcontentlength></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>")
		}
		out.WriteString("</d:multistatus>")
		w.Header().Set("Content-Type", "application/xml; charset=utf-8")
		w.WriteHeader(207)
		io.WriteString(w, out.String())
	default:
		http.Error(w, "method unsupported", 405)
	}
}
func match(value, etag string, weak bool) bool {
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		if weak {
			v = strings.TrimPrefix(v, "W/")
		}
		if v == "*" || v == etag {
			return true
		}
	}
	return false
}
func headers(w http.ResponseWriter, f storage.File) {
	w.Header().Set("ETag", f.ETag)
	w.Header().Set("Last-Modified", time.UnixMilli(f.Modified).UTC().Format(http.TimeFormat))
}
func fail(w http.ResponseWriter, e error) {
	code := 500
	switch {
	case errors.Is(e, storage.ErrNotFound):
		code = 404
	case errors.Is(e, storage.ErrPrecondition):
		code = 412
	case errors.Is(e, storage.ErrConflict):
		code = 409
	case errors.Is(e, storage.ErrPath):
		code = 400
	}
	http.Error(w, http.StatusText(code), code)
}
