package engine

import (
	"errors"
	"fmt"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
	"slices"
)

func applyCounter(s p.Object, a Action) error {
	pl := a.Payload
	model := p.Obj(s["simpleCounter"])
	if model == nil {
		return errors.New("simpleCounter model missing")
	}
	switch a.Code {
	case "SA":
		return entityAdd(s, "simpleCounter", p.Obj(pl["simpleCounter"]), false)
	case "SUS":
		return entityAdd(s, "simpleCounter", p.Obj(pl["simpleCounter"]), true)
	case "SU":
		u := p.Obj(pl["simpleCounter"])
		return entityUpdate(s, "simpleCounter", u)
	case "SD":
		entityRemove(s, "simpleCounter", p.Str(pl["id"]))
	case "SDM":
		for _, id := range p.Strings(pl["ids"]) {
			entityRemove(s, "simpleCounter", id)
		}
	case "SUA":
		ids := []any{}
		for _, raw := range p.Arr(pl["items"]) {
			ids = addUnique(ids, p.Str(p.Obj(raw)["id"]), true)
		}
		for _, id := range p.Arr(model["ids"]) {
			if !has(ids, p.Str(id)) {
				entityRemove(s, "simpleCounter", p.Str(id))
			}
		}
		for _, raw := range p.Arr(pl["items"]) {
			if err := entityAdd(s, "simpleCounter", p.Obj(raw), true); err != nil {
				return err
			}
		}
		model["ids"] = ids
	case "SM":
		valid := []any{}
		for _, id := range p.Arr(pl["ids"]) {
			if has(p.Arr(model["ids"]), p.Str(id)) {
				valid = append(valid, id)
			}
		}
		if len(valid) == 0 {
			return nil
		}
		next := []any{}
		index := 0
		for _, id := range p.Arr(model["ids"]) {
			if has(valid, p.Str(id)) {
				next = append(next, valid[index])
				index++
			} else {
				next = append(next, id)
			}
		}
		model["ids"] = next
	case "ST", "SFD", "SC":
		counter := p.Obj(p.Obj(model["entities"])[p.Str(pl["id"])])
		if counter == nil {
			return nil
		}
		day := p.Str(pl["date"])
		if a.Code == "ST" {
			day = p.Str(pl["today"])
		}
		days := p.Clone(p.Obj(counter["countOnDay"]))
		if days == nil {
			days = p.Object{}
		}
		if a.Code == "SC" {
			days[day] = p.Num(days[day]) + p.Num(pl["duration"])
		} else {
			days[day] = math.Max(0, p.Num(pl["newVal"]))
		}
		counter["countOnDay"] = days
	}
	return nil
}

func validateCounterFields(fields p.Object) error {
	for k, v := range fields {
		switch k {
		case "id", "title", "type":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s must be string", k)
			}
			if k == "type" && !slices.Contains([]string{"StopWatch", "ClickCounter", "RepeatedCountdownReminder"}, p.Str(v)) {
				return errors.New("invalid counter type")
			}
		case "icon":
			if v != nil {
				if _, ok := v.(string); !ok {
					return errors.New("icon must be string or null")
				}
			}
		case "isEnabled", "isHideButton", "isTrackStreaks", "isOn":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s must be boolean", k)
			}
		case "streakMinValue", "streakWeeklyFrequency", "countdownDuration":
			if !nonnegativeNumber(v) {
				return fmt.Errorf("%s must be nonnegative number", k)
			}
		case "streakMode":
			if v != "specific-days" && v != "weekly-frequency" {
				return errors.New("invalid streak mode")
			}
		case "streakWeekDays":
			if p.Obj(v) == nil {
				return errors.New("streakWeekDays object required")
			}
		case "countOnDay":
			if p.Obj(v) == nil {
				return errors.New("countOnDay object required")
			}
			for day, n := range p.Obj(v) {
				if !validDate(day) || !nonnegativeNumber(n) {
					return errors.New("invalid counter day")
				}
			}
		default:
			return fmt.Errorf("%w: counter field %s", ErrUnsupported, k)
		}
	}
	return nil
}

func compileCounter(s p.Object, cmd Command, c Context) ([]Action, error) {
	arg := cmd.Args
	id := p.Str(arg["id"])
	a := Action{Entity: "SIMPLE_COUNTER", ID: id, Kind: "UPD", Payload: p.Object{}}
	switch cmd.Name {
	case "counter_create":
		fields := p.Clone(p.Obj(arg["counter"]))
		if fields == nil {
			return nil, errors.New("counter required")
		}
		if err := validateCounterFields(fields); err != nil {
			return nil, err
		}
		fields["id"] = c.ID()
		a.ID = p.Str(fields["id"])
		a.Code = "SA"
		a.Kind = "CRT"
		a.Payload["simpleCounter"] = fields
	case "counter_update":
		changes := p.Obj(arg["changes"])
		if len(changes) == 0 {
			return nil, errors.New("changes required")
		}
		if _, ok := changes["id"]; ok {
			return nil, errors.New("counter id is immutable")
		}
		if err := validateCounterFields(changes); err != nil {
			return nil, err
		}
		a.Code = "SU"
		a.Payload["simpleCounter"] = p.Object{"id": id, "changes": p.Clone(changes)}
	case "counter_delete":
		a.Code = "SD"
		a.Kind = "DEL"
		a.Payload["id"] = id
	case "counters_delete":
		a.Code = "SDM"
		a.Kind = "DEL"
		a.ID = ""
		a.IDs = p.Strings(arg["ids"])
		a.Payload["ids"] = arg["ids"]
	case "counter_reorder":
		a.Code = "SM"
		a.Kind = "MOV"
		a.ID = ""
		a.IDs = p.Strings(arg["ids"])
		a.Payload["ids"] = arg["ids"]
	case "counter_set_today", "counter_set_date", "counter_increase_today", "counter_decrease_today":
		value := arg["value"]
		if !isNumber(value) {
			return nil, errors.New("numeric value required")
		}
		day := c.Today
		if cmd.Name == "counter_set_date" {
			day = p.Str(arg["date"])
			if !validDate(day) {
				return nil, errors.New("valid date required")
			}
		}
		if cmd.Name == "counter_increase_today" || cmd.Name == "counter_decrease_today" {
			counter := p.Obj(entities(s, "simpleCounter")[id])
			if counter == nil {
				return []Action{}, nil
			}
			old := p.Num(p.Obj(counter["countOnDay"])[day])
			if cmd.Name == "counter_increase_today" {
				value = old + p.Num(value)
			} else {
				value = math.Max(0, old-p.Num(value))
			}
		}
		a.Code = "ST"
		a.Payload = p.Object{"id": id, "newVal": value, "today": day}
		if cmd.Name == "counter_set_date" {
			a.Code = "SFD"
			delete(a.Payload, "today")
			a.Payload["date"] = day
		}
	default:
		return nil, ErrUnsupported
	}
	return []Action{a}, nil
}
