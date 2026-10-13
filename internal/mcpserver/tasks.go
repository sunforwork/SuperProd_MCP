package mcpserver

import (
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sunforwork/SuperProd_MCP/internal/engine"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
)

type TaskIDInput struct {
	RequestID    string `json:"requestId" jsonschema:"Unique idempotency ID; reuse only when retrying the same request."`
	BaseRevision int64  `json:"baseRevision" jsonschema:"Revision from the most recent read."`
	ID           string `json:"id" jsonschema:"Task ID."`
}
type ScheduleInput struct {
	TaskIDInput
	DueWithTime     int64  `json:"dueWithTime" jsonschema:"Positive Unix milliseconds for the scheduled instant."`
	Reminder        string `json:"reminder" jsonschema:"DoNotRemind, AtStart, m5, m10, m15, m30 or h1."`
	IsMoveToBacklog bool   `json:"isMoveToBacklog,omitempty"`
}
type UnscheduleInput struct {
	TaskIDInput
	IsLeaveInToday bool `json:"isLeaveInToday,omitempty"`
}
type DeadlineInput struct {
	TaskIDInput
	Date             string `json:"date,omitempty" jsonschema:"Calendar date YYYY-MM-DD, mutually exclusive with deadlineWithTime."`
	DeadlineWithTime *int64 `json:"deadlineWithTime,omitempty" jsonschema:"Positive Unix milliseconds, mutually exclusive with date."`
	Reminder         string `json:"reminder,omitempty" jsonschema:"Required for timed deadlines: DoNotRemind, AtStart, m5, m10, m15, m30 or h1."`
}
type TaskIDsInput struct {
	RequestID    string   `json:"requestId"`
	BaseRevision int64    `json:"baseRevision"`
	IDs          []string `json:"ids" jsonschema:"Task IDs; parent deletion cascades to its children."`
}
type TaskProjectInput struct {
	TaskIDInput
	ProjectID string `json:"projectId"`
}
type TaskTagsInput struct {
	TaskIDInput
	TagIDs []string `json:"tagIds" jsonschema:"Complete tag list; empty array clears tags. TODAY is virtual, use scheduling tools."`
}
type AttachmentAddInput struct {
	TaskIDInput
	Attachment p.Object `json:"attachment" jsonschema:"Metadata: type FILE/LINK/IMG/COMMAND/NOTE and path required; title/icon/originalImgPath optional. Stored as content, never executed."`
}
type AttachmentEditInput struct {
	TaskIDInput
	AttachmentID string   `json:"attachmentId"`
	Changes      p.Object `json:"changes"`
}
type AttachmentDeleteInput struct {
	TaskIDInput
	AttachmentID string `json:"attachmentId"`
}

func addCommandTool[T any](server *mcp.Server, s *service.Service, name, desc string) {
	destructive := name != "attachment_add" && name != "task_create" && name != "task_add_child"
	mcp.AddTool(server, tool(name, desc+" Requires requestId and baseRevision; no custom approval.", false, destructive), func(ctx context.Context, r *mcp.CallToolRequest, in T) (*mcp.CallToolResult, p.Object, error) {
		b, e := json.Marshal(in)
		if e != nil {
			return result(nil, e)
		}
		args, e := p.Parse(b)
		if e != nil {
			return result(nil, e)
		}
		requestID, revision := p.Str(args["requestId"]), p.Int(args["baseRevision"])
		delete(args, "requestId")
		delete(args, "baseRevision")
		v, e := s.Execute(ctx, identity(ctx), service.Mutation{RequestID: requestID, BaseRevision: revision, Commands: []engine.Command{{Name: name, Args: args}}})
		return result(v, e)
	})
}
func addTaskLifecycleTools(server *mcp.Server, s *service.Service) {
	addCommandTool[TaskProjectInput](server, s, "task_move_project", "Move a parent task and children to another project, preserving original list and section semantics.")
	addCommandTool[TaskTagsInput](server, s, "task_set_tags", "Replace task tag membership, deduplicating tags and updating tag task lists.")
	addCommandTool[AttachmentAddInput](server, s, "attachment_add", "Add task attachment metadata with a new client-compatible ID. Does not upload files, fetch URLs or execute commands.")
	addCommandTool[AttachmentEditInput](server, s, "attachment_update", "Edit stored task attachment metadata and preserve unknown fields.")
	addCommandTool[AttachmentDeleteInput](server, s, "attachment_delete", "Delete stored attachment references; does not delete the referenced external file.")
	addCommandTool[ScheduleInput](server, s, "task_schedule", "Set a scheduled instant and reminder with original Today, backlog, section and planner behavior.")
	addCommandTool[ScheduleInput](server, s, "task_reschedule", "Change a scheduled instant using the client's distinct reschedule action.")
	addCommandTool[UnscheduleInput](server, s, "task_unschedule", "Clear scheduled time and reminder; optionally preserve the logical Today date.")
	addCommandTool[TaskIDInput](server, s, "task_dismiss_reminder", "Dismiss only the scheduling reminder; retain the plan.")
	addCommandTool[DeadlineInput](server, s, "task_set_deadline", "Set a date or timed deadline. Apply original deadline auto-planning, including future plans and nested subtasks.")
	addCommandTool[TaskIDInput](server, s, "task_remove_deadline", "Remove deadline fields and its reminder; retain scheduling fields.")
	addCommandTool[TaskIDInput](server, s, "task_dismiss_deadline_reminder", "Dismiss only the deadline reminder.")
	addCommandTool[TaskIDInput](server, s, "task_delete", "Delete a task and its children; update parent time and references using the client's singular delete action.")
	addCommandTool[TaskIDsInput](server, s, "tasks_delete", "Delete selected tasks atomically using the client's bulk action, parent-order time rules and calendar import dismissal data.")
}
