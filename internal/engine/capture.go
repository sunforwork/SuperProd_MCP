package engine

import (
	"github.com/sunforwork/SuperProd_MCP/internal/compat"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
)

func CompactOperation(a Action, c Context, id, client string, clock p.Object) p.Object {
	ids := a.IDs
	if len(ids) == 0 && a.ID != "" {
		ids = []string{a.ID}
	}
	entityID := a.ID
	if entityID == "" && len(ids) > 0 {
		entityID = ids[0]
	}
	op := p.Object{"id": id, "a": a.Code, "o": a.Kind, "e": a.Entity, "p": CapturePayload(a, c), "c": client, "v": p.Clone(clock), "t": c.Now, "s": compat.SchemaVersion}
	if entityID != "" {
		op["d"] = entityID
	}
	if len(ids) > 0 {
		op["ds"] = ids
	}
	return op
}

func CapturePayload(a Action, c Context) p.Object {
	raw := p.Clone(a.Payload)
	delete(raw, "meta")
	delete(raw, "type")
	changes := []any{}
	if a.Entity == "TIME_TRACKING" {
		entry := p.Object{}
		if a.Code == "KS" {
			for _, k := range []string{"contextType", "contextId", "date", "data"} {
				if value, exists := raw[k]; exists {
					entry[k] = value
				}
			}
		} else if a.Code == "KW" {
			for _, k := range []string{"ctx", "date", "updates"} {
				if value, exists := raw[k]; exists {
					entry[k] = value
				}
			}
		}
		if len(entry) > 0 {
			changes = append(changes, p.Object{"entityType": "TIME_TRACKING", "entityId": a.ID, "opType": "UPD", "changes": entry})
		}
	}
	if a.Code == "KT" && a.Entity == "TASK" {
		entry := p.Object{}
		for _, key := range []string{"taskId", "date", "duration"} {
			if value, exists := raw[key]; exists {
				entry[key] = value
			}
		}
		change := p.Object{"entityType": "TASK", "opType": "UPD", "changes": entry}
		if value, exists := raw["taskId"]; exists {
			change["entityId"] = value
		}
		changes = append(changes, change)
	}
	return p.Object{"actionPayload": raw, "entityChanges": changes}
}
