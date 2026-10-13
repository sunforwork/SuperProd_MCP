package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"sort"
	"strconv"
	"time"
)

func TodaySchedule(s p.Object, now time.Time, location *time.Location) (p.Object, error) {
	ctx := NewContext(s, now, location)
	model := p.Obj(s["task"])
	tasks := entities(s, "task")
	projects := entities(s, "project")
	ids := []string{}
	for _, id := range p.Strings(model["ids"]) {
		task := p.Obj(tasks[id])
		if task == nil {
			continue
		}
		project := p.Obj(projects[p.Str(task["projectId"])])
		if p.Bool(project["isArchived"]) {
			continue
		}
		ids = append(ids, id)
	}

	index := func(id string) (uint64, bool) {
		n, e := strconv.ParseUint(id, 10, 32)
		return n, e == nil && n < 4294967295 && strconv.FormatUint(n, 10) == id
	}
	sort.SliceStable(ids, func(i, j int) bool {
		a, ai := index(ids[i])
		b, bi := index(ids[j])
		if ai != bi {
			return ai
		}
		return ai && a < b
	})
	members := map[string]bool{}
	for _, id := range ids {
		task := p.Obj(tasks[id])
		timestamp := p.Int(task["dueWithTime"])
		if timestamp != 0 {
			if timestamp < 0 || timestamp > 8640000000000000 {
				return nil, errors.New("invalid dueWithTime")
			}
			if time.UnixMilli(timestamp).Add(-DayOffset(s)).In(location).Format("2006-01-02") == ctx.Today {
				members[id] = true
			}
		} else if p.Str(task["dueDay"]) == ctx.Today {
			members[id] = true
		}
	}
	top := []string{}
	for _, id := range ids {
		if members[id] && !members[p.Str(p.Obj(tasks[id])["parentId"])] {
			top = append(top, id)
		}
	}
	tag := p.Obj(entities(s, "tag")["TODAY"])
	stored := p.Strings(tag["taskIds"])
	positions := map[string]int{}
	for i, id := range stored {
		positions[id] = i
	}
	ordered := []string{}
	unordered := []string{}
	for _, id := range top {
		if _, exists := positions[id]; exists {
			ordered = append(ordered, id)
		} else {
			unordered = append(unordered, id)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return positions[ordered[i]] < positions[ordered[j]] })
	ordered = append(ordered, unordered...)
	undone := []string{}
	for _, id := range ordered {
		if value, ok := p.Obj(tasks[id])["isDone"].(bool); ok && !value {
			undone = append(undone, id)
		}
	}
	return p.Object{"today": ctx.Today, "taskIds": p.Values(ordered), "undoneTaskIds": p.Values(undone), "progress": p.Object{"done": len(ordered) - len(undone), "total": len(ordered)}}, nil
}
