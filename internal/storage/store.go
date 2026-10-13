package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")
var ErrPrecondition = errors.New("version changed")
var ErrConflict = errors.New("conflict")
var ErrPath = errors.New("invalid WebDAV path")

type File struct {
	Path     string
	Body     []byte
	ETag     string
	Revision int64
	Modified int64
	Dir      bool
	Size     int64
}
type Conditions struct {
	Match     string
	NoneMatch string
	Revision  *int64
}
type Store struct{ db *sql.DB }
type Tx struct{ tx *sql.Tx }

func CleanPath(p string) (string, error) {
	if strings.ContainsAny(p, "\\\x00") || !strings.HasPrefix(p, "/") {
		return "", ErrPath
	}
	for _, s := range strings.Split(p, "/") {
		if s == ".." || s == "." {
			return "", ErrPath
		}
	}
	p = path.Clean(p)
	if p != "/" && p != "/super-productivity" && !strings.HasPrefix(p, "/super-productivity/") {
		return "", ErrPath
	}
	return p, nil
}
func Open(file string) (*Store, error) {
	db, e := sql.Open("sqlite", file)
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(1)
	var version int
	if e = db.QueryRow(`PRAGMA user_version`).Scan(&version); e != nil {
		db.Close()
		return nil, e
	}
	if version > 1 {
		db.Close()
		return nil, errors.New("database schema is newer than this binary")
	}
	s := &Store{db: db}
	_, e = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL; PRAGMA busy_timeout=10000; PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS versions (revision INTEGER PRIMARY KEY AUTOINCREMENT,path TEXT NOT NULL,body BLOB,etag TEXT NOT NULL,modified INTEGER NOT NULL,dir INTEGER NOT NULL,deleted INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS versions_path ON versions(path,revision);
 CREATE TABLE IF NOT EXISTS files (path TEXT PRIMARY KEY,revision INTEGER NOT NULL REFERENCES versions(revision));
 CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS oauth_records (key TEXT PRIMARY KEY,value BLOB NOT NULL,expires INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS oauth_expiry ON oauth_records(expires);
 CREATE TABLE IF NOT EXISTS commands (id TEXT PRIMARY KEY,fingerprint TEXT NOT NULL,result BLOB NOT NULL,created INTEGER NOT NULL);
 CREATE TABLE IF NOT EXISTS proposals (id TEXT PRIMARY KEY,identity TEXT NOT NULL,base_revision INTEGER NOT NULL,patch BLOB NOT NULL,patch_hash TEXT NOT NULL,nonce_hash TEXT NOT NULL,expires INTEGER NOT NULL,status TEXT NOT NULL,result BLOB);
 PRAGMA user_version=1;`)
	if e != nil {
		db.Close()
		return nil, e
	}
	e = s.Transaction(context.Background(), func(t *Tx) error {
		for _, p := range []string{"/", "/super-productivity"} {
			if _, e := t.Get(p); e == ErrNotFound {
				if _, e = t.Put(p, nil, true, Conditions{}); e != nil {
					return e
				}
			} else if e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		db.Close()
		return nil, e
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Transaction(ctx context.Context, f func(*Tx) error) error {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return e
	}
	defer tx.Rollback()
	if e = f(&Tx{tx}); e != nil {
		return e
	}
	return tx.Commit()
}
func rowFile(row *sql.Row) (File, error) {
	var f File
	e := row.Scan(&f.Path, &f.Body, &f.ETag, &f.Revision, &f.Modified, &f.Dir)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	f.Size = int64(len(f.Body))
	return f, e
}

const fileQuery = `SELECT v.path,v.body,v.etag,v.revision,v.modified,v.dir FROM files f JOIN versions v ON v.revision=f.revision WHERE f.path=?`

func (t *Tx) Get(p string) (File, error) { return rowFile(t.tx.QueryRow(fileQuery, p)) }
func (s *Store) Get(ctx context.Context, p string) (File, error) {
	return rowFile(s.db.QueryRowContext(ctx, fileQuery, p))
}
func (t *Tx) Check(p string, c Conditions) error {
	f, e := t.Get(p)
	if e != nil && e != ErrNotFound {
		return e
	}
	exists := e == nil
	if c.Revision != nil && ((!exists && *c.Revision != 0) || (exists && f.Revision != *c.Revision)) {
		return ErrPrecondition
	}
	matches := func(v string, weak bool) bool {
		for _, tag := range strings.Split(v, ",") {
			tag = strings.TrimSpace(tag)
			if weak {
				tag = strings.TrimPrefix(tag, "W/")
			}
			if exists && (tag == "*" || tag == f.ETag) {
				return true
			}
		}
		return false
	}
	if c.Match != "" && !matches(c.Match, false) {
		return ErrPrecondition
	}
	if c.NoneMatch != "" && matches(c.NoneMatch, true) {
		return ErrPrecondition
	}
	return nil
}
func (t *Tx) Put(p string, body []byte, dir bool, c Conditions) (File, error) {
	p, e := CleanPath(p)
	if e != nil {
		return File{}, e
	}
	if e = t.Check(p, c); e != nil {
		return File{}, e
	}
	old, oe := t.Get(p)
	if oe != nil && oe != ErrNotFound {
		return File{}, oe
	}
	if oe == nil && old.Dir != dir {
		return File{}, ErrConflict
	}
	if p != "/" {
		parent, e := t.Get(path.Dir(p))
		if e != nil || !parent.Dir {
			return File{}, ErrConflict
		}
	}
	h := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(h[:]) + `"`
	now := time.Now().UnixMilli()
	r, e := t.tx.Exec(`INSERT INTO versions(path,body,etag,modified,dir) VALUES(?,?,?,?,?)`, p, body, etag, now, dir)
	if e != nil {
		return File{}, e
	}
	rev, e := r.LastInsertId()
	if e != nil {
		return File{}, e
	}
	_, e = t.tx.Exec(`INSERT INTO files(path,revision) VALUES(?,?) ON CONFLICT(path) DO UPDATE SET revision=excluded.revision`, p, rev)
	return File{Path: p, Body: body, ETag: etag, Revision: rev, Modified: now, Dir: dir, Size: int64(len(body))}, e
}
func (t *Tx) Delete(p string, c Conditions) error {
	if p == "/" || p == "/super-productivity" {
		return ErrConflict
	}
	if e := t.Check(p, c); e != nil {
		return e
	}
	f, e := t.Get(p)
	if e != nil {
		return e
	}
	if f.Dir {
		var count int
		if e = t.tx.QueryRow(`SELECT count(*) FROM files WHERE instr(path,?)=1`, p+"/").Scan(&count); e != nil {
			return e
		}
		if count > 0 {
			return ErrConflict
		}
	}
	_, e = t.tx.Exec(`INSERT INTO versions(path,body,etag,modified,dir,deleted) VALUES(?,NULL,?,?,?,1)`, p, f.ETag, time.Now().UnixMilli(), f.Dir)
	if e != nil {
		return e
	}
	_, e = t.tx.Exec(`DELETE FROM files WHERE path=?`, p)
	return e
}
func (s *Store) List(ctx context.Context, p string) ([]File, error) {
	rows, e := s.db.QueryContext(ctx, `SELECT v.path,v.etag,v.revision,v.modified,v.dir,length(v.body) FROM files f JOIN versions v ON v.revision=f.revision ORDER BY v.path`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []File{}
	for rows.Next() {
		var f File
		var size sql.NullInt64
		if e = rows.Scan(&f.Path, &f.ETag, &f.Revision, &f.Modified, &f.Dir, &size); e != nil {
			return nil, e
		}
		f.Size = size.Int64
		if f.Path == p || (f.Path != "/" && path.Dir(f.Path) == p) {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}
func (t *Tx) Setting(key string) (string, error) {
	var v string
	e := t.tx.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if e == sql.ErrNoRows {
		e = ErrNotFound
	}
	return v, e
}
func (t *Tx) SetSetting(key, value string) error {
	_, e := t.tx.Exec(`INSERT INTO settings VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return e
}
func (t *Tx) Command(id, fingerprint string) ([]byte, error) {
	var fp string
	var result []byte
	e := t.tx.QueryRow(`SELECT fingerprint,result FROM commands WHERE id=?`, id).Scan(&fp, &result)
	if e == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if e != nil {
		return nil, e
	}
	if fp != fingerprint {
		return nil, fmt.Errorf("%w: idempotency key reused for different input", ErrConflict)
	}
	return result, nil
}
func (t *Tx) SaveCommand(id, fingerprint string, result []byte) error {
	_, e := t.tx.Exec(`INSERT INTO commands VALUES(?,?,?,?)`, id, fingerprint, result, time.Now().UnixMilli())
	return e
}

type Version struct {
	Revision int64  `json:"revision"`
	Path     string `json:"path"`
	ETag     string `json:"etag"`
	Modified int64  `json:"modified"`
	Deleted  bool   `json:"deleted"`
}

func (s *Store) History(ctx context.Context, p string, limit int) ([]Version, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, e := s.db.QueryContext(ctx, `SELECT revision,path,etag,modified,deleted FROM versions WHERE path=? ORDER BY revision DESC LIMIT ?`, p, limit)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		if e = rows.Scan(&v.Revision, &v.Path, &v.ETag, &v.Modified, &v.Deleted); e != nil {
			return nil, e
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Store) AtRevision(ctx context.Context, rev int64) (File, error) {
	return rowFile(s.db.QueryRowContext(ctx, `SELECT path,body,etag,revision,modified,dir FROM versions WHERE revision=? AND deleted=0`, rev))
}
