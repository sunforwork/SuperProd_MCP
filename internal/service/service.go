package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/sunforwork/SuperProd_MCP/internal/compat"
	"github.com/sunforwork/SuperProd_MCP/internal/engine"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/storage"
	"path"
	"strings"
	"time"
)

const MainPath = "/super-productivity/sync-data.json"
const OpsPath = "/super-productivity/sync-ops.json"

type Service struct {
	Store              *storage.Store
	Codec              *p.Codec
	Location           *time.Location
	RequireEncryption  bool
	ExperimentalWrites bool
	Now                func() time.Time
}
type Document struct {
	File         storage.File
	Envelope     p.Object
	State        p.Object
	Format       p.Format
	Version      int
	Dependencies []storage.File
	Archives     p.Object
}

func New(store *storage.Store, password, tz string, writes bool) (*Service, error) {
	loc, e := time.LoadLocation(tz)
	if e != nil {
		return nil, e
	}
	return &Service{store, p.NewCodec(password), loc, password != "", writes, time.Now}, nil
}
func (s *Service) load(tx *storage.Tx) (Document, error) {
	f, e := tx.Get(MainPath)
	if e != nil && e != storage.ErrNotFound {
		return Document{}, e
	}
	var doc p.Object
	var format p.Format
	if e == nil {
		doc, format, e = s.Codec.Decode(f.Body, s.RequireEncryption)
		if e != nil {
			return Document{}, e
		}
		if p.Int(doc["version"]) == 2 {
			if p.Obj(doc["state"]) == nil {
				return Document{}, errors.New("v2 state missing")
			}
			return Document{File: f, Envelope: doc, State: p.Obj(doc["state"]), Format: format, Version: 2, Archives: archiveViews(doc)}, nil
		}
		if p.Int(doc["version"]) != 3 {
			return Document{}, errors.New("unsupported file version")
		}
	}
	f, e = tx.Get(OpsPath)
	if e != nil {
		return Document{}, e
	}
	doc, format, e = s.Codec.Decode(f.Body, s.RequireEncryption)
	if e != nil {
		return Document{}, e
	}
	if p.Int(doc["version"]) != 3 {
		return Document{}, errors.New("invalid v3 ops")
	}
	if p.Obj(doc["migration"]) != nil {
		return Document{}, errors.New("v3 migration is pending")
	}
	ref := p.Obj(doc["snapshotRef"])
	if ref == nil {
		return Document{}, errors.New("v3 snapshot reference missing")
	}
	files := []string{}
	if name := p.Str(ref["file"]); name != "" {
		if path.Base(name) != name || strings.ContainsAny(name, "\\\x00") {
			return Document{}, errors.New("invalid snapshot filename")
		}
		files = append(files, name)
	}
	files = append(files, "sync-state.json", "sync-state.json.bak")
	var snap p.Object
	var sf storage.File
	for _, name := range files {
		candidate, err := tx.Get("/super-productivity/" + name)
		if err != nil {
			continue
		}
		decoded, _, err := s.Codec.Decode(candidate.Body, s.RequireEncryption)
		if err != nil {
			continue
		}
		if p.Int(decoded["version"]) == 3 && p.Int(decoded["syncVersion"]) == p.Int(ref["syncVersion"]) && p.CompareClock(p.Obj(decoded["vectorClock"]), p.Obj(ref["vectorClock"])) == 0 {
			snap = decoded
			sf = candidate
			break
		}
	}
	if snap == nil {
		return Document{}, errors.New("no snapshot matches v3 reference")
	}
	state := p.Obj(snap["state"])
	if state == nil {
		return Document{}, errors.New("v3 state missing")
	}
	clock := p.Obj(snap["vectorClock"])
	replayClock := p.Clone(clock)
	seen := map[string]string{}
	for _, raw := range p.Arr(doc["recentOps"]) {
		op := p.Obj(raw)
		id := p.Str(op["id"])
		if id == "" {
			return Document{}, errors.New("invalid operation ID")
		}
		if prior, exists := seen[id]; exists {
			if prior != fingerprint(op) {
				return Document{}, errors.New("duplicate operation ID has different content")
			}
			continue
		}
		seen[id] = fingerprint(op)
		cmp := p.CompareClock(p.Obj(op["v"]), clock)
		if cmp == 0 || cmp == -1 {
			continue
		}
		if p.Int(op["s"]) != compat.SchemaVersion {
			return Document{}, errors.New("unsupported tail operation schema")
		}
		client := p.Str(op["c"])
		opClock := p.Obj(op["v"])
		if client == "" || p.Int(opClock[client]) != p.Int(replayClock[client])+1 {
			return Document{}, errors.New("v3 operation clock gap")
		}
		for device, value := range opClock {
			if device != client && p.Num(value) > p.Num(replayClock[device]) {
				return Document{}, errors.New("v3 causal dependency missing")
			}
		}
		payload := p.Obj(p.Obj(op["p"])["actionPayload"])
		if payload == nil {
			return Document{}, errors.New("unsupported operation payload")
		}
		replayContext := engine.NewContext(state, s.Now(), s.Location)
		replayContext.Now = p.Int(op["t"])
		if e = engine.NewBusinessDocument(snap).Apply(engine.Action{Code: p.Str(op["a"]), Payload: payload}, replayContext); e != nil {
			return Document{}, fmt.Errorf("v3 tail cannot be materialized: %w", e)
		}
		replayClock = p.MergeClock(replayClock, opClock)
	}
	if p.CompareClock(replayClock, p.Obj(doc["vectorClock"])) != 0 {
		return Document{}, errors.New("v3 replay did not reach file vector clock")
	}
	return Document{File: f, Envelope: doc, State: state, Format: format, Version: 3, Dependencies: []storage.File{sf}, Archives: archiveViews(snap)}, nil
}
func archiveViews(envelope p.Object) p.Object {
	out := p.Object{}
	for _, k := range []string{"archiveYoung", "archiveOld"} {
		if v, ok := envelope[k]; ok {
			out[k] = v
		}
	}
	return out
}
func (s *Service) Read(ctx context.Context) (d Document, e error) {
	e = s.Store.Transaction(ctx, func(tx *storage.Tx) error { d, e = s.load(tx); return e })
	return
}

