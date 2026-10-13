package storage

import "context"

type Usage struct {
	CurrentBytes    int64 `json:"currentBytes"`
	CurrentFiles    int64 `json:"currentFiles"`
	HistoryBytes    int64 `json:"historyBytes"`
	HistoryVersions int64 `json:"historyVersions"`
}

func (s *Store) Usage(ctx context.Context) (Usage, error) {
	var u Usage
	err := s.db.QueryRowContext(ctx, `SELECT
 (SELECT COALESCE(SUM(length(v.body)),0) FROM files f JOIN versions v ON f.revision=v.revision WHERE v.dir=0),
 (SELECT COUNT(*) FROM files f JOIN versions v ON f.revision=v.revision WHERE v.dir=0),
 (SELECT COALESCE(SUM(length(v.body)),0) FROM versions v WHERE v.dir=0 AND v.deleted=0 AND NOT EXISTS(SELECT 1 FROM files f WHERE f.revision=v.revision)),
 (SELECT COUNT(*) FROM versions v WHERE v.dir=0 AND v.deleted=0 AND NOT EXISTS(SELECT 1 FROM files f WHERE f.revision=v.revision))`).Scan(&u.CurrentBytes, &u.CurrentFiles, &u.HistoryBytes, &u.HistoryVersions)
	return u, err
}
