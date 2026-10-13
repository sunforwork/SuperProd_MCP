package assets

import "embed"

//go:embed plugin/plugin.json plugin/skills/*/SKILL.md web/review.html web/models.md LICENSE NOTICE THIRD_PARTY_NOTICES.md licenses
var Files embed.FS