type Mutation struct {
	RequestID    string           `json:"requestId"`
	BaseRevision int64            `json:"baseRevision"`
	Commands     []engine.Command `json:"commands"`
}

func fingerprint(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (s *Service) Execute(ctx context.Context, identity string, m Mutation) (p.Object, error) {
	var result p.Object
	e := s.Store.Transaction(ctx, func(tx *storage.Tx) error { var e error; result, e = s.execute(tx, identity, m); return e })
	return result, e
}
func (s *Service) execute(tx *storage.Tx, identity string, m Mutation) (p.Object, error) {
	return s.executeAt(tx, identity, m, s.Now())
}
func (s *Service) executeAt(tx *storage.Tx, identity string, m Mutation, now time.Time) (p.Object, error) {
	if !s.ExperimentalWrites {
		return nil, errors.New("write gate closed: full client compatibility release criteria are not yet met")
	}
	if m.RequestID == "" || len(m.RequestID) > 128 || m.BaseRevision <= 0 || len(m.Commands) == 0 || len(m.Commands) > 100 {
		return nil, errors.New("requestId, baseRevision and 1..100 commands required")
	}
	fp := fingerprint(m)
	key := identity + ":" + m.RequestID
	if result, e := tx.Command(key, fp); e == nil {
		return p.Parse(result)
	} else if e != storage.ErrNotFound {
		return nil, e
	}
	d, e := s.load(tx)
	if e != nil {
		return nil, e
	}
	if d.File.Revision != m.BaseRevision {
		return nil, storage.ErrPrecondition
	}
	if d.Version != 2 {
		return nil, errors.New("v3 writing awaits compaction and migration oracle tests")
	}
	if p.Int(d.Envelope["schemaVersion"]) != compat.SchemaVersion {
		return nil, errors.New("unsupported schema for writing")
	}
	client, e := tx.Setting("clientId")
	if e == storage.ErrNotFound {
		client = engine.NewID()
		if e = tx.SetSetting("clientId", client); e != nil {
			return nil, e
		}
	} else if e != nil {
		return nil, e
	}
	ctx := engine.NewContext(d.State, now, s.Location)
	clock := p.Clone(p.Obj(d.Envelope["vectorClock"]))
	if clock == nil {
		return nil, errors.New("vector clock missing")
	}
	sv := p.Int(d.Envelope["syncVersion"]) + 1
	ops := []any{}
	results := []any{}
	for _, command := range m.Commands {
		actions, e := engine.NewBusinessDocument(d.Envelope).Compile(command, ctx)
		if e != nil {
			return nil, e
		}
		for _, a := range actions {
			if e = engine.NewBusinessDocument(d.Envelope).ApplyLocal(a, ctx); e != nil {
				return nil, e
			}
			clock[client] = p.Int(clock[client]) + 1
			opID, e := uuid.NewV7()
			if e != nil {
				return nil, e
			}
			ids := a.IDs
			if len(ids) == 0 {
				ids = []string{a.ID}
			}
			id := a.ID
			if id == "" {
				id = ids[0]
			}
			op := engine.CompactOperation(a, ctx, opID.String(), client, clock)
			op["sv"] = sv
			ops = append(ops, op)
			results = append(results, p.Object{"entityId": id, "action": a.Code, "operationId": opID.String()})
		}
	}
	if len(ops) == 0 {
		result := p.Object{"status": "no_change", "deviceDelivery": "unknown", "revision": d.File.Revision, "etag": d.File.ETag, "syncVersion": d.Envelope["syncVersion"], "operations": []any{}}
		b, _ := json.Marshal(result)
		if e := tx.SaveCommand(key, fp, b); e != nil {
			return nil, e
		}
		return result, nil
	}
	env := d.Envelope
	env["state"] = d.State
	env["syncVersion"] = sv
	env["vectorClock"] = clock
	env["clientId"] = client
	env["lastModified"] = ctx.Now
	recent := append(p.Arr(env["recentOps"]), ops...)
	if len(recent) > compat.MaxRecentOps {
		recent = recent[len(recent)-compat.MaxRecentOps:]
	}
	env["recentOps"] = recent
	if len(recent) > 0 {
		first := p.Obj(recent[0])
		if v, ok := first["sv"]; ok {
			env["oldestOpSyncVersion"] = v
		} else {
			delete(env, "oldestOpSyncVersion")
		}
	}
	body, e := s.Codec.Encode(env, d.Format)
	if e != nil {
		return nil, e
	}
	if _, e = tx.Put(MainPath+".bak", d.File.Body, false, storage.Conditions{}); e != nil {
		return nil, e
	}
	file, e := tx.Put(MainPath, body, false, storage.Conditions{Revision: &m.BaseRevision})
	if e != nil {
		return nil, e
	}
	result := p.Object{"status": "committed_to_webdav", "deviceDelivery": "unknown", "revision": file.Revision, "etag": file.ETag, "syncVersion": sv, "operations": results}
	b, _ := json.Marshal(result)
	if e = tx.SaveCommand(key, fp, b); e != nil {
		return nil, e
	}
	return result, nil
}
func (s *Service) Capabilities(ctx context.Context) p.Object {
	out := p.Object{"upstreamCommit": compat.Commit, "schemaVersion": compat.SchemaVersion, "catalogActionCount": len(compat.Registry.Actions), "releaseReady": false, "writeEnabled": false, "v2": "snapshot read; scoped experimental writes", "v3": "reference read; unsupported tail actions fail closed; write disabled", "verifiedReducerFixtures": compat.Coverage.ReducerFixtureCount, "verifiedQueryFixtures": compat.Coverage.QueryFixtureCount, "verifiedOperationFixtures": compat.Coverage.OperationFixtureCount, "mcpAuthentication": "API key via Authorization: Bearer or X-API-Key; endpoint /mcp", "verifiedServiceCommandFixtures": compat.Coverage.CommandFixtureCount, "verifiedFullWorkflows": []string{}, "scopedCommands": []string{"task_move_project", "task_set_tags", "attachment_add", "attachment_update", "attachment_delete", "task_schedule", "task_reschedule", "task_unschedule", "task_dismiss_reminder", "task_set_deadline", "task_remove_deadline", "task_dismiss_deadline_reminder", "task_delete", "tasks_delete", "task_plan_today", "task_create:literal", "task_complete:non-recurring", "task_reopen:non-recurring", "note_create", "note_update", "note_delete", "note_move", "note_reorder", "task_update:supported-fields,literal-title-only,project-footprint,day-and-time-side-effects", "time_set_day:active-or-archived", "time_update_work_context", "project_create", "project_update:scoped", "project_archive", "project_unarchive", "project_reopen", "project_reorder", "tag_create:explicit-color", "tag_update:scoped", "tag_reorder"}, "pending": "Full workflow/operation/conflict/query parity and physical device convergence remain required."}
	out["mcpAuthentication"] = "Configured deployment authentication; ChatGPT cloud plugin uses OAuth at /mcp."
	out["businessJSON"] = p.Object{"mode": "native_state_replacement", "draftTTLMinutes": 30, "approvalTTLMinutes": 15, "maxDocumentBytes": maxBusinessBytes, "maxRequestBytes": 2 << 20, "models": Models, "validation": "pinned type schema, entity indexes and client relationships", "conflict": "notify user, reread latest and restart; no automatic merge", "backupStorage": "named encrypted history references", "backupGuidance": BackupGuidance, "physicalDeviceVerification": "pending"}
	out["scopedCommands"] = append(out["scopedCommands"].([]string), []string{"section_create", "section_update", "section_delete", "section_reorder", "section_add_task", "section_remove_task", "board_create", "board_update", "board_delete", "board_reorder", "board_set_panel_tasks", "board_duplicate", "metric_create", "metric_update", "metric_upsert", "metric_delete", "metric_log_focus", "menu_set_project_tree", "menu_set_tag_tree", "menu_rename_folder", "menu_delete_folder", "plugin_set_enabled", "plugin_delete_metadata", "task_nest", "task_promote", "task_move_child", "task_child_to_top", "task_child_to_bottom", "task_set_subtasks_display", "time_remove", "counter_create", "counter_update", "counter_delete", "counters_delete", "counter_reorder", "counter_set_today", "counter_set_date", "counter_increase_today", "counter_decrease_today"}...)
	out["verifiedCommandScenarios"] = compat.Coverage.VerifiedCommandScenarios
	out["scopedCommands"] = append(out["scopedCommands"].([]string), "task_move_up", "task_move_down", "task_move_to_top", "task_move_to_bottom")
	out["businessVerification"] = p.Object{"query": "Today scheduling selectors only", "command": "scoped original service/component scenarios passed", "state": "scoped reducer/meta-reducer cases passed", "nativeOperations": "scoped capture/compact encoding cases passed", "effects": "pending", "archives": "pending", "runtime": "pending", "conflictReplay": "pending", "physicalDevices": "pending"}
	out["target"] = "v2/schema v4 complete native client sync-data behavior; v3 writing/migration outside this phase"
	out["scopedCommands"] = append(out["scopedCommands"].([]string), "task_add_child:literal")
	out["scopedCommands"] = append(out["scopedCommands"].([]string), "task_move_in_context", "task_send_to_backlog", "task_send_to_regular")
	out["verifiedTaskDefaultsFixtures"], out["verifiedLocalEffectFixtures"] = compat.Coverage.TaskDefaultsFixtureCount, compat.Coverage.LocalEffectFixtureCount
	p.Obj(out["businessVerification"])["query"] = "Today scheduling and scoped work-context selectors passed; other selectors pending"
	p.Obj(out["businessVerification"])["effects"] = "onAllSubTasksDone, setDefaultEstimateIfNonGiven, reassertOwnTagsAfterConvert, moveTaskToUnDone and moveTaskToDone passed in scoped workflows; remaining effects pending"
	out["scopedCommands"] = append(out["scopedCommands"].([]string), "time_edit", "time_delete_day", "task_restore:original-or-today")
	out["verifiedArchiveDocumentFixtures"], out["verifiedArchiveReplayFixtures"] = compat.Coverage.ArchiveDocumentFixtureCount, compat.Coverage.ArchiveReplayFixtureCount
	p.Obj(out["businessVerification"])["archives"] = "scoped native restore and history time edits passed on complete documents and serialized remote replay across both archive partitions; other archive workflows pending"
	d, e := s.Read(ctx)
	if e != nil {
		out["dataError"] = e.Error()
		return out
	}
	out["revision"] = d.File.Revision
	out["etag"] = d.File.ETag
	out["fileVersion"] = d.Version
	out["syncVersion"] = d.Envelope["syncVersion"]
	out["writeEnabled"] = s.ExperimentalWrites && d.Version == 2 && p.Int(d.Envelope["schemaVersion"]) == 4
	return out
}
