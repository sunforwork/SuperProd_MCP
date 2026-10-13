package storage

func (t *Tx) AtRevision(revision int64) (File, error) {
	return rowFile(t.tx.QueryRow(`SELECT path,body,etag,revision,modified,dir FROM versions WHERE revision=? AND deleted=0`, revision))
}

func (t *Tx) UpdateProposal(p Proposal) error {
	_, err := t.tx.Exec(`UPDATE proposals SET patch=?,patch_hash=?,nonce_hash=?,expires=?,status=?,result=? WHERE id=? AND identity=?`, p.Patch, p.PatchHash, p.NonceHash, p.Expires, p.Status, p.Result, p.ID, p.Identity)
	return err
}

func (t *Tx) ExpireBusinessProposals(now int64) error {
	_, err := t.tx.Exec(`UPDATE proposals SET status='expired',patch=X'',nonce_hash='' WHERE expires<? AND status IN ('draft','pending')`, now)
	return err
}
