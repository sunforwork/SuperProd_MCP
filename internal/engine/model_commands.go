package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"strings"
)

func compileModels(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	a := Action{ID: id, Kind: "UPD", Payload: p.Object{}}
	if strings.HasPrefix(cmd.Name, "section_") {
		a.Entity = "SECTION"
		switch cmd.Name {
		case "section_create":
			ct, ci := p.Str(arg["contextType"]), p.Str(arg["contextId"])
			if ct != "PROJECT" && (ct != "TAG" || ci != "TODAY") {
				return nil, nil
			}
			title, ok := arg["title"].(string)
			if !ok {
				return nil, errors.New("title required")
			}
			a.ID = c.ID()
			a.Code = "S1"
			a.Kind = "CRT"
			a.Payload["section"] = p.Object{"id": a.ID, "contextId": ci, "contextType": ct, "title": sanitizedSectionTitle(title), "isExpanded": true, "taskIds": []any{}}
		case "section_update":
			a.Code = "S3"
			ch := p.Obj(arg["changes"])
			for k := range ch {
				if k != "title" && k != "isExpanded" {
					return nil, fmt.Errorf("section field %s requires a dedicated membership command", k)
				}
			}
			a.Payload["section"] = p.Object{"id": id, "changes": p.Clone(ch)}
		case "section_delete":
			a.Code = "S2"
			a.Kind = "DEL"
			a.Payload["id"] = id
		case "section_reorder":
			a.Code = "S4"
			a.Kind = "MOV"
			a.ID = ""
			a.IDs = p.Strings(arg["ids"])
			a.Payload = p.Object{"contextId": arg["contextId"], "ids": p.Clone(arg["ids"])}
		case "section_add_task":
			a.Code = "S5"
			a.Kind = "MOV"
			for _, key := range []string{"afterTaskId", "sourceSectionId"} {
				if _, ok := arg[key]; !ok {
					return nil, fmt.Errorf("%s required, use null for no anchor/source", key)
				}
			}
			a.ID = p.Str(arg["sectionId"])
			source := p.Str(arg["sourceSectionId"])
			if source != "" && source != a.ID {
				a.IDs = []string{source, a.ID}
				a.ID = source
			}
			a.Payload = p.Object{"sectionId": arg["sectionId"], "taskId": arg["taskId"], "afterTaskId": arg["afterTaskId"], "sourceSectionId": arg["sourceSectionId"]}
		case "section_remove_task":
			a.Code = "S6"
			a.ID = p.Str(arg["sectionId"])
			if _, ok := arg["workContextAfterTaskId"]; !ok {
				return nil, errors.New("workContextAfterTaskId required, use null for beginning")
			}
			a.Payload = p.Object{"sectionId": arg["sectionId"], "taskId": arg["taskId"], "workContextId": arg["contextId"], "workContextType": arg["contextType"], "workContextAfterTaskId": arg["workContextAfterTaskId"]}
		}
		return []Action{a}, nil
	}
	if strings.HasPrefix(cmd.Name, "board_") {
		a.Entity = "BOARD"
		switch cmd.Name {
		case "board_create":
			board := p.Clone(p.Obj(arg["board"]))
			if board == nil {
				return nil, errors.New("board required")
			}
			if p.Str(board["id"]) == "" {
				board["id"] = c.ID()
			}
			a.ID = p.Str(board["id"])
			a.Code = "BA"
			a.Kind = "CRT"
			a.Payload["board"] = board
		case "board_duplicate":
			var source p.Object
			for _, raw := range p.Arr(p.Obj(s["boards"])["boardCfgs"]) {
				if b := p.Obj(raw); b["id"] == id {
					source = b
					break
				}
			}
			if source == nil {
				return nil, errors.New("board not found")
			}
			a.ID = c.ID()
			panels := []any{}
			for _, raw := range p.Arr(source["panels"]) {
				panel := p.Clone(p.Obj(raw))
				panel["id"] = c.ID()
				panel["taskIds"] = []any{}
				panels = append(panels, panel)
			}
			a.Code = "BA"
			a.Kind = "CRT"
			a.Payload["board"] = p.Object{"id": a.ID, "title": p.Str(source["title"]) + " (copy)", "cols": source["cols"], "panels": panels}
		case "board_update":
			a.Code = "BU"
			a.Payload = p.Object{"id": id, "updates": p.Clone(arg["changes"])}
		case "board_delete":
			a.Code = "BD"
			a.Kind = "DEL"
			a.Payload["id"] = id
		case "board_reorder":
			a.Code = "BS"
			a.Kind = "MOV"
			a.ID = ""
			a.IDs = p.Strings(arg["ids"])
			a.Payload["ids"] = p.Clone(arg["ids"])
		case "board_set_panel_tasks":
			a.Code = "BT"
			a.ID = p.Str(arg["panelId"])
			a.Payload = p.Object{"panelId": arg["panelId"], "taskIds": p.Clone(arg["ids"])}
		}
		return []Action{a}, nil
	}
	if strings.HasPrefix(cmd.Name, "metric_") {
		a.Entity = "METRIC"
		if id == "" {
			id = c.Today
		}
		a.ID = id
		switch cmd.Name {
		case "metric_create", "metric_upsert":
			value := p.Clone(p.Obj(arg["metric"]))
			if value == nil {
				return nil, errors.New("metric required")
			}
			if p.Str(value["id"]) == "" {
				value["id"] = id
			}
			a.ID = p.Str(value["id"])
			a.Payload["metric"] = value
			a.Code = "EA"
			a.Kind = "CRT"
			if cmd.Name == "metric_upsert" {
				a.Code = "EX"
				a.Kind = "UPD"
			}
		case "metric_update":
			a.Code = "EU"
			a.Payload["metric"] = p.Object{"id": id, "changes": p.Clone(arg["changes"])}
		case "metric_delete":
			a.Code = "ED"
			a.Kind = "DEL"
			a.Payload["id"] = id
		case "metric_log_focus":
			if !nonnegativeNumber(arg["durationMs"]) {
				return nil, errors.New("durationMs must be a nonnegative integer")
			}
			if p.Int(arg["durationMs"]) < 1000 {
				return nil, nil
			}
			a.Code = "EL"
			a.Payload = p.Object{"day": id, "duration": arg["durationMs"]}
		}
		return []Action{a}, nil
	}
	if strings.HasPrefix(cmd.Name, "menu_") {
		a.Entity = "MENU_TREE"
		switch cmd.Name {
		case "menu_set_project_tree":
			a.Code = "MP"
			a.ID = "projectTree"
			a.Payload["tree"] = p.Clone(arg["tree"])
		case "menu_set_tag_tree":
			a.Code = "MG"
			a.ID = "tagTree"
			a.Payload["tree"] = p.Clone(arg["tree"])
		case "menu_rename_folder", "menu_delete_folder":
			if arg["treeType"] != "p" && arg["treeType"] != "t" {
				return nil, errors.New("treeType must be p or t")
			}
			a.Payload = p.Object{"folderId": id, "treeType": arg["treeType"]}
			a.Code = "MU"
			a.Payload["name"] = arg["title"]
			if cmd.Name == "menu_delete_folder" {
				a.Code = "MD"
				a.Kind = "DEL"
				delete(a.Payload, "name")
			}
		}
		return []Action{a}, nil
	}
	a.Entity = "PLUGIN_METADATA"
	switch cmd.Name {
	case "plugin_set_enabled":
		enabled, ok := arg["isEnabled"].(bool)
		if !ok {
			return nil, errors.New("isEnabled required")
		}
		a.Code = "UM"
		a.Payload["pluginMetadata"] = p.Object{"id": id, "isEnabled": enabled}
	case "plugin_delete_metadata":
		a.Code = "UDM"
		a.Kind = "DEL"
		a.Payload["pluginId"] = id
	default:
		return nil, ErrUnsupported
	}
	if id == "" {
		return nil, errors.New("plugin id required")
	}
	return []Action{a}, nil
}
