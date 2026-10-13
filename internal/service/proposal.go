package service

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/sunforwork/SuperProd_MCP/internal/engine"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
	"reflect"
	"strings"
	"time"
)

type Patch struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	From  string `json:"from,omitempty"`
	Value any    `json:"value,omitempty"`
}
type ProposalInput struct {
	BaseRevision int64   `json:"baseRevision,omitempty"`
	DraftID      string  `json:"draftId,omitempty"`
	DraftVersion int64   `json:"draftVersion,omitempty"`
	Reason       string  `json:"reason"`
	Patch        []Patch `json:"patch,omitempty"`
}

func token() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func pointer(s string) ([]string, error) {
	if !strings.HasPrefix(s, "/") {
		return nil, errors.New("JSON Pointer required")
	}
	parts := strings.Split(s[1:], "/")
	for i, v := range parts {
		for j := 0; j < len(v); j++ {
			if v[j] == '~' {
				if j+1 >= len(v) || (v[j+1] != '0' && v[j+1] != '1') {
					return nil, errors.New("invalid JSON Pointer escape")
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(v, "~1", "/"), "~0", "~")
	}
	return parts, nil
}
func patchCommands(state p.Object, patch []Patch) ([]engine.Command, error) {
	commands := []engine.Command{}
	working := p.Clone(state)
	for _, item := range patch {
		parts, e := pointer(item.Path)
		if e != nil {
			return nil, e
		}
		if len(parts) == 5 && parts[0] == "task" && parts[1] == "entities" && parts[3] == "timeSpentOnDay" {
			entity, e := engine.Entity(working, "task", parts[2])
			if e != nil {
				return nil, e
			}
			days := p.Obj(entity["timeSpentOnDay"])
			if days == nil {
				return nil, errors.New("daily time object missing")
			}
			day := parts[4]
			old, exists := days[day]
			if item.Op == "test" {
				if !exists || !reflect.DeepEqual(p.Clone(old), p.Clone(item.Value)) {
					return nil, errors.New("JSON Patch test failed")
				}
				continue
			}
			if (item.Op == "replace" || item.Op == "remove") && !exists {
				return nil, errors.New("patch target does not exist")
			}
			switch item.Op {
			case "add", "replace":
				days[day] = item.Value
			case "remove":
				delete(days, day)
			default:
				return nil, engine.ErrUnsupported
			}
			commands = append(commands, engine.Command{Name: "time_replace_days", Args: p.Object{"id": parts[2], "days": p.Clone(days)}})
			continue
		}
		if len(parts) != 4 || parts[1] != "entities" {
			return nil, fmt.Errorf("%w: patch path %s has no verified action mapping", engine.ErrUnsupported, item.Path)
		}
		model, id, field := parts[0], parts[2], parts[3]
		entity, e := engine.Entity(working, model, id)
		if e != nil {
			return nil, e
		}
		old, exists := entity[field]
		if item.Op == "test" {
			if !exists || !reflect.DeepEqual(p.Clone(old), p.Clone(item.Value)) {
				return nil, errors.New("JSON Patch test failed")
			}
			continue
		}
		if item.Op != "add" && item.Op != "replace" {
			return nil, fmt.Errorf("%w: %s mapping unavailable", engine.ErrUnsupported, item.Op)
		}
		if item.Op == "replace" && !exists {
			return nil, errors.New("replace target does not exist")
		}
		name := ""
		switch model {
		case "task":
			name = "task_update"
			if field == "timeSpentOnDay" {
				commands = append(commands, engine.Command{Name: "time_replace_days", Args: p.Object{"id": id, "days": p.Clone(item.Value)}})
				entity[field] = p.Clone(item.Value)
				continue
			}
		case "note":
			name = "note_update"
		case "project":
			name = "project_update"
		case "tag":
			name = "tag_update"
		default:
			return nil, fmt.Errorf("%w: model %s", engine.ErrUnsupported, model)
		}
		commands = append(commands, engine.Command{Name: name, Args: p.Object{"id": id, "changes": p.Object{field: item.Value}, "titleMode": "literal"}})
		entity[field] = item.Value
	}
	if len(commands) == 0 {
		return nil, errors.New("proposal contains no changes")
	}
	return commands, nil
}
func (s *Service) proposeLegacy(ctx context.Context, identity string, input ProposalInput) (public p.Object, nonce string, err error) {
	if len(input.Patch) == 0 || len(input.Patch) > 100 || strings.TrimSpace(input.Reason) == "" {
		return nil, "", errors.New("reason and 1..100 patch operations required")
	}
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		d, e := s.load(tx)
		if e != nil {
			return e
		}
		if d.File.Revision != input.BaseRevision {
			return storage.ErrPrecondition
		}
		if !s.ExperimentalWrites || d.Version != 2 {
			return errors.New("write gate closed")
		}
		commands, e := patchCommands(d.State, input.Patch)
		if e != nil {
			return e
		}
		before := p.Clone(d.State)
		now := s.Now()
		commandContext := engine.NewContext(d.State, now, s.Location)
		for _, command := range commands {
			actions, e := engine.Compile(d.State, command, commandContext)
			if e != nil {
				return e
			}
			for _, a := range actions {
				if e = engine.Apply(d.State, a, commandContext); e != nil {
					return e
				}
			}
		}
		diff := Diff(before, d.State)
		id := engine.NewID()
		nonce = token()
		hash := fingerprint(input.Patch)
		expires := now.Add(15 * 60 * 1e9).UnixMilli()
		public = p.Object{"proposalId": id, "patchHash": hash, "baseRevision": input.BaseRevision, "expires": expires, "reason": input.Reason, "patch": input.Patch, "diff": diff, "status": "pending", "deviceDelivery": "unknown"}
		stored := p.Object{"public": public, "commands": commands, "plannedAt": now.UnixMilli(), "expectedStateHash": fingerprint(d.State)}
		body, e := s.Codec.Encode(stored, p.Format{Compressed: true, Encrypted: s.RequireEncryption, Version: "2"})
		if e != nil {
			return e
		}
		return tx.SaveProposal(storage.Proposal{ID: id, Identity: identity, BaseRevision: input.BaseRevision, Patch: body, PatchHash: hash, NonceHash: fingerprint(nonce), Expires: expires, Status: "pending"})
	})
	return
}
func (s *Service) ProposalStatus(ctx context.Context, identity, id string) (out p.Object, err error) {
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		proposal, e := tx.Proposal(id)
		if e != nil || proposal.Identity != identity {
			return storage.ErrNotFound
		}
		out = p.Object{"proposalId": id, "status": proposal.Status, "expires": proposal.Expires, "baseRevision": proposal.BaseRevision}
		if proposal.Status == "pending" || proposal.Status == "draft" {
			if s.Now().UnixMilli() > proposal.Expires {
				out["status"] = "expired"
			} else if d, e := s.load(tx); e == nil && d.File.Revision != proposal.BaseRevision {
				out["status"] = "stale"
				out["latestRevision"], out["restartRequired"] = d.File.Revision, true
			}
			if len(proposal.Patch) > 0 {
				if stored, e := s.decodeBusiness(proposal.Patch); e == nil {
					out["draftVersion"] = stored["draftVersion"]
					if backupID := p.Str(stored["backupId"]); backupID != "" {
						if _, e := s.backupByID(tx, backupID); e == storage.ErrNotFound {
							out["status"] = "backup_deleted"
						}
					}
				}
			}
		}
		if len(proposal.Result) > 0 {
			result, e := p.Parse(proposal.Result)
			if e != nil {
				return e
			}
			out["result"] = result
		}
		return nil
	})
	return
}
func (s *Service) CommitProposal(ctx context.Context, identity, id, hash, nonce string) (out p.Object, err error) {
	err = s.Store.Transaction(ctx, func(tx *storage.Tx) error {
		proposal, e := tx.Proposal(id)
		if e != nil || proposal.Identity != identity {
			return storage.ErrNotFound
		}
		got := fingerprint(nonce)
		if proposal.Status != "pending" || subtle.ConstantTimeCompare([]byte(proposal.NonceHash), []byte(got)) != 1 {
			return errors.New("invalid or consumed approval credential")
		}
		if proposal.PatchHash != hash || s.Now().UnixMilli() > proposal.Expires {
			return errors.New("proposal changed or expired")
		}
		stored, _, e := s.Codec.Decode(proposal.Patch, s.RequireEncryption)
		if e != nil {
			return e
		}
		if p.Str(stored["kind"]) == "business-snapshot" {
			out, e = s.commitBusinessSnapshot(tx, identity, proposal, stored)
			return e
		}
		b, e := json.Marshal(stored["commands"])
		if e != nil {
			return e
		}
		var commands []engine.Command
		if e = json.Unmarshal(b, &commands); e != nil {
			return e
		}
		out, e = s.executeAt(tx, identity, Mutation{RequestID: "proposal:" + id, BaseRevision: proposal.BaseRevision, Commands: commands}, time.UnixMilli(p.Int(stored["plannedAt"])))
		if e != nil {
			return e
		}
		verified, e := s.load(tx)
		if e != nil {
			return e
		}
		if fingerprint(verified.State) != p.Str(stored["expectedStateHash"]) {
			return errors.New("commit differs from approved preview; transaction rolled back")
		}
		result, _ := json.Marshal(out)
		return tx.CompleteProposal(id, result)
	})
	return
}
