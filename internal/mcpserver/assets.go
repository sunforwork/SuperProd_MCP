package mcpserver

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	assets "github.com/sunforwork/SuperProd_MCP"
	p "github.com/sunforwork/SuperProd_MCP/internal/protocol"
	"strings"
)

type skillParams struct {
	mcp.ParamsBase
	URI string `json:"uri,omitempty"`
}
type skillsResult struct {
	mcp.ResultBase
	Skills []p.Object `json:"skills"`
}
type skillResult struct {
	mcp.ResultBase
	Skill p.Object `json:"skill"`
}

func addAssets(server *mcp.Server) {
	resources := map[string]struct{ File, MIME string }{"ui://super-productivity/review.html": {"web/review.html", "text/html;profile=mcp-app"}, "resource://super-productivity/models": {"web/models.md", "text/markdown"}}
	skills := []p.Object{}
	for _, name := range []string{"daily-management", "free-edit-repair"} {
		file := "plugin/skills/" + name + "/SKILL.md"
		uri := "skill://super-productivity/" + name + "/SKILL.md"
		b, _ := assets.Files.ReadFile(file)
		hash := sha256.Sum256(b)
		front := p.Object{}
		parts := strings.SplitN(string(b), "---", 3)
		for _, line := range strings.Split(parts[1], "\n") {
			if k, v, ok := strings.Cut(line, ":"); ok {
				front[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
		skills = append(skills, p.Object{"uri": uri, "frontmatter": front, "resources": []any{p.Object{"uri": uri, "digest": "sha256:" + hex.EncodeToString(hash[:])}}})
		resources[uri] = struct{ File, MIME string }{file, "text/markdown"}
	}
	for uri, v := range resources {
		b, _ := assets.Files.ReadFile(v.File)
		mime := v.MIME
		server.AddResource(&mcp.Resource{URI: uri, Name: uri, MIMEType: mime}, func(ctx context.Context, r *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			meta := mcp.Meta{}
			if strings.HasPrefix(uri, "ui:") {
				meta["ui"] = p.Object{"csp": p.Object{"connectDomains": []string{}, "resourceDomains": []string{}}, "prefersBorder": true}
			}
			return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: uri, MIMEType: mime, Text: string(b), Meta: meta}}}, nil
		})
	}
	if e := mcp.AddReceivingCustomMethod(server, "skills/list", func(ctx context.Context, ss *mcp.ServerSession, in *skillParams) (*skillsResult, error) {
		return &skillsResult{Skills: skills}, nil
	}); e != nil {
		panic(e)
	}
	if e := mcp.AddReceivingCustomMethod(server, "skills/get", func(ctx context.Context, ss *mcp.ServerSession, in *skillParams) (*skillResult, error) {
		for _, skill := range skills {
			if skill["uri"] == in.URI {
				return &skillResult{Skill: skill}, nil
			}
		}
		return nil, errors.New("skill not found")
	}); e != nil {
		panic(e)
	}
}
