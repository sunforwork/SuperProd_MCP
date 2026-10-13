package compat

import (
	_ "embed"
	"encoding/json"
)

const Commit = "71eb7780bcf5d6b1dfdcd39a8a8265547d040760"
const SchemaVersion = 4
const MaxRecentOps = 2000

//go:embed actions.generated.json
var CatalogJSON []byte

//go:embed defaults.generated.json
var DefaultsJSON []byte

//go:embed coverage.generated.json
var CoverageJSON []byte

var Coverage struct {
	ReducerFixtureCount         int      `json:"reducerFixtureCount"`
	QueryFixtureCount           int      `json:"queryFixtureCount"`
	OperationFixtureCount       int      `json:"operationFixtureCount"`
	CommandFixtureCount         int      `json:"commandFixtureCount"`
	TaskDefaultsFixtureCount    int      `json:"taskDefaultsFixtureCount"`
	LocalEffectFixtureCount     int      `json:"localEffectFixtureCount"`
	ArchiveDocumentFixtureCount int      `json:"archiveDocumentFixtureCount"`
	ArchiveReplayFixtureCount   int      `json:"archiveReplayFixtureCount"`
	VerifiedCommandScenarios    []string `json:"verifiedCommandScenarios"`
}

type Action struct {
	Name       string `json:"name"`
	WireAction string `json:"wireAction"`
	Code       string `json:"code"`
	Sources    []struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"sources"`
}
type Catalog struct {
	UpstreamCommit string   `json:"upstreamCommit"`
	Actions        []Action `json:"actions"`
}

var Registry Catalog
var ByCode = map[string]Action{}
var ByName = map[string]Action{}

func init() {
	if err := json.Unmarshal(CoverageJSON, &Coverage); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(CatalogJSON, &Registry); err != nil {
		panic(err)
	}
	for _, a := range Registry.Actions {
		ByCode[a.Code] = a
		ByName[a.Name] = a
	}
}
