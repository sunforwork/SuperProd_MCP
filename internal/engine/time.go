package engine

import (
	"errors"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
)

func replayTime(s p.Object, a Action) error {
	pl := a.Payload
	if a.Code == "KT" {
		id := p.Str(pl["taskId"])
		task, e := Entity(s, "task", id)
		if e != nil {
			return nil
		}
		duration := pl["duration"]
		if !isNumber(duration) || math.IsNaN(p.Num(duration)) || math.IsInf(p.Num(duration), 0) {

			return nil
		}
		old := p.Obj(task["timeSpentOnDay"])
		days := p.Clone(old)
		if days == nil {
			days = p.Object{}
		}
		day := p.Str(pl["date"])
		delta := p.Num(duration)
		days[day] = p.Num(days[day]) + delta
		task["timeSpentOnDay"] = days
		task["timeSpent"] = total(days)
		if parent := p.Obj(entities(s, "task")[p.Str(task["parentId"])]); parent != nil {
			pd := p.Clone(p.Obj(parent["timeSpentOnDay"]))
			if pd == nil {
				pd = p.Object{}
			}
			if delta != 0 {
				v := p.Num(pd[day]) + delta
				if v > 0 {
					pd[day] = v
				} else {
					delete(pd, day)
				}
			}
			parent["timeSpentOnDay"] = pd
			parent["timeSpent"] = p.Num(parent["timeSpent"]) + delta
		}
		return nil
	}
	model := p.Obj(s["timeTracking"])
	if model == nil {
		return errors.New("timeTracking model missing")
	}
	ct, ci, day := p.Str(pl["contextType"]), p.Str(pl["contextId"]), p.Str(pl["date"])
	if a.Code == "KW" {
		ct = p.Str(p.Obj(pl["ctx"])["type"])
		ci = p.Str(p.Obj(pl["ctx"])["id"])
	}
	prop := "project"
	if ct == "TAG" {
		prop = "tag"
	}
	group := p.Obj(model[prop])
	if group == nil {
		group = p.Object{}
		model[prop] = group
	}
	dates := p.Obj(group[ci])
	if dates == nil {
		dates = p.Object{}
		group[ci] = dates
	}
	if a.Code == "KS" {
		dates[day] = p.Clone(pl["data"])
	} else {
		value := p.Obj(dates[day])
		if value == nil {
			value = p.Object{}
			dates[day] = value
		}
		update(value, p.Obj(pl["updates"]))
	}
	return nil
}
