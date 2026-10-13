package engine

import (
	"errors"
	"fmt"
	"github.com/sunforwork/SuperProd_MCP/internal/compat"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"slices"
)

var clientDefaults = func() p.Object {
	d, e := p.Parse(compat.DefaultsJSON)
	if e != nil || p.Str(d["upstreamCommit"]) != compat.Commit {
		panic("invalid pinned defaults")
	}
	return p.Obj(d["defaults"])
}()

func defaultModel(name string) p.Object { return p.Clone(p.Obj(clientDefaults[name])) }
func inboxID() string                   { return p.Str(clientDefaults["inboxId"]) }

func applyOrganization(s p.Object, a Action) error {
	pl := a.Payload
	model := "project"
	if slices.Contains([]string{"GA", "GU", "GO", "GC"}, a.Code) {
		model = "tag"
	}
	m := p.Obj(s[model])
	ents := p.Obj(m["entities"])
	if ents == nil {
		return errors.New("organization model missing")
	}
	switch a.Code {
	case "PA", "GA":
		obj := p.Obj(pl[model])
		id := p.Str(obj["id"])
		if id == "" {
			return errors.New("entity ID missing")
		}
		if ents[id] == nil {
			ents[id] = p.Clone(obj)
			m["ids"] = append(p.Arr(m["ids"]), id)
		}
		if model == "tag" {
			menu := p.Obj(s["menuTree"])
			if menu == nil {
				return errors.New("menuTree missing")
			}
			tree := p.Arr(menu["tagTree"])
			if !treeContains(tree, "t", id) {
				menu["tagTree"] = append(tree, p.Object{"k": "t", "id": id})
			}
		}
	case "PU", "GU":
		u := p.Obj(pl[model])
		obj := p.Obj(ents[p.Str(u["id"])])
		if obj == nil {
			return nil
		}
		ch := p.Clone(p.Obj(u["changes"]))
		if _, ok := ch["id"]; ok {
			return fmt.Errorf("%w: entity ID mutation", ErrUnsupported)
		}
		if model == "tag" {
			if ids, ok := ch["taskIds"].([]any); ok {
				next := []any{}
				for _, id := range ids {
					if has(p.Arr(p.Obj(s["task"])["ids"]), p.Str(id)) {
						next = append(next, id)
					}
				}
				ch["taskIds"] = next
			}
		}
		update(obj, ch)
	case "PC", "GC":
		obj := p.Obj(ents[p.Str(pl[model+"Id"])])
		if obj == nil {
			return errors.New("organization entity missing")
		}
		cfg := p.Clone(p.Obj(obj["advancedCfg"]))
		if cfg == nil {
			cfg = p.Object{}
		}
		key := p.Str(pl["sectionKey"])
		section := p.Clone(p.Obj(cfg[key]))
		if section == nil {
			section = p.Object{}
		}
		update(section, p.Obj(pl["data"]))
		cfg[key] = section
		obj["advancedCfg"] = cfg
	case "GO":
		ids := p.Arr(pl["ids"])
		if len(ids) != len(p.Arr(m["ids"])) {
			return errors.New("Tag length should not change on re-order")
		}
		m["ids"] = p.Clone(ids)
	case "PO":
		existing := remove(p.Arr(m["ids"]), inboxID())
		ids := p.Arr(pl["ids"])
		next := remove(ids, inboxID())
		if len(next) != len(existing) {
			archived, unarchived, hidden, visible := []any{}, []any{}, []any{}, []any{}
			for _, raw := range existing {
				obj := p.Obj(ents[p.Str(raw)])
				if p.Bool(obj["isArchived"]) {
					archived = append(archived, raw)
				} else {
					unarchived = append(unarchived, raw)
					if !p.Bool(obj["isHiddenFromMenu"]) {
						visible = append(visible, raw)
					}
				}
				if p.Bool(obj["isHiddenFromMenu"]) {
					hidden = append(hidden, raw)
				}
			}
			first := ""
			if len(ids) > 0 {
				first = p.Str(ids[0])
			}
			switch {
			case len(next) > 0 && len(next) == len(visible) && has(visible, first):
				next = append(append(p.Clone(ids), archived...), hidden...)
			case len(next) > 0 && len(next) == len(unarchived) && has(unarchived, first):
				next = append(p.Clone(ids), archived...)
			case len(next) > 0 && len(next) == len(archived) && has(archived, first):
				next = append(unarchived, ids...)
			default:
				return errors.New("Invalid param given to UpdateProjectOrder")
			}
		}
		if ents[inboxID()] != nil {
			next = prepend(next, inboxID())
		}
		m["ids"] = next
	case "PX", "PR", "PCO", "PRO", "PH":
		id := p.Str(pl["id"])
		obj := p.Obj(ents[id])
		if obj == nil {
			return nil
		}
		switch a.Code {
		case "PX":
			if id != inboxID() {
				obj["isArchived"] = true
			}
		case "PCO":
			if id != inboxID() {
				update(obj, p.Object{"isArchived": true, "isDone": true, "doneOn": pl["doneOn"]})
			}
		case "PR", "PRO":
			update(obj, p.Object{"isArchived": false, "isDone": false, "doneOn": nil})
		case "PH":
			obj["isHiddenFromMenu"] = !p.Bool(obj["isHiddenFromMenu"])
		}
	case "PBA":
		obj := p.Obj(ents[p.Str(pl["projectId"])])
		if obj == nil {
			return errors.New("project missing")
		}
		obj["taskIds"] = append(p.Arr(obj["taskIds"]), p.Arr(obj["backlogTaskIds"])...)
		obj["backlogTaskIds"] = []any{}
	default:
		return ErrUnsupported
	}
	return nil
}
func treeContains(tree []any, kind, id string) bool {
	for _, raw := range tree {
		n := p.Obj(raw)
		if p.Str(n["k"]) == kind && p.Str(n["id"]) == id {
			return true
		}
		if p.Str(n["k"]) == "f" && treeContains(p.Arr(n["children"]), kind, id) {
			return true
		}
	}
	return false
}

