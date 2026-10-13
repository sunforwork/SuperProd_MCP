package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"github.com/sunforwork/SuperProd_MCP/internal/service"
)

func approvalResult(v p.Object, nonce string, e error) (*mcp.CallToolResult, p.Object, error) {
	if e != nil {
		return result(nil, e)
	}
	return &mcp.CallToolResult{StructuredContent: v, Content: []mcp.Content{&mcp.TextContent{Text: "Review the complete business snapshot difference and approve once in the card. Offline devices may encounter native replacement conflicts."}}, Meta: mcp.Meta{"approvalCredential": nonce}}, v, nil
}
func addBusinessTools(server *mcp.Server, s *service.Service) {
	mcp.AddTool(server, tool("read_business_json", "Read current, draft, proposal, before-proposal or named-backup business JSON by JSON Pointer. Default entries mode paginates containers; mode=value returns small complete values. Pass baseRevision/draftVersion for consistent multi-call reads. Credentials are redacted; never write redaction placeholders.", true, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.BusinessReadInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.ReadBusinessJSON(ctx, identity(ctx), in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("begin_free_edit", "Begin a 30-minute JSON draft of the latest complete business document; requires requestId and baseRevision. No automatic named backup. "+service.BackupGuidance, false, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.BeginEditInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.BeginFreeEdit(ctx, identity(ctx), in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("apply_free_edit", "Apply RFC 6902 add/remove/replace/move/copy/test to a draft with requestId and draftVersion. Intermediate business inconsistency is allowed. Invalidates previous approval. Final validation occurs at propose_free_edit. On cloud version conflict inform the user, read latest data and restart; never merge the old draft.", false, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.ApplyEditInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.ApplyFreeEdit(ctx, identity(ctx), in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("cancel_free_edit", "Discard a draft or pending proposal and invalidate its approval credential. Does not modify sync data.", false, true), func(ctx context.Context, r *mcp.CallToolRequest, in service.CancelEditInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.CancelFreeEdit(ctx, identity(ctx), in.DraftID)
		return result(v, e)
	})
	mcp.AddTool(server, tool("get_edit_diff", "Read every approved-document difference with pagination. Large values link to read_business_json source=before or proposal. Full document hash binds all pages including archives.", true, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.EditDiffInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.EditDiff(ctx, identity(ctx), in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("create_business_backup", "Name the current complete business JSON using an encrypted immutable history reference, without duplicating its content. Requires requestId/baseRevision. "+service.BackupGuidance, false, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.CreateBackupInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.CreateBusinessBackup(ctx, identity(ctx), in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("list_business_backups", "List this account's named recovery points, source versions and logical sizes. References do not duplicate history storage. "+service.BackupGuidance, true, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.ListBackupInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.ListBusinessBackups(ctx, in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("rename_business_backup", "Rename a named backup without changing its ID or data. Requires requestId.", false, false), func(ctx context.Context, r *mcp.CallToolRequest, in service.RenameBackupInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.RenameBusinessBackup(ctx, identity(ctx), in)
		return result(v, e)
	})
	mcp.AddTool(server, tool("delete_business_backup", "Delete a named recovery entry and invalidate its pending rollback proposals. Existing sync history remains; this does not reclaim its physical database space. Requires requestId.", false, true), func(ctx context.Context, r *mcp.CallToolRequest, in service.DeleteBackupInput) (*mcp.CallToolResult, p.Object, error) {
		v, e := s.DeleteBusinessBackup(ctx, identity(ctx), in)
		return result(v, e)
	})
	restore := tool("propose_backup_restore", "Validate a named backup and preview full replacement of current business JSON including archives. Requires baseRevision and reason, then one approval in the card. Uses current protocol causality; does not restore credentials. No automatic named backup.", false, false)
	restore.Annotations.IdempotentHint = false
	restore.Meta["ui"] = p.Object{"resourceUri": "ui://super-productivity/review.html"}
	mcp.AddTool(server, restore, func(ctx context.Context, r *mcp.CallToolRequest, in service.RestoreBackupInput) (*mcp.CallToolResult, p.Object, error) {
		return approvalResult(s.ProposeBackupRestore(ctx, identity(ctx), in))
	})
}
