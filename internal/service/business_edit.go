package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/sunforwork/SuperProd_MCP/internal/engine"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
)

const maxBusinessBytes = 64 << 20
const draftTTL = 30 * time.Minute
const approvalTTL = 15 * time.Minute

type StaleBusinessError struct {
	LatestRevision int64 `json:"latestRevision"`
}

func (e *StaleBusinessError) Error() string {
	return fmt.Sprintf("version_changed: latestRevision=%d; restartRequired=true. Inform the user, read the latest cloud business JSON, restart editing and obtain a new approval. Do not merge or retry the old proposal.", e.LatestRevision)
}
func (e *StaleBusinessError) Unwrap() error { return storage.ErrPrecondition }

type BeginEditInput struct {
	RequestID    string `json:"requestId"`
	BaseRevision int64  `json:"baseRevision"`
}
type ApplyEditInput struct {
	RequestID    string  `json:"requestId"`
	DraftID      string  `json:"draftId"`
	DraftVersion int64   `json:"draftVersion"`
	Patch        []Patch `json:"patch"`
}
type CancelEditInput struct {
	DraftID string `json:"draftId"`
}
type BusinessReadInput struct {
	Source       string `json:"source,omitempty"`
	ID           string `json:"id,omitempty"`
	Path         string `json:"path,omitempty"`
	Offset       int    `json:"offset,omitempty"`
	Limit        int    `json:"limit,omitempty"`
	Mode         string `json:"mode,omitempty"`
	BaseRevision int64  `json:"baseRevision,omitempty"`
	DraftVersion int64  `json:"draftVersion,omitempty"`
}
type EditDiffInput struct {
	ProposalID string `json:"proposalId"`
	Offset     int    `json:"offset,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

func businessDocument(d Document) p.Object {
	out := p.Clone(d.State)
	for _, key := range []string{"archiveYoung", "archiveOld"} {
		if value, ok := d.Envelope[key]; ok {
			out[key] = p.Clone(value)
		}
	}
	return out
}
func (s *Service) businessGate(d Document) error {
	if !s.ExperimentalWrites {
		return errors.New("write gate closed")
	}
	if d.Version != 2 || p.Int(d.Envelope["schemaVersion"]) != 4 {
		return errors.New("JSON editing requires v2/schema v4")
	}
	return nil
}
func checkBusinessSize(doc p.Object) error {
	b, e := json.Marshal(doc)
	if e != nil {
		return e
	}
	if len(b) > maxBusinessBytes {
		return errors.New("business document exceeds 64 MiB")
	}
	var clientJSON any
	if e = json.Unmarshal(b, &clientJSON); e != nil || !jsonEqual(doc, clientJSON) {
		return errors.New("JSON contains a number that cannot round-trip through the client's JavaScript number representation")
	}
	for _, key := range []string{"version", "schemaVersion", "syncVersion", "vectorClock", "snapshotBaseClock", "recentOps", "oldestOpSyncVersion", "clientId", "lastModified"} {
		if _, ok := doc[key]; ok {
			return fmt.Errorf("/%s is server-controlled protocol metadata, not business data", key)
		}
	}
	return nil
}
func (s *Service) encodeBusiness(v p.Object) ([]byte, error) {
	return s.Codec.Encode(v, p.Format{Compressed: true, Encrypted: s.RequireEncryption, Version: "2"})
}
func (s *Service) decodeBusiness(b []byte) (p.Object, error) {
	v, _, e := s.Codec.Decode(b, s.RequireEncryption)
	return v, e
}
func (s *Service) saveBusinessRecord(tx *storage.Tx, record storage.Proposal, data p.Object, create bool) error {
	body, e := s.encodeBusiness(data)
	if e != nil {
		return e
	}
	record.Patch = body
	if create {
		return tx.SaveProposal(record)
	}
	return tx.UpdateProposal(record)
}
func (s *Service) businessRecord(tx *storage.Tx, identity, id string) (storage.Proposal, p.Object, error) {
	r, e := tx.Proposal(id)
	if e != nil || r.Identity != identity {
		return r, nil, storage.ErrNotFound
	}
	if r.Status != "draft" && r.Status != "pending" {
		return r, nil, fmt.Errorf("edit is %s", r.Status)
	}
	if s.Now().UnixMilli() > r.Expires {
		return r, nil, errors.New("edit expired; read latest data and start again")
	}
	v, e := s.decodeBusiness(r.Patch)
	if e != nil {
		return r, nil, e
	}
	if p.Str(v["kind"]) != "business-snapshot" {
		return r, nil, errors.New("not a JSON draft")
	}
	if id := p.Str(v["backupId"]); id != "" {
		if _, e := s.backupByID(tx, id); e != nil {
			return r, nil, errors.New("backup deleted; rollback proposal invalid")
		}
	}
	d, e := s.load(tx)
	if e != nil {
		return r, nil, e
	}
	if d.File.Revision != r.BaseRevision {
		return r, nil, &StaleBusinessError{d.File.Revision}
	}
	if r.Status == "draft" {
		r.Expires = s.Now().Add(draftTTL).UnixMilli()
		if e := tx.UpdateProposal(r); e != nil {
			return r, nil, e
		}
	}
	return r, v, nil
}
func draftPublic(record storage.Proposal, data p.Object) p.Object {
	return p.Object{"draftId": record.ID, "draftVersion": data["draftVersion"], "baseRevision": record.BaseRevision, "expires": record.Expires, "status": record.Status}
}

func (s *Service) businessRequest(ctx context.Context, identity, requestID, kind string, input any, fn func(*storage.Tx) (p.Object, error)) (out p.Object, err error) {
	if requestID == "" || len(requestID) > 128 {
		return nil, errors.New("requestId of 1..128 characters required")
	}
	key := "business:" + identity + ":" + requestID
	hash := fingerprint(p.Object{"kind": kind, "input": input})
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		if e := tx.ExpireBusinessProposals(s.Now().UnixMilli()); e != nil {
			return e
		}
		if body, e := tx.Command(key, hash); e == nil {
			out, e = s.decodeBusiness(body)
			return e
		} else if e != storage.ErrNotFound {
			return e
		}
		var e error
		out, e = fn(tx)
		if e != nil {
			return e
		}
		body, e := s.encodeBusiness(out)
		if e != nil {
			return e
		}
		return tx.SaveCommand(key, hash, body)
	})
	return
}
func (s *Service) BeginFreeEdit(ctx context.Context, identity string, in BeginEditInput) (p.Object, error) {
	return s.businessRequest(ctx, identity, in.RequestID, "begin", in, func(tx *storage.Tx) (p.Object, error) {
		d, e := s.load(tx)
		if e != nil {
			return nil, e
		}
		if e = s.businessGate(d); e != nil {
			return nil, e
		}
		if in.BaseRevision != d.File.Revision {
			return nil, &StaleBusinessError{d.File.Revision}
		}
		r := storage.Proposal{ID: engine.NewID(), Identity: identity, BaseRevision: in.BaseRevision, Status: "draft", Expires: s.Now().Add(draftTTL).UnixMilli()}
		v := p.Object{"kind": "business-snapshot", "document": businessDocument(d), "draftVersion": 1}
		if e = s.saveBusinessRecord(tx, r, v, true); e != nil {
			return nil, e
		}
		return draftPublic(r, v), nil
	})
}
func (s *Service) ApplyFreeEdit(ctx context.Context, identity string, in ApplyEditInput) (p.Object, error) {
	return s.businessRequest(ctx, identity, in.RequestID, "apply", in, func(tx *storage.Tx) (p.Object, error) {
		r, v, e := s.businessRecord(tx, identity, in.DraftID)
		if e != nil {
			return nil, e
		}
		if p.Int(v["draftVersion"]) != in.DraftVersion {
			return nil, errors.New("draft version changed; reread draft before editing")
		}
		if p.Str(v["backupId"]) != "" {
			return nil, errors.New("rollback proposals are immutable; start a free-edit draft instead")
		}
		if len(in.Patch) == 0 {
			return nil, errors.New("patch required")
		}
		doc, e := applyJSONPatch(p.Obj(v["document"]), in.Patch)
		if e != nil {
			return nil, e
		}
		if e = checkBusinessSize(doc); e != nil {
			return nil, e
		}
		v["document"], v["draftVersion"] = doc, in.DraftVersion+1
		delete(v, "public")
		r.Status, r.PatchHash, r.NonceHash, r.Expires = "draft", "", "", s.Now().Add(draftTTL).UnixMilli()
		if e = s.saveBusinessRecord(tx, r, v, false); e != nil {
			return nil, e
		}
		return draftPublic(r, v), nil
	})
}
func (s *Service) CancelFreeEdit(ctx context.Context, identity, id string) (out p.Object, err error) {
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		r, e := tx.Proposal(id)
		if e != nil || r.Identity != identity {
			return storage.ErrNotFound
		}
		if r.Status == "committed" {
			return errors.New("committed edit cannot be cancelled")
		}
		r.Status, r.NonceHash, r.Patch = "cancelled", "", []byte{}
		out = p.Object{"draftId": id, "status": "cancelled"}
		return tx.UpdateProposal(r)
	})
	return
}

func (s *Service) Propose(ctx context.Context, identity string, in ProposalInput) (out p.Object, nonce string, err error) {
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
		var r storage.Proposal
		var v p.Object
		create := in.DraftID == ""
		if !create {
			if len(in.Patch) > 0 {
				return errors.New("use apply_free_edit to change a draft before sealing")
			}
			r, v, e = s.businessRecord(tx, identity, in.DraftID)
			if e != nil {
				return e
			}
			if in.DraftVersion != p.Int(v["draftVersion"]) {
				return errors.New("draftVersion mismatch")
			}
		} else {
			if in.BaseRevision != d.File.Revision {
				return &StaleBusinessError{d.File.Revision}
			}
			if len(in.Patch) == 0 {
				return errors.New("draftId or JSON patch required")
			}
			doc, e := applyJSONPatch(businessDocument(d), in.Patch)
			if e != nil {
				return e
			}
			r = storage.Proposal{ID: engine.NewID(), Identity: identity, BaseRevision: d.File.Revision}
			v = p.Object{"kind": "business-snapshot", "document": doc, "draftVersion": 1}
		}
		out, nonce, e = s.sealBusiness(tx, r, v, d, in.Reason, create)
		return e
	})
	return
}
func (s *Service) sealBusiness(tx *storage.Tx, r storage.Proposal, v p.Object, d Document, reason string, create bool) (p.Object, string, error) {
	doc := p.Obj(v["document"])

	oldSync := p.Obj(p.Obj(d.State["globalConfig"])["sync"])
	newSync := p.Obj(p.Obj(doc["globalConfig"])["sync"])
	for _, key := range []string{"syncInterval", "isManualSyncOnly", "syncProvider", "isEnabled", "isEncryptionEnabled"} {
		a, ae := oldSync[key]
		b, be := newSync[key]
		if ae != be || !jsonEqual(a, b) {
			return nil, "", fmt.Errorf("/globalConfig/sync/%s is device-local; change it in the device client", key)
		}
	}
	if e := checkBusinessSize(doc); e != nil {
		return nil, "", e
	}
	if e := engine.ValidateBusiness(doc); e != nil {
		return nil, "", e
	}
	diff := Diff(businessDocument(d), doc)
	if len(diff) == 0 {
		return nil, "", errors.New("document has no changes")
	}
	nonce := token()
	r.Status = "pending"
	r.Expires = s.Now().Add(approvalTTL).UnixMilli()
	r.PatchHash = fingerprint(p.Object{"document": doc, "baseRevision": r.BaseRevision, "identity": r.Identity, "backupId": v["backupId"]})
	r.NonceHash = fingerprint(nonce)
	public := p.Object{"proposalId": r.ID, "draftId": r.ID, "draftVersion": v["draftVersion"], "baseRevision": r.BaseRevision, "patchHash": r.PatchHash, "expires": r.Expires, "reason": reason, "status": "pending", "mode": "native_state_replacement", "deviceDelivery": "unknown", "diffCount": len(diff), "diffSummary": diffSummary(diff), "backupId": v["backupId"], "impact": "Replaces the complete business snapshot, including both archives. Offline devices may need native conflict resolution on their next sync."}
	public["diff"] = safeDiffPage(diff, businessDocument(d), doc, 0, 20)["items"]
	v["public"] = public
	if e := s.saveBusinessRecord(tx, r, v, create); e != nil {
		return nil, "", e
	}
	return public, nonce, nil
}
func diffSummary(diff []any) p.Object {
	counts := p.Object{"added": 0, "removed": 0, "modified": 0}
	models := p.Object{}
	for _, raw := range diff {
		v := p.Obj(raw)
		kind := "modified"
		if !p.Bool(v["beforeExists"]) {
			kind = "added"
		} else if !p.Bool(v["afterExists"]) {
			kind = "removed"
		}
		counts[kind] = p.Int(counts[kind]) + 1
		parts, _ := jsonPointer(p.Str(v["path"]))
		if len(parts) > 0 {
			models[parts[0]] = p.Int(models[parts[0]]) + 1
		}
	}
	counts["models"] = models
	return counts
}
func safeDiffPage(diff []any, before, after p.Object, offset, limit int) p.Object {
	if offset < 0 {
		offset = 0
	}
	if limit < 1 || limit > 100 {
		limit = 20
	}
	if offset > len(diff) {
		offset = len(diff)
	}
	items := []any{}
	size := 0
	rb, ra := redact(before), redact(after)
	for i := offset; i < len(diff) && len(items) < limit; i++ {
		v := p.Clone(p.Obj(diff[i]))
		parts, _ := jsonPointer(p.Str(v["path"]))
		if p.Bool(v["beforeExists"]) {
			v["before"], _ = jsonAt(rb, parts)
		}
		if p.Bool(v["afterExists"]) {
			v["after"], _ = jsonAt(ra, parts)
		}
		for _, key := range []string{"before", "after"} {
			body, _ := json.Marshal(v[key])
			if len(body) > 32<<10 {
				v[key] = p.Object{"omitted": true, "bytes": len(body), "readPath": v["path"], "side": key}
			}
		}
		body, _ := json.Marshal(v)
		if size+len(body) > 256<<10 && len(items) > 0 {
			break
		}
		size += len(body)
		items = append(items, v)
	}
	return p.Object{"items": items, "offset": offset, "nextOffset": offset + len(items), "total": len(diff), "hasMore": offset+len(items) < len(diff)}
}
func (s *Service) EditDiff(ctx context.Context, identity string, in EditDiffInput) (out p.Object, err error) {
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		r, v, e := s.businessRecord(tx, identity, in.ProposalID)
		if e != nil {
			return e
		}
		if r.Status != "pending" {
			return errors.New("seal the draft before reading its approval diff")
		}
		d, e := s.load(tx)
		if e != nil {
			return e
		}
		before, after := businessDocument(d), p.Obj(v["document"])
		out = safeDiffPage(Diff(before, after), before, after, in.Offset, in.Limit)
		out["proposalId"], out["patchHash"] = r.ID, r.PatchHash
		return nil
	})
	return
}
func (s *Service) commitBusinessSnapshot(tx *storage.Tx, identity string, r storage.Proposal, v p.Object) (p.Object, error) {
	d, e := s.load(tx)
	if e != nil {
		return nil, e
	}
	if e = s.businessGate(d); e != nil {
		return nil, e
	}
	if r.BaseRevision != d.File.Revision {
		return nil, &StaleBusinessError{d.File.Revision}
	}
	if backupID := p.Str(v["backupId"]); backupID != "" {
		if _, e = s.backupByID(tx, backupID); e != nil {
			return nil, errors.New("backup deleted; rollback proposal invalid")
		}
	}
	doc := p.Obj(v["document"])
	if fingerprint(p.Object{"document": doc, "baseRevision": r.BaseRevision, "identity": identity, "backupId": v["backupId"]}) != r.PatchHash {
		return nil, errors.New("approved document hash mismatch")
	}
	if e = engine.ValidateBusiness(doc); e != nil {
		return nil, e
	}
	client, e := tx.Setting("clientId")
	if e == storage.ErrNotFound {
		client = engine.NewID()
		e = tx.SetSetting("clientId", client)
	}
	if e != nil {
		return nil, e
	}
	clock := p.Clone(p.Obj(d.Envelope["vectorClock"]))
	if clock == nil {
		return nil, errors.New("vector clock missing")
	}
	clock[client] = p.Int(clock[client]) + 1
	env := p.Clone(d.Envelope)
	state := p.Clone(doc)
	for _, key := range []string{"archiveYoung", "archiveOld"} {
		env[key] = state[key]
		delete(state, key)
	}
	env["state"], env["syncVersion"], env["vectorClock"], env["snapshotBaseClock"] = state, 1, clock, p.Clone(clock)
	env["clientId"], env["lastModified"], env["recentOps"] = client, s.Now().UnixMilli(), []any{}
	delete(env, "oldestOpSyncVersion")
	body, e := s.Codec.Encode(env, d.Format)
	if e != nil {
		return nil, e
	}
	file, e := tx.Put(MainPath, body, false, storage.Conditions{Revision: &r.BaseRevision})
	if e != nil {
		return nil, e
	}

	if _, e = tx.Put(MainPath+".bak", body, false, storage.Conditions{}); e != nil {
		return nil, e
	}
	verified, e := s.load(tx)
	if e != nil {
		return nil, e
	}
	if fingerprint(businessDocument(verified)) != fingerprint(doc) {
		return nil, errors.New("committed document differs from approved preview")
	}
	out := p.Object{"status": "committed_to_webdav", "mode": "native_state_replacement", "revision": file.Revision, "previousRevision": d.File.Revision, "etag": file.ETag, "syncVersion": 1, "deviceDelivery": "unknown", "verifiedDocumentHash": fingerprint(doc)}
	r.Status, r.NonceHash, r.Patch = "committed", "", []byte{}
	r.Result, _ = json.Marshal(out)
	if e = tx.UpdateProposal(r); e != nil {
		return nil, e
	}
	return out, nil
}

func jsonPage(value any, path, mode string, offset, limit int) (p.Object, error) {
	if offset < 0 {
		return nil, errors.New("negative offset")
	}
	if mode != "" && mode != "entries" && mode != "value" {
		return nil, errors.New("mode must be entries or value")
	}
	if str, ok := value.(string); ok && len(str) > 64<<10 && mode != "value" {
		runes := []rune(str)
		if offset > len(runes) {
			offset = len(runes)
		}
		if limit < 1 || limit > 32768 {
			limit = 16384
		}
		end := min(offset+limit, len(runes))
		return p.Object{"path": path, "kind": "string", "chunk": string(runes[offset:end]), "offset": offset, "nextOffset": end, "total": len(runes), "hasMore": end < len(runes), "offsetUnit": "unicode_codepoints"}, nil
	}
	if limit < 1 || limit > 100 {
		limit = 50
	}
	if mode == "value" {
		b, _ := json.Marshal(value)
		if len(b) > 512<<10 {
			return nil, errors.New("value exceeds 512 KiB; read child paths or use paginated entries")
		}
		return p.Object{"path": path, "value": value}, nil
	}
	entries := []any{}
	var keys []string
	var count int
	switch v := value.(type) {
	case map[string]any:
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		count = len(keys)
	case []any:
		count = len(v)
	default:
		return p.Object{"path": path, "value": value}, nil
	}
	if offset > count {
		offset = count
	}
	size := 0
	for i := offset; i < count && len(entries) < limit; i++ {
		var key string
		var item any
		switch v := value.(type) {
		case map[string]any:
			key = keys[i]
			item = v[key]
		case []any:
			key = fmt.Sprint(i)
			item = v[i]
		}
		b, _ := json.Marshal(item)
		entry := p.Object{"path": path + "/" + escaped(key), "key": key, "bytes": len(b)}
		if len(b) <= 16<<10 {
			entry["value"] = item
		} else {
			entry["expand"] = true
		}
		encoded, _ := json.Marshal(entry)
		if size+len(encoded) > 256<<10 && len(entries) > 0 {
			break
		}
		entries = append(entries, entry)
		size += len(encoded)
	}
	return p.Object{"path": path, "entries": entries, "total": count, "offset": offset, "nextOffset": offset + len(entries), "hasMore": offset+len(entries) < count}, nil
}
func (s *Service) ReadBusinessJSON(ctx context.Context, identity string, in BusinessReadInput) (out p.Object, err error) {
	parts, e := jsonPointer(in.Path)
	if e != nil {
		return nil, e
	}
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		var doc p.Object
		var revision, version int64
		switch in.Source {
		case "", "current":
			d, e := s.load(tx)
			if e != nil {
				return e
			}
			if d.Version != 2 {
				return errors.New("business JSON editing targets v2")
			}
			revision = d.File.Revision
			if in.BaseRevision != 0 && revision != in.BaseRevision {
				return &StaleBusinessError{revision}
			}
			doc = businessDocument(d)
		case "draft", "proposal", "before":
			r, v, e := s.businessRecord(tx, identity, in.ID)
			if e != nil {
				return e
			}
			revision, version = r.BaseRevision, p.Int(v["draftVersion"])
			if in.DraftVersion != 0 && in.DraftVersion != version {
				return errors.New("draft version changed")
			}
			doc = p.Obj(v["document"])
			if in.Source == "before" {
				d, e := s.load(tx)
				if e != nil {
					return e
				}
				doc = businessDocument(d)
			}
		case "backup":
			b, e := s.backupByID(tx, in.ID)
			if e != nil {
				return e
			}
			revision = p.Int(b["sourceRevision"])
			doc, e = s.backupDocument(tx, b)
			if e != nil {
				return e
			}
		default:
			return errors.New("source must be current, draft, proposal, before or backup")
		}
		value, e := jsonAt(redact(doc), parts)
		if e != nil {
			return e
		}
		out, e = jsonPage(value, in.Path, in.Mode, in.Offset, in.Limit)
		if e != nil {
			return e
		}
		out["baseRevision"], out["draftVersion"] = revision, version
		return nil
	})
	return
}

func (s *Service) encryptedSetting(tx *storage.Tx, key string) (p.Object, error) {
	v, e := tx.Setting(key)
	if e == storage.ErrNotFound {
		return p.Object{}, nil
	}
	if e != nil {
		return nil, e
	}
	body, e := base64.StdEncoding.DecodeString(v)
	if e != nil {
		return nil, e
	}
	return s.decodeBusiness(body)
}
func (s *Service) setEncryptedSetting(tx *storage.Tx, key string, value p.Object) error {
	body, e := s.encodeBusiness(value)
	if e != nil {
		return e
	}
	return tx.SetSetting(key, base64.StdEncoding.EncodeToString(body))
}
