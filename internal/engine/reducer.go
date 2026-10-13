package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"time"
)

var ErrUnsupported = errors.New("behavior has not passed compatibility verification")

type Action struct {
	Code          string   `json:"code"`
	Entity        string   `json:"entity"`
	ID            string   `json:"id"`
	IDs           []string `json:"ids,omitempty"`
	Kind          string   `json:"kind"`
	Payload       p.Object `json:"payload"`
	archiveTarget string
}
type Context struct {
	Now           int64
	Today         string
	CalendarToday string
	Location      *time.Location
	IDSource      func() string
}

func (c Context) ID() string {
	if c.IDSource != nil {
		return c.IDSource()
	}
	return NewID()
}

func entities(s p.Object, model string) p.Object { return p.Obj(p.Obj(s[model])["entities"]) }
func Entity(s p.Object, model, id string) (p.Object, error) {
	v := p.Obj(entities(s, model)[id])
	if v == nil {
		return nil, fmt.Errorf("%s entity not found", model)
	}
	return v, nil
}
func remove(a []any, id string) []any {
	r := []any{}
	for _, v := range a {
		if v != id {
			r = append(r, v)
		}
	}
	return r
}
func has(a []any, id string) bool {
	for _, v := range a {
		if v == id {
			return true
		}
	}
	return false
}
func prepend(a []any, id string) []any { return append([]any{id}, a...) }
func reorder(old, ids []any) []any {
	r := []any{}
	for _, id := range old {
		if !has(ids, p.Str(id)) {
			r = append(r, id)
		}
	}
	for _, id := range ids {
		if has(old, p.Str(id)) {
			r = append(r, id)
		}
	}
	return r
}
func update(target, changes p.Object) {
	for k, v := range changes {
		target[k] = v
	}
}
func total(m p.Object) float64 {
	var r float64
	for _, v := range m {
		r += p.Num(v)
	}
	return r
}

func Apply(s p.Object, a Action, c Context) error {

	beforeToday := append([]any{}, p.Arr(p.Obj(entities(s, "tag")["TODAY"])["taskIds"])...)
	defer func() {
		afterToday := p.Arr(p.Obj(entities(s, "tag")["TODAY"])["taskIds"])
		removed := []any{}
		for _, id := range beforeToday {
			if !has(afterToday, p.Str(id)) {
				removed = append(removed, id)
			}
		}
		cleanupSections(s, collectTaskIDs(s, removed), func(section p.Object) bool { return section["contextType"] == "TAG" && section["contextId"] == "TODAY" })
	}()
	switch a.Code {
	case "HR":
		return applyRestoreTask(s, a, c)
	case "PRB", "PBR", "PAB", "PAR":
		return applyBacklogTransfer(s, a)
	case "WM", "WMU", "WMD", "WMT", "WMB", "PM", "PMU", "PMD", "PMT", "PMB":
		return applyContextMove(s, a)
	case "SA", "SU", "SUS", "SD", "SDM", "SUA", "SM", "ST", "SFD", "SC":
		return applyCounter(s, a)
	case "TA", "TMS", "TMU", "TMD", "TMT", "TMB", "HCS", "HC", "TR", "TRD":
		return applyTaskTree(s, a, c)
	case "S1", "S2", "S3", "S4", "S5", "S6":
		return applySection(s, a)
	case "BA", "BD", "BU", "BP", "BT", "BS":
		return applyBoard(s, a)
	case "EA", "ED", "EU", "EX", "EL":
		return applyMetric(s, a)
	case "UU", "UD", "UM", "UDM":
		return applyPlugin(s, a)
	case "MP", "MG", "MU", "MD":
		return applyMenu(s, a)
	case "HMP", "XA", "XU", "XD":
		return applyTaskContent(s, a)
	case "HD", "HDM":
		return applyDeleteTasks(s, a)
	case "HS", "HSR", "HSX", "HRX", "HDL", "HDT", "HXD", "HCR":
		return applySchedule(s, a, c)
	case "HPT":
		return applyPlanToday(s, a, c)
	case "PA", "PU", "PO", "PC", "PX", "PR", "PCO", "PRO", "PH", "PBA", "GA", "GU", "GO", "GC":
		return applyOrganization(s, a)
	case "KT", "KS", "KW":
		return replayTime(s, a)
	case "HA":
		return applyAddTask(s, a, c)
	case "NA", "NU", "ND", "NO", "NM":
		return applyNote(s, a)
	case "HU":
		return applyTaskUpdate(s, a, c)
	case "TUU":
		return applyTaskUI(s, a)

	default:
		return fmt.Errorf("%w: action %s", ErrUnsupported, a.Code)
	}
}
func applyNote(s p.Object, a Action) error {
	model := p.Obj(s["note"])
	if model == nil {
		return errors.New("note model missing")
	}
	ents := p.Obj(model["entities"])
	project := entities(s, "project")
	pl := a.Payload
	note := p.Obj(pl["note"])
	id := p.Str(note["id"])
	switch a.Code {
	case "NA":
		if pid := p.Str(note["projectId"]); pid != "" {
			proj := p.Obj(project[pid])
			if proj == nil {
				return errors.New("project missing")
			}
			proj["noteIds"] = prepend(p.Arr(proj["noteIds"]), id)
		}
		ents[id] = p.Clone(note)
		model["ids"] = prepend(p.Arr(model["ids"]), id)
		if p.Bool(note["isPinnedToToday"]) {
			model["todayOrder"] = prepend(p.Arr(model["todayOrder"]), id)
		}
	case "NU":
		ch := p.Obj(note["changes"])
		if pinned, ok := ch["isPinnedToToday"]; ok {
			order := p.Arr(model["todayOrder"])
			if p.Bool(pinned) {
				if !has(order, id) {
					order = prepend(order, id)
				}
			} else {
				order = remove(order, id)
			}
			model["todayOrder"] = order
		}
		if existing := p.Obj(ents[id]); existing != nil {
			update(existing, ch)
		}
	case "ND":
		id = p.Str(pl["id"])
		if pid := p.Str(pl["projectId"]); pid != "" {
			proj := p.Obj(project[pid])
			if proj == nil {
				return errors.New("project missing")
			}
			proj["noteIds"] = remove(p.Arr(proj["noteIds"]), id)
		}
		delete(ents, id)
		model["ids"] = remove(p.Arr(model["ids"]), id)
		model["todayOrder"] = remove(p.Arr(model["todayOrder"]), id)
	case "NO":
		if p.Str(pl["activeContextType"]) == "PROJECT" {
			if proj := p.Obj(project[p.Str(pl["activeContextId"])]); proj != nil {
				proj["noteIds"] = reorder(p.Arr(proj["noteIds"]), p.Arr(pl["ids"]))
			}
		} else {
			model["todayOrder"] = reorder(p.Arr(model["todayOrder"]), p.Arr(pl["ids"]))
		}
	case "NM":
		src, dst := p.Str(note["projectId"]), p.Str(pl["targetProjectId"])
		if src == dst {
			return errors.New("source and destination are equal")
		}
		if src != "" {
			proj := p.Obj(project[src])
			if proj == nil {
				return errors.New("source project missing")
			}
			proj["noteIds"] = remove(p.Arr(proj["noteIds"]), id)
		}
		if dst != "" {
			proj := p.Obj(project[dst])
			if proj == nil {
				return errors.New("target project missing")
			}
			proj["noteIds"] = append(p.Arr(proj["noteIds"]), id)
		}
		if existing := p.Obj(ents[id]); existing != nil {
			existing["projectId"] = dst
		}
	}
	return nil
}
