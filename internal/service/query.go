package service

import (
	"context"
	"encoding/json"
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"strings"
)

var Models = []string{"task", "project", "tag", "globalConfig", "note", "issueProvider", "planner", "boards", "metric", "simpleCounter", "taskRepeatCfg", "menuTree", "timeTracking", "pluginUserData", "pluginMetadata", "reminders", "section", "archiveYoung", "archiveOld"}

func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		r := p.Object{}
		for k, v := range x {
			key := strings.ToLower(k)
			if strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "apikey") || strings.Contains(key, "api_key") || key == "authorization" || key == "encryptkey" || key == "encryptionkey" {
				r[k] = "[REDACTED]"
			} else {
				r[k] = redact(v)
			}
		}
		return r
	case []any:
		r := make([]any, len(x))
		for i, v := range x {
			r[i] = redact(v)
		}
		return r
	default:
		return v
	}
}

type InspectInput struct {
	Model  string `json:"model"`
	ID     string `json:"id,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

func (s *Service) Inspect(ctx context.Context, in InspectInput) (p.Object, error) {
	valid := false
	for _, m := range Models {
		if m == in.Model {
			valid = true
		}
	}
	if !valid {
		return nil, errors.New("unsupported model")
	}
	d, e := s.Read(ctx)
	if e != nil {
		return nil, e
	}
	v, exists := d.State[in.Model]
	if in.Model == "archiveYoung" || in.Model == "archiveOld" {
		if top, ok := d.Archives[in.Model]; ok {
			v = top
			exists = true
		}
	}
	if !exists {
		v = d.Envelope[in.Model]
	}
	if v == nil {
		return nil, errors.New("model unavailable")
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	if in.Limit < 1 || in.Limit > 500 || in.Offset < 0 {
		return nil, errors.New("invalid pagination")
	}
	out := p.Object{"revision": d.File.Revision, "model": in.Model, "semantics": "raw persisted data; not a client UI selector", "fieldReference": "resource://super-productivity/models"}
	if model := p.Obj(v); p.Obj(model["entities"]) != nil {
		ents := p.Obj(model["entities"])
		if in.ID != "" {
			item, ok := ents[in.ID]
			if !ok {
				return nil, errors.New("entity not found")
			}
			out["data"] = redact(item)
		} else {
			ids := p.Arr(model["ids"])
			start := min(in.Offset, len(ids))
			end := min(start+in.Limit, len(ids))
			items := []any{}
			for _, raw := range ids[start:end] {
				items = append(items, redact(ents[p.Str(raw)]))
			}
			out["data"] = items
			out["total"] = len(ids)
			if end < len(ids) {
				out["nextOffset"] = end
			}
		}
	} else if list, ok := v.([]any); ok {
		start := min(in.Offset, len(list))
		end := min(start+in.Limit, len(list))
		out["data"] = redact(list[start:end])
		out["total"] = len(list)
		if end < len(list) {
			out["nextOffset"] = end
		}
	} else {
		out["data"] = redact(v)
	}
	b, _ := json.Marshal(out)
	if len(b) > 1<<20 {
		return nil, errors.New("result exceeds 1 MiB; request an entity or smaller page")
	}
	return out, nil
}
func (s *Service) TimeAnomalies(ctx context.Context) (p.Object, error) {
	d, e := s.Read(ctx)
	if e != nil {
		return nil, e
	}
	items := []any{}
	for id, raw := range p.Obj(p.Obj(d.State["task"])["entities"]) {
		task := p.Obj(raw)
		var total int64
		for date, v := range p.Obj(task["timeSpentOnDay"]) {
			n := p.Int(v)
			total += n
			if n < 0 || n > 86400000 {
				items = append(items, p.Object{"taskId": id, "date": date, "durationMs": n, "kind": "negative_or_over_24h", "parentAggregate": len(p.Arr(task["subTaskIds"])) > 0})
			}
		}
		if total != p.Int(task["timeSpent"]) {
			items = append(items, p.Object{"taskId": id, "kind": "total_mismatch", "stored": task["timeSpent"], "computed": total})
		}
	}
	return p.Object{"revision": d.File.Revision, "anomalies": items, "interpretation": "Candidates only. Daily aggregates cannot determine true start/end times. Parent aggregates may legitimately exceed 24h; no automatic corrections."}, nil
}
