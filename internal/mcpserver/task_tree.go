package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
)

type TaskNestInput struct {
	TaskIDInput
	TargetParentID string  `json:"targetParentId"`
	AfterTaskID    *string `json:"afterTaskId"`
}
type TaskPromoteInput struct {
	TaskIDInput
	ContextType string `json:"contextType"`
	ContextID   string `json:"contextId"`
}
type TaskDisplayInput struct {
	TaskIDInput
	Mode string `json:"mode"`
}

type TaskChildCreateInput struct {
	RequestID    string   `json:"requestId"`
	BaseRevision int64    `json:"baseRevision"`
	ParentID     string   `json:"parentId"`
	ContextType  string   `json:"contextType"`
	ContextID    string   `json:"contextId"`
	TitleMode    string   `json:"titleMode" jsonschema:"literal; short syntax workflows remain unavailable."`
	Additional   p.Object `json:"additional" jsonschema:"Original Task fields, including title. Omitted title creates an empty title. Unknown fields are preserved, inherited fields are normalized by the native child reducer."`
}

type TaskCreateInput struct {
	RequestID      string   `json:"requestId"`
	BaseRevision   int64    `json:"baseRevision"`
	Title          string   `json:"title" jsonschema:"Task title, including an empty string when creating a blank task."`
	TitleMode      string   `json:"titleMode" jsonschema:"literal; short syntax workflows remain unavailable."`
	ContextType    string   `json:"contextType"`
	ContextID      string   `json:"contextId"`
	IsAddToBacklog bool     `json:"isAddToBacklog,omitempty"`
	IsAddToBottom  bool     `json:"isAddToBottom,omitempty"`
	Additional     p.Object `json:"additional,omitempty" jsonschema:"Additional original Task fields, checked against the pinned native type. Unknown fields are preserved."`
}
type RemoveTimeInput struct {
	TaskIDInput
	Date       string `json:"date,omitempty"`
	DurationMs int64  `json:"durationMs"`
}

type EditTimeInput struct {
	TaskIDInput
	TimeEstimate float64  `json:"timeEstimate" jsonschema:"Original client estimate in milliseconds, submitted together with the complete daily records."`
	Days         p.Object `json:"days" jsonschema:"Complete original timeSpentOnDay object. Values are native numbers; zero and fractional values are retained."`
}

type SetTimeDayInput struct {
	TaskIDInput
	Date       string  `json:"date" jsonschema:"Explicit daily-record key from the original worklog. Other daily records are preserved."`
	DurationMs float64 `json:"durationMs" jsonschema:"Original numeric milliseconds, retaining zero and fractional values."`
}

type DeleteTimeDayInput struct {
	TaskIDInput
	Date string `json:"date" jsonschema:"Exact existing daily-record key to remove. Other keys remain unchanged."`
}

type RestoreTaskInput struct {
	TaskIDInput
	Placement string `json:"placement" jsonschema:"Required original or today. Today reproduces HistoryComponent restoration to the current logical day; original reproduces TaskService restoration without replanning."`
}

func addTaskTreeTools(server *mcp.Server, s *service.Service) {
	addCommandTool[SetTimeDayInput](server, s, "time_set_day", "Edit a daily worklog value via the original HistoryComponent/updateEverywhere flow. Chooses an active task first, then archiveYoung and archiveOld; preserves other date rows, applies native parent changes and emits the original full daily-map update.")
	addCommandTool[TaskCreateInput](server, s, "task_create", "Create a task via the native TaskService literal-title path, with explicit context, original type validation, date defaults, deadline planning and default-estimate follow-up.")
	addCommandTool[TaskChildCreateInput](server, s, "task_add_child", "Create a child via the client's literal-content bridge, preserving default values, first-child time inheritance, type validation and default-estimate follow-up. Requires explicit context and titleMode literal.")
	addCommandTool[TaskNestInput](server, s, "task_nest", "Convert an eligible main task into a child. Preserves its real tags, clears day planning, and updates project/section membership and parent totals.")
	addCommandTool[TaskNestInput](server, s, "task_move_child", "Move a child to another parent, or position it within the same parent, using an explicit nullable anchor.")
	addCommandTool[TaskPromoteInput](server, s, "task_promote", "Promote a child to a main task in an explicit client view context. Keeps its own tags or inherits parent tags, updates parent totals, and plans for Today only in TODAY context.")
	addCommandTool[TaskIDInput](server, s, "task_child_to_top", "Move a child to the beginning of its parent's subtask order.")
	addCommandTool[TaskIDInput](server, s, "task_child_to_bottom", "Move a child to the end of its parent's subtask order.")
	addCommandTool[TaskDisplayInput](server, s, "task_set_subtasks_display", "Set synchronized subtask display to show, hideDone or hideAll. Show uses the client's clearedFields wire representation.")
	addCommandTool[RemoveTimeInput](server, s, "time_remove", "Subtract milliseconds from a task's logical day, clamping at zero and applying the original parent time delta. Omitted date uses the server client's logical Today.")
	addCommandTool[EditTimeInput](server, s, "time_edit", "Submit the native time-estimate dialog: estimate and complete daily records in one task update. Preserves fractional values and applies original parent deltas and remaining-estimate rules.")
	addCommandTool[DeleteTimeDayInput](server, s, "time_delete_day", "Delete a daily-record row and submit the native time-estimate dialog, preserving the current estimate and all remaining daily records. Produces the client's full-field update rather than an invented time delta.")
	addCommandTool[RestoreTaskInput](server, s, "task_restore", "Restore an archived task with its archived children, choosing original or today placement. Reads both archive partitions using native precedence, normalizes stale references, adopts eligible active children, and applies the native archive-removal rules in the existing transaction.")
}
