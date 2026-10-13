package storage

import (
	"database/sql"
	"errors"
)

type Proposal struct {
	ID, Identity         string
	BaseRevision         int64
	Patch                []byte
	PatchHash, NonceHash string
	Expires              int64
	Status               string
	Result               []byte
}

func (t *Tx) Proposal(id string) (Proposal, error) {
	var p Proposal
	e := t.tx.QueryRow(`SELECT id,identity,base_revision,patch,patch_hash,nonce_hash,expires,status,result FROM proposals WHERE id=?`, id).Scan(&p.ID, &p.Identity, &p.BaseRevision, &p.Patch, &p.PatchHash, &p.NonceHash, &p.Expires, &p.Status, &p.Result)
	if e == sql.ErrNoRows {
		e = ErrNotFound
	}
	return p, e
}
func (t *Tx) SaveProposal(p Proposal) error {
	_, e := t.tx.Exec(`INSERT INTO proposals VALUES(?,?,?,?,?,?,?,?,?)`, p.ID, p.Identity, p.BaseRevision, p.Patch, p.PatchHash, p.NonceHash, p.Expires, p.Status, p.Result)
	return e
}
func (t *Tx) CompleteProposal(id string, result []byte) error {
	r, e := t.tx.Exec(`UPDATE proposals SET status='committed',nonce_hash='',result=? WHERE id=? AND status='pending'`, result, id)
	if e != nil {
		return e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return e
	}
	if n != 1 {
		return errors.New("proposal already consumed")
	}
	return nil
}
