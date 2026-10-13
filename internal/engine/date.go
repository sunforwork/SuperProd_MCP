package engine

import (
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"math"
	"regexp"
	"strconv"
	"time"
)

var boundary = regexp.MustCompile(`^([01]?\d|2[0-3]):([0-5]\d)$`)

func DayOffset(state p.Object) time.Duration {
	misc := p.Obj(p.Obj(state["globalConfig"])["misc"])
	if s, ok := misc["startOfNextDayTime"].(string); ok {
		m := boundary.FindStringSubmatch(s)
		if m == nil {
			return 0
		}
		h, _ := strconv.Atoi(m[1])
		minutes, _ := strconv.Atoi(m[2])
		return time.Duration(h*60+minutes) * time.Minute
	}
	n := p.Num(misc["startOfNextDay"])
	if n < 0 || n > 23 || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return time.Duration(math.Floor(n)) * time.Hour
}
func NewContext(state p.Object, now time.Time, location *time.Location) Context {
	return Context{Now: now.UnixMilli(), Today: now.Add(-DayOffset(state)).In(location).Format("2006-01-02"), CalendarToday: now.In(location).Format("2006-01-02"), Location: location}
}
