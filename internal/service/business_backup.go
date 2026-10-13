package service

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/sunforwork/SuperProd_MCP/internal/engine"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
)

const backupSetting = "business.named-backups.v1"
const BackupGuidance = "When confident about the user's goal, data meaning and result, execute without creating a named backup. Create one only when explicitly requested or a recovery point is genuinely needed. Do not routinely back up free edits, bulk changes or snapshot replacements. Clarify uncertainty instead of treating a backup as permission to guess."

type CreateBackupInput struct {
	RequestID    string `json:"requestId"`
	BaseRevision int64  `json:"baseRevision"`
	Name         string `json:"name"`
}
type RenameBackupInput struct {
	RequestID string `json:"requestId"`
	BackupID  string `json:"backupId"`
	Name      string `json:"name"`
}
type DeleteBackupInput struct {
	RequestID string `json:"requestId"`
	BackupID  string `json:"backupId"`
}
type ListBackupInput struct {
	Offset int `json:"offset,omitempty"`
	Limit  int `json:"limit,omitempty"`
}
type RestoreBackupInput struct {
	BackupID     string `json:"backupId"`
	BaseRevision int64  `json:"baseRevision"`
	Reason       string `json:"reason"`
}

func validBackupName(name string) error {
	if strings.TrimSpace(name) == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") {
		return errors.New("backup name requires 1..256 bytes without control line breaks")
	}
	return nil
}
func (s *Service) backupByID(tx *storage.Tx, id string) (p.Object, error) {
	all, e := s.encryptedSetting(tx, backupSetting)
	if e != nil {
		return nil, e
	}
	b := p.Obj(all[id])
	if b == nil {
		return nil, storage.ErrNotFound
	}
	return b, nil
}
func (s *Service) backupDocument(tx *storage.Tx, b p.Object) (p.Object, error) {
	f, e := tx.AtRevision(p.Int(b["sourceRevision"]))
	if e != nil {
		return nil, e
	}
	if f.Path != MainPath {
		return nil, errors.New("backup revision does not reference main sync file")
	}
	env, format, e := s.Codec.Decode(f.Body, s.RequireEncryption)
	if e != nil {
		return nil, e
	}
	if p.Int(env["version"]) != 2 || p.Int(env["schemaVersion"]) != 4 {
		return nil, errors.New("backup is not v2/schema v4")
	}
	doc := businessDocument(Document{State: p.Obj(env["state"]), Envelope: env, Format: format})
	if fingerprint(doc) != p.Str(b["documentHash"]) {
		return nil, errors.New("backup content hash mismatch")
	}
	return doc, nil
}
func (s *Service) CreateBusinessBackup(ctx context.Context, identity string, in CreateBackupInput) (p.Object, error) {
	if e := validBackupName(in.Name); e != nil {
		return nil, e
	}
	return s.businessRequest(ctx, identity, in.RequestID, "backup-create", in, func(tx *storage.Tx) (p.Object, error) {
		d, e := s.load(tx)
		if e != nil {
			return nil, e
		}
		if d.Version != 2 || p.Int(d.Envelope["schemaVersion"]) != 4 {
			return nil, errors.New("backup requires v2/schema v4")
		}
		if in.BaseRevision != d.File.Revision {
			return nil, &StaleBusinessError{d.File.Revision}
		}
		all, e := s.encryptedSetting(tx, backupSetting)
		if e != nil {
			return nil, e
		}
		doc := businessDocument(d)
		hash := fingerprint(doc)
		for _, raw := range all {
			b := p.Obj(raw)
			if p.Str(b["name"]) == in.Name {
				if p.Str(b["documentHash"]) != hash {
					return nil, errors.New("backup name already exists with different content")
				}
				return p.Object{"status": "reused", "backup": b}, nil
			}
		}
		body, _ := json.Marshal(doc)
		id := engine.NewID()
		b := p.Object{"backupId": id, "name": in.Name, "createdAt": s.Now().UnixMilli(), "sourceRevision": d.File.Revision, "documentHash": hash, "logicalBytes": len(body), "additionalContentBytes": 0, "storage": "encrypted_history_reference"}
		all[id] = b
		if e = s.setEncryptedSetting(tx, backupSetting, all); e != nil {
			return nil, e
		}
		return p.Object{"status": "created", "backup": b}, nil
	})
}
func (s *Service) ListBusinessBackups(ctx context.Context, in ListBackupInput) (out p.Object, err error) {
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		all, e := s.encryptedSetting(tx, backupSetting)
		if e != nil {
			return e
		}
		items := []any{}
		for _, v := range all {
			items = append(items, v)
		}
		sort.Slice(items, func(i, j int) bool {
			a, b := p.Obj(items[i]), p.Obj(items[j])
			if p.Int(a["createdAt"]) == p.Int(b["createdAt"]) {
				return p.Str(a["backupId"]) < p.Str(b["backupId"])
			}
			return p.Int(a["createdAt"]) > p.Int(b["createdAt"])
		})
		offset, limit := in.Offset, in.Limit
		if offset < 0 {
			return errors.New("negative offset")
		}
		if limit < 1 || limit > 100 {
			limit = 50
		}
		if offset > len(items) {
			offset = len(items)
		}
		end := min(offset+limit, len(items))
		out = p.Object{"backups": items[offset:end], "total": len(items), "nextOffset": end, "hasMore": end < len(items), "deletionEffect": "Removes named recovery entry only; existing sync history is retained and physical database size may not shrink.", "backupGuidance": BackupGuidance}
		return nil
	})
	return
}
func (s *Service) RenameBusinessBackup(ctx context.Context, identity string, in RenameBackupInput) (p.Object, error) {
	if e := validBackupName(in.Name); e != nil {
		return nil, e
	}
	return s.businessRequest(ctx, identity, in.RequestID, "backup-rename", in, func(tx *storage.Tx) (p.Object, error) {
		all, e := s.encryptedSetting(tx, backupSetting)
		if e != nil {
			return nil, e
		}
		b := p.Obj(all[in.BackupID])
		if b == nil {
			return nil, storage.ErrNotFound
		}
		for id, raw := range all {
			if id != in.BackupID && p.Str(p.Obj(raw)["name"]) == in.Name {
				return nil, errors.New("backup name already exists")
			}
		}
		b["name"] = in.Name
		if e = s.setEncryptedSetting(tx, backupSetting, all); e != nil {
			return nil, e
		}
		return p.Object{"status": "renamed", "backup": b}, nil
	})
}
func (s *Service) DeleteBusinessBackup(ctx context.Context, identity string, in DeleteBackupInput) (p.Object, error) {
	return s.businessRequest(ctx, identity, in.RequestID, "backup-delete", in, func(tx *storage.Tx) (p.Object, error) {
		all, e := s.encryptedSetting(tx, backupSetting)
		if e != nil {
			return nil, e
		}
		if p.Obj(all[in.BackupID]) == nil {
			return nil, storage.ErrNotFound
		}
		delete(all, in.BackupID)
		if e = s.setEncryptedSetting(tx, backupSetting, all); e != nil {
			return nil, e
		}
		return p.Object{"status": "deleted", "backupId": in.BackupID, "historyRetained": true, "physicalSpaceReclaimed": false}, nil
	})
}
func (s *Service) ProposeBackupRestore(ctx context.Context, identity string, in RestoreBackupInput) (out p.Object, nonce string, err error) {
	if strings.TrimSpace(in.Reason) == "" {
		return nil, "", errors.New("reason required")
	}
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		if e := tx.ExpireBusinessProposals(s.Now().UnixMilli()); e != nil {
			return e
		}
		d, e := s.load(tx)
		if e != nil {
			return e
		}
		if e = s.businessGate(d); e != nil {
			return e
		}
		if d.File.Revision != in.BaseRevision {
			return &StaleBusinessError{d.File.Revision}
		}
		b, e := s.backupByID(tx, in.BackupID)
		if e != nil {
			return e
		}
		doc, e := s.backupDocument(tx, b)
		if e != nil {
			return e
		}
		r := storage.Proposal{ID: engine.NewID(), Identity: identity, BaseRevision: d.File.Revision}
		v := p.Object{"kind": "business-snapshot", "document": doc, "draftVersion": 1, "backupId": in.BackupID}
		out, nonce, e = s.sealBusiness(tx, r, v, d, in.Reason, true)
		return e
	})
	return
}
