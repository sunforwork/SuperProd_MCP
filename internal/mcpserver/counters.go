package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
)

type CounterCreateInput struct {
	RequestID    string   `json:"requestId"`
	BaseRevision int64    `json:"baseRevision"`
	Counter      p.Object `json:"counter"`
}
type CounterEditInput struct {
	TaskIDInput
	Changes p.Object `json:"changes"`
}
type CounterValueInput struct {
	TaskIDInput
	Value float64 `json:"value"`
}
type CounterDateInput struct {
	CounterValueInput
	Date string `json:"date"`
}

func addCounterTools(server *mcp.Server, s *service.Service) {
	addCommandTool[CounterCreateInput](server, s, "counter_create", "Create a client counter from complete configuration/data, generating a fresh ID as the client service does.")
	addCommandTool[CounterEditInput](server, s, "counter_update", "Edit client counter configuration, streak settings, or daily values.")
	addCommandTool[TaskIDInput](server, s, "counter_delete", "Delete a synchronized counter.")
	addCommandTool[TaskIDsInput](server, s, "counters_delete", "Delete multiple synchronized counters in one native operation.")
	addCommandTool[TaskIDsInput](server, s, "counter_reorder", "Reorder selected counters while preserving the absolute positions of counters absent from the order.")
	addCommandTool[CounterValueInput](server, s, "counter_set_today", "Set the logical Today's counter value, clamping negative values to zero.")
	addCommandTool[CounterDateInput](server, s, "counter_set_date", "Set a counter value for an explicit date using the client's date-specific action.")
	addCommandTool[CounterValueInput](server, s, "counter_increase_today", "Increase the logical Today's counter and publish the client service's absolute-value operation.")
	addCommandTool[CounterValueInput](server, s, "counter_decrease_today", "Decrease the logical Today's counter, clamp at zero, and publish an absolute-value operation.")
}
