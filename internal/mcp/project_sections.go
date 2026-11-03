// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package mcp

import (
	"context"
	"encoding/json"
	"github.com/blackcode-ch/tmx-go/internal/client"
	"github.com/blackcode-ch/tmx-go/internal/fleet"
	"github.com/blackcode-ch/tmx-go/internal/model"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type projectSectionArgs struct {
	FinalOutput *[]model.ProjectOutput `json:"final_output,omitempty" jsonschema:"shared final outputs; only the project lead can set these"`
	Project     string                 `json:"project" jsonschema:"project name or id"`
	Status      *string                `json:"status,omitempty" jsonschema:"at most three short lines in plain words"`
	Needs       *[]string              `json:"needs,omitempty" jsonschema:"replace the checklist; preserves ticks for unchanged text; never creates cards"`
	Links       *[]string              `json:"links,omitempty" jsonschema:"Title | URL or a bare path"`
	Lead        *string                `json:"lead,omitempty" jsonschema:"local feedback recipient; empty restores the project lead"`
}

func (t *Tools) registerProjectSections(s *sdk.Server) {
	sdk.AddTool(s, &sdk.Tool{Name: "my_part", Description: "Update your own contribution to a local project. Identity comes from authentication; omitted fields stay and empty values clear."}, t.projectContributionSet)
	sdk.AddTool(s, &sdk.Tool{Name: "project_artifacts", Description: "Read local project final outputs and agent contributions. Links and paths are reported metadata, not verified files."}, t.projectArtifactsGet)
	sdk.AddTool(s, &sdk.Tool{Name: "announce_set", Description: "Update the project's daily section: status (at most 3 lines), needs (checklist only, never Your-turn cards), links and local feedback lead. Omitted fields stay; empty values clear."}, t.projectSectionSet)
}
func (t *Tools) projectSectionSet(ctx context.Context, _ *sdk.CallToolRequest, in projectSectionArgs) (*sdk.CallToolResult, any, error) {
	var sec model.ProjectSection
	patch := fleet.ProjectSectionPatch{Status: in.Status, Needs: in.Needs, Links: in.Links, Lead: in.Lead, FinalOutput: in.FinalOutput}
	if err := t.c.Post(ctx, "/api/projects/"+client.Seg(in.Project)+"/section", patch, &sec); err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(sec)
	if err != nil {
		return nil, nil, err
	}
	return untrusted(string(data)), nil, nil
}

type projectContributionArgs struct {
	Project     string                 `json:"project" jsonschema:"local project name or id"`
	Status      *string                `json:"status,omitempty"`
	FinalOutput *[]model.ProjectOutput `json:"final_output,omitempty"`
}

func (t *Tools) projectContributionSet(ctx context.Context, _ *sdk.CallToolRequest, in projectContributionArgs) (*sdk.CallToolResult, any, error) {
	var out model.ProjectContribution
	if err := t.c.Post(ctx, "/api/projects/"+client.Seg(in.Project)+"/contribution", fleet.ProjectContributionPatch{Status: in.Status, FinalOutput: in.FinalOutput}, &out); err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return untrusted(string(data)), nil, nil
}

type projectArtifactsArgs struct {
	Project string `json:"project" jsonschema:"local project name or id"`
}

func (t *Tools) projectArtifactsGet(ctx context.Context, _ *sdk.CallToolRequest, in projectArtifactsArgs) (*sdk.CallToolResult, any, error) {
	var out model.ProjectArtifacts
	if err := t.c.Get(ctx, "/api/projects/"+client.Seg(in.Project)+"/artifacts", &out); err != nil {
		return nil, nil, err
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return untrusted(string(data)), nil, nil
}