func compileOrganization(s p.Object, cmd Command, c Context) ([]Action, error) {
	args := cmd.Args
	model := "project"
	entity := "PROJECT"
	if len(cmd.Name) >= 4 && cmd.Name[:4] == "tag_" {
		model = "tag"
		entity = "TAG"
	}
	id := p.Str(args["id"])
	a := Action{Entity: entity, ID: id, Kind: "UPD", Payload: p.Object{}}
	switch cmd.Name {
	case "project_create", "tag_create":
		title, ok := args["title"].(string)
		if !ok {
			return nil, errors.New("title required")
		}
		obj := defaultModel(model)
		id = c.ID()
		obj["id"] = id
		obj["title"] = title
		a.ID = id
		a.Kind = "CRT"
		a.Code = "PA"
		if model == "tag" {
			color := p.Str(args["color"])
			if color == "" {
				return nil, fmt.Errorf("%w: tag creation currently requires explicit color", ErrUnsupported)
			}
			obj["color"] = color
			obj["created"] = c.Now
			a.Code = "GA"
		}
		a.Payload[model] = obj
	case "project_update", "tag_update":
		if _, e := Entity(s, model, id); e != nil {
			return nil, e
		}
		ch := p.Obj(args["changes"])
		if len(ch) == 0 {
			return nil, errors.New("changes required")
		}
		for k, v := range ch {
			switch k {
			case "title":
				if _, ok := v.(string); !ok {
					return nil, errors.New("title must be a string")
				}
			case "icon", "color":
				if v != nil {
					if _, ok := v.(string); !ok {
						return nil, errors.New("icon/color must be a string or null")
					}
				}
				if k == "color" && model != "tag" {
					return nil, ErrUnsupported
				}
			case "isEnableBacklog", "isHiddenFromMenu":
				if model != "project" {
					return nil, ErrUnsupported
				}
				if _, ok := v.(bool); !ok {
					return nil, errors.New("boolean required")
				}
			default:
				return nil, fmt.Errorf("%w: organization field %s", ErrUnsupported, k)
			}
		}
		a.Code = "PU"
		if model == "tag" {
			a.Code = "GU"
		}
		a.Payload[model] = p.Object{"id": id, "changes": p.Clone(ch)}
		if value, ok := ch["isEnableBacklog"].(bool); ok && !value {
			return []Action{a, {Code: "PBA", Entity: "PROJECT", ID: id, Kind: "UPD", Payload: p.Object{"projectId": id}}}, nil
		}
	case "project_archive", "project_unarchive", "project_reopen":
		if _, e := Entity(s, model, id); e != nil {
			return nil, e
		}
		a.Code = map[string]string{"project_archive": "PX", "project_unarchive": "PR", "project_reopen": "PRO"}[cmd.Name]
		a.Payload["id"] = id
	case "project_reorder", "tag_reorder":
		ids, ok := args["ids"].([]any)
		if !ok || len(ids) == 0 {
			return nil, errors.New("nonempty ids required")
		}
		seen := map[string]bool{}
		for _, raw := range ids {
			id, ok := raw.(string)
			if !ok || seen[id] {
				return nil, errors.New("IDs must be unique strings")
			}
			if _, e := Entity(s, model, id); e != nil {
				return nil, e
			}
			seen[id] = true
		}
		a.IDs = p.Strings(ids)
		a.ID = a.IDs[0]
		a.Kind = "MOV"
		a.Code = "PO"
		if model == "tag" {
			a.Code = "GO"
		}
		a.Payload["ids"] = ids
	default:
		return nil, ErrUnsupported
	}
	return []Action{a}, nil
}
