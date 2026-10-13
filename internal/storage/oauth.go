package storage

import (
	"context"
	"database/sql"
)

type OAuthRecord struct {
	Value   []byte
	Expires int64
}

func authRow(row *sql.Row) (OAuthRecord, error) {
	var v OAuthRecord
	e := row.Scan(&v.Value, &v.Expires)
	if e == sql.ErrNoRows {
		e = ErrNotFound
	}
	return v, e
}
func (s *Store) OAuthGet(ctx context.Context, key string) (OAuthRecord, error) {
	return authRow(s.db.QueryRowContext(ctx, `SELECT value,expires FROM oauth_records WHERE key=?`, key))
}
func (t *Tx) OAuthGet(key string) (OAuthRecord, error) {
	return authRow(t.tx.QueryRow(`SELECT value,expires FROM oauth_records WHERE key=?`, key))
}
func (t *Tx) OAuthPut(key string, value []byte, expires int64) error {
	_, e := t.tx.Exec(`INSERT INTO oauth_records(key,value,expires) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,expires=excluded.expires`, key, value, expires)
	return e
}
func (t *Tx) OAuthDelete(key string) error {
	_, e := t.tx.Exec(`DELETE FROM oauth_records WHERE key=?`, key)
	return e
}
func (t *Tx) OAuthPrune(now int64) error {
	_, e := t.tx.Exec(`DELETE FROM oauth_records WHERE expires<=?`, now)
	return e
}
func (t *Tx) OAuthPendingCount() (int, error) {
	var count int
	e := t.tx.QueryRow(`SELECT count(*) FROM oauth_records WHERE key LIKE 'request:%'`).Scan(&count)
	return count, e
}
