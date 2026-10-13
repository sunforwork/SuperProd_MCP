package engine

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
	"regexp"
	"slices"
	"time"
)

type Command struct {
	Name string   `json:"name"`
	Args p.Object `json:"args"`
}

func NewID() string {
	b := make([]byte, 21)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)[:21]
}

var datePattern = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func nonnegativeNumber(v any) bool {
	switch v.(type) {
	case json.Number, float64, int, int64:
	default:
		return false
	}
	n := p.Num(v)
	return n >= 0 && math.Trunc(n) == n && !math.IsNaN(n) && !math.IsInf(n, 0) && n <= 9007199254740991
}
func validDate(s string) bool {
	if !datePattern.MatchString(s) {
		return false
	}
	_, e := time.Parse("2006-01-02", s)
	return e == nil
}

func Compile(s p.Object, cmd Command, c Context) ([]Action, error) {
	actions, err := compilePrimary(s, cmd, c)
	if err != nil {
		return nil, err
	}
	return localTaskFollowups(s, actions, c)
}

func compilePrimary(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	pl := p.Object{}
	a := Action{Kind: "UPD", ID: id, Payload: pl}
	switch cmd.Name {
	case "time_edit", "time_delete_day":
		return compileTimeEdit(s, cmd, c)
	case "task_move_in_context", "task_send_to_backlog", "task_send_to_regular":
		return compileBacklog(s, cmd, c)
	case "task_add_child":
		return compileAddChild(s, arg, c)
	case "task_move_up", "task_move_down", "task_move_to_top", "task_move_to_bottom":
		return compileContextMove(s, cmd, c)
	case "counter_create", "counter_update", "counter_delete", "counters_delete", "counter_reorder", "counter_set_today", "counter_set_date", "counter_increase_today", "counter_decrease_today":
		return compileCounter(s, cmd, c)
	case "task_nest", "task_promote", "task_move_child", "task_child_to_top", "task_child_to_bottom", "task_set_subtasks_display", "time_remove":
		return compileTaskCommands(s, cmd, c)
	case "section_create", "section_update", "section_delete", "section_reorder", "section_add_task", "section_remove_task", "board_create", "board_update", "board_delete", "board_reorder", "board_set_panel_tasks", "board_duplicate", "metric_create", "metric_update", "metric_upsert", "metric_delete", "metric_log_focus", "menu_set_project_tree", "menu_set_tag_tree", "menu_rename_folder", "menu_delete_folder", "plugin_set_enabled", "plugin_delete_metadata":
		return compileModels(s, cmd, c)
	case "task_move_project", "task_set_tags", "attachment_add", "attachment_update", "attachment_delete":
		return compileTaskContent(s, cmd, c)
	case "task_delete", "tasks_delete":
		return compileDelete(s, cmd)
	case "task_schedule", "task_reschedule", "task_unschedule", "task_dismiss_reminder", "task_set_deadline", "task_remove_deadline", "task_dismiss_deadline_reminder":
		return compileSchedule(s, cmd, c)
	case "task_plan_today":
		task, e := Entity(s, "task", id)
		if e != nil {
			return nil, e
		}
		parents := p.Object{}
		if parent, exists := task["parentId"]; exists {
			parents[id] = parent
		}
		a.Code = "HPT"
		a.Entity = "TASK"
		a.IDs = []string{id}
		a.Payload = p.Object{"taskIds": []any{id}, "today": c.Today, "startOfNextDayDiffMs": DayOffset(s).Milliseconds(), "parentTaskMap": parents}
	case "time_update_work_context":
		ct, ci := p.Str(arg["contextType"]), p.Str(arg["contextId"])
		model := "project"
		if ct == "TAG" {
			model = "tag"
		} else if ct != "PROJECT" {
			return nil, errors.New("PROJECT or TAG context required")
		}
		if _, e := Entity(s, model, ci); e != nil {
			return nil, e
		}
		day := p.Str(arg["date"])
		ch := p.Obj(arg["changes"])
		if !validDate(day) || len(ch) == 0 {
			return nil, errors.New("date and changes required")
		}
		for k, v := range ch {
			if !slices.Contains([]string{"s", "e", "b", "bt"}, k) || !nonnegativeNumber(v) {
				return nil, errors.New("work context data requires nonnegative integer s/e/b/bt values")
			}
		}
		a.Code = "KW"
		a.Entity = "TIME_TRACKING"
		a.ID = ct + ":" + ci + ":" + day
		a.Payload = p.Object{"ctx": p.Object{"type": ct, "id": ci}, "date": day, "updates": p.Clone(ch)}
	case "time_replace_days":
		task, e := Entity(s, "task", id)
		if e != nil {
			return nil, e
		}
		if len(p.Arr(task["subTaskIds"])) > 0 {
			return nil, errors.New("replace child task times; parent total is derived")
		}
		days := p.Obj(arg["days"])
		if days == nil {
			return nil, errors.New("daily time object required")
		}
		for day, v := range days {
			if !validDate(day) || !nonnegativeNumber(v) {
				return nil, errors.New("invalid daily time entry")
			}
		}
		a.Code = "HU"
		a.Entity = "TASK"
		a.Payload = p.Object{"task": p.Object{"id": id, "changes": p.Object{"timeSpentOnDay": p.Clone(days)}}}
	case "project_create", "project_update", "project_archive", "project_unarchive", "project_reopen", "project_reorder", "tag_create", "tag_update", "tag_reorder":
		return compileOrganization(s, cmd, c)
	case "task_create":
		return compileCreate(s, arg, c)
	case "task_complete", "task_reopen":
		return compileDone(s, arg, c, cmd.Name == "task_complete")
	case "task_update":
		task, e := Entity(s, "task", id)
		if e != nil {
			return nil, e
		}
		ch := p.Obj(arg["changes"])
		if len(ch) == 0 {
			return nil, errors.New("changes required")
		}
		if err := validateTaskEdits(ch); err != nil {
			return nil, err
		}
		if project, ok := ch["projectId"].(string); ok && !validTaskProject(s, task, project) {
			return nil, errors.New("invalid task project destination")
		}
		if _, changed := ch["isDone"]; changed && p.Str(task["repeatCfgId"]) != "" {
			return nil, fmt.Errorf("%w: recurring completion workflow", ErrUnsupported)
		}
		a.Code = "HU"
		a.Entity = "TASK"
		if _, ok := ch["title"]; ok && len(ch) == 1 {
			if p.Str(arg["titleMode"]) != "literal" {
				return nil, fmt.Errorf("%w: short syntax; use titleMode literal", ErrUnsupported)
			}
			pl["isIgnoreShortSyntax"] = true
		}
		pl["task"] = p.Object{"id": id, "changes": ch}
		if _, changed := ch["projectId"]; changed && p.Str(task["parentId"]) == "" {
			all := uniqueIDs(collectTaskIDs(s, []any{id}))
			pl["projectMoveSubTaskIds"] = remove(all, id)
			a.IDs = p.Strings(all)
		}
	case "time_set_day":
		return compileHistoryTimeEdit(s, arg)
	case "note_create":
		contextType, contextID := p.Str(arg["contextType"]), p.Str(arg["contextId"])
		if !slices.Contains([]string{"PROJECT", "TAG"}, contextType) || contextID == "" {
			return nil, errors.New("explicit PROJECT or TAG context required")
		}
		model := "project"
		if contextType == "TAG" {
			model = "tag"
		}
		if _, e := Entity(s, model, contextID); e != nil {
			return nil, e
		}
		id = c.ID()
		note := p.Object{"id": id, "projectId": nil, "isPinnedToToday": contextType == "TAG", "content": p.Str(arg["content"]), "created": c.Now, "modified": c.Now}
		if contextType == "PROJECT" {
			note["projectId"] = contextID
		}
		a.ID = id
		a.Code = "NA"
		a.Entity = "NOTE"
		a.Kind = "CRT"
		pl["note"] = note
		pl["isPreventFocus"] = false
	case "note_update":
		if _, e := Entity(s, "note", id); e != nil {
			return nil, e
		}
		ch := p.Obj(arg["changes"])
		if len(ch) == 0 {
			return nil, errors.New("changes required")
		}
		for k := range ch {
			if !slices.Contains([]string{"content", "imgUrl", "isLock", "backgroundColor", "isPinnedToToday"}, k) {
				return nil, fmt.Errorf("%w: note field %s", ErrUnsupported, k)
			}
			if k == "isLock" || k == "isPinnedToToday" {
				if _, ok := ch[k].(bool); !ok {
					return nil, errors.New("note boolean must be true or false")
				}
			} else if _, ok := ch[k].(string); !ok {
				return nil, errors.New("note text must be a string")
			}
		}
		a.Code = "NU"
		a.Entity = "NOTE"
		pl["note"] = p.Object{"id": id, "changes": ch}
	case "note_delete":
		note, e := Entity(s, "note", id)
		if e != nil {
			return nil, e
		}
		a.Code = "ND"
		a.Entity = "NOTE"
		a.Kind = "DEL"
		pl["id"] = id
		pl["projectId"] = note["projectId"]
		pl["isPinnedToToday"] = note["isPinnedToToday"]
		pl["note"] = p.Clone(note)
	case "note_move":
		note, e := Entity(s, "note", id)
		if e != nil {
			return nil, e
		}
		target := p.Str(arg["projectId"])
		if _, e = Entity(s, "project", target); e != nil {
			return nil, e
		}
		a.Code = "NM"
		a.Entity = "NOTE"
		pl["note"] = p.Clone(note)
		pl["targetProjectId"] = target
	case "note_reorder":
		a.Code = "NO"
		a.Entity = "NOTE"
		a.Kind = "MOV"
		a.IDs = p.Strings(arg["ids"])
		if len(a.IDs) == 0 {
			return nil, errors.New("ids required")
		}
		if !slices.Contains([]string{"PROJECT", "TAG"}, p.Str(arg["contextType"])) {
			return nil, errors.New("contextType required")
		}
		pl["ids"] = arg["ids"]
		pl["activeContextType"] = arg["contextType"]
		pl["activeContextId"] = arg["contextId"]
	default:
		return nil, fmt.Errorf("%w: command %s", ErrUnsupported, cmd.Name)
	}
	return []Action{a}, nil
}
