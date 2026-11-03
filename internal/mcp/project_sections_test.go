// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package mcp

import (
	"github.com/blackcode-ch/tmx-go/internal/api/apitest"
	"strings"
	"testing"
)

func TestProjectSectionTool(t *testing.T) {
	d := apitest.New(t)
	head := session(t, d, "head")
	mustOK(t, head, "project_create", map[string]any{"name": "Daily report"})
	lead := session(t, d, "daily-report-lead")
	out := mustOK(t, lead, "announce_set", map[string]any{"project": "daily-report", "status": "Ready", "needs": []string{"Review"}, "links": []string{"Guide | https://example.test/guide"}})
	if !strings.Contains(out, "Ready") || !strings.Contains(out, "Guide") {
		t.Fatal(out)
	}
	if _, bad := call(t, lead, "announce_set", map[string]any{"project": "daily-report", "status": "1\n2\n3\n4"}); !bad {
		t.Fatal("accepted fourth line")
	}
}

func TestContributionAndArtifactsTools(t *testing.T) {
	d := apitest.New(t)
	head := session(t, d, "head")
	mustOK(t, head, "project_create", map[string]any{"name": "Artifacts"})
	lead := session(t, d, "artifacts-lead")
	outputs := []map[string]string{{"path": "https://example.test/result", "what": "Result"}}
	mustOK(t, lead, "my_part", map[string]any{"project": "artifacts", "status": "Ready", "final_output": outputs})
	mustOK(t, lead, "announce_set", map[string]any{"project": "artifacts", "final_output": outputs})
	out := mustOK(t, lead, "project_artifacts", map[string]any{"project": "artifacts"})
	if !strings.Contains(out, `"kind":"link"`) || !strings.Contains(out, "Ready") {
		t.Fatal(out)
	}
	if _, bad := call(t, head, "my_part", map[string]any{"project": "artifacts", "status": "Overwrite"}); !bad {
		t.Fatal("nonmember wrote contribution")
	}
}
