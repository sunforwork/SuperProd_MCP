package mcpserver

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sunforwork/SuperProd_MCP/internal/engine"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
)

type WorkContextInput struct {
	ContextType string `json:"contextType" jsonschema:"PROJECT or TAG; use TAG with TODAY for the logical-day view."`
	ContextID   string `json:"contextId"`
}
type TaskOrderInput struct {
	TaskIDInput
	WorkContextInput
	IsBacklog bool `json:"isBacklog,omitempty" jsonschema:"PROJECT backlog only. A child boundary moves its parent in the regular list, following the client."`
}

type TaskDragInput struct {
	TaskIDInput
	WorkContextInput
	Src           string   `json:"src" jsonschema:"UNDONE, DONE or BACKLOG."`
	Target        string   `json:"target" jsonschema:"UNDONE, DONE or BACKLOG."`
	NewOrderedIDs []string `json:"newOrderedIds" jsonschema:"The destination list order after moving the task; the original client derives an anchor from this list."`
}
type TaskRegularInput struct {
	TaskProjectInput
	IsMoveToTop bool `json:"isMoveToTop,omitempty"`
}

func addWorkContextTools(server *mcp.Server, s *service.Service) {
	mcp.AddTool(server, tool("get_work_context", "Read the original work-context view, ordered membership, unfinished IDs, trackable tasks and startable tasks. Parent/child nesting, archived projects and virtual TODAY follow client selectors.", true, false), func(ctx context.Context, r *mcp.CallToolRequest, in WorkContextInput) (*mcp.CallToolResult, p.Object, error) {
		d, err := s.Read(ctx)
		if err != nil {
			return result(nil, err)
		}
		v, err := engine.WorkContextView(d.State, in.ContextType, in.ContextID, s.Now(), s.Location)
		if err == nil {
			v["revision"] = d.File.Revision
		}
		return result(v, err)
	})
	addCommandTool[TaskOrderInput](server, s, "task_move_up", "Move a task up in an explicit client view, skipping completed neighbors. A first child moves its parent; a child shown as a main item follows context ordering.")
	addCommandTool[TaskOrderInput](server, s, "task_move_down", "Move a task down in an explicit client view, skipping completed neighbors. A last child moves its parent; matching section order updates in the same operation.")
	addCommandTool[TaskOrderInput](server, s, "task_move_to_top", "Move a task to the top of the explicit regular/backlog list. Children move to the top of their parent's child order, following the client.")
	addCommandTool[TaskOrderInput](server, s, "task_move_to_bottom", "Move a task to the bottom of the explicit regular/backlog list. Children move to the bottom of their parent's child order, following the client.")
	addCommandTool[TaskDragInput](server, s, "task_move_in_context", "Move a task between or within client regular/done/backlog lists using the destination order. Crossing completion lists emits a separate native status operation. Recurring completion is unavailable.")
	addCommandTool[TaskProjectInput](server, s, "task_send_to_backlog", "Use the native project backlog command. Retains date planning; disabled backlog or an existing backlog member follows the client's no-op state behavior.")
	addCommandTool[TaskRegularInput](server, s, "task_send_to_regular", "Move a backlog task into its project's regular list, at the end by default or top when requested. Preserves native guards and planning.")
}
