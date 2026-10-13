package storage

import "context"

func (s *Store) RevokeOAuth(ctx context.Context) error {
	return s.Transaction(ctx, func(t *Tx) error { _, err := t.tx.Exec(`DELETE FROM oauth_records`); return err })
}
