package storage

import (
	"context"
	"strings"
	"time"
)

type OAuthIndexEntry struct {
	Hash    string
	Expires int64
}

func (s *Store) OAuthAccessIndex(ctx context.Context) ([]OAuthIndexEntry, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,expires FROM oauth_records WHERE key LIKE 'access:%' AND expires>?`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OAuthIndexEntry{}
	for rows.Next() {
		var key string
		var expires int64
		if err = rows.Scan(&key, &expires); err != nil {
			return nil, err
		}
		out = append(out, OAuthIndexEntry{Hash: strings.TrimPrefix(key, "access:"), Expires: expires})
	}
	return out, rows.Err()
}
