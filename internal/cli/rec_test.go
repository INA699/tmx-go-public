// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package cli

import (
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/model"
	"github.com/blackcode-ch/tmx-go/internal/recorder"
)

func TestRecCommands(t *testing.T) {
	d := cliDaemon(t)
	if out, _, code := tmx(t, "rec", "ls"); code != 0 || !strings.Contains(out, "no recordings") {
		t.Fatalf("empty: %d %s", code, out)
	}
	if _, errs, code := tmx(t, "rec", "start"); code == 0 || !strings.Contains(errs, "unknown rec command") {
		t.Fatalf("rec start exists: %d %s", code, errs)
	}
	r, err := d.Recorder.Begin(recorder.BeginRequest{Kind: model.RecordingScreen, Mic: true, Mime: "video/mp4;codecs=avc1", Title: "Demo"})
	if err != nil {
		t.Fatal(err)
	}
	d.Recorder.Append(r.ID, 1, recorder.TrackMedia, 0, 0, []byte("mp4 bytes"))
	d.Recorder.Append(r.ID, 1, recorder.TrackPCM, 0, 0, make([]byte, 64000))
	d.Recorder.SetState(r.ID, "stopped")
	if out, _, code := tmx(t, "rec", "ls"); code != 0 || !strings.Contains(out, r.ID) || !strings.Contains(out, "screen  done   0:02") || !strings.Contains(out, "Demo") {
		t.Fatalf("ls: %d %s", code, out)
	}
	if out, _, code := tmx(t, "rec", "show", r.ID); code != 0 || !strings.Contains(out, "seg-0001.mp4") || !strings.Contains(out, "16 kHz WAV") {
		t.Fatalf("show: %d %s", code, out)
	}
	if _, errs, code := tmx(t, "rec", "rm", r.ID); code == 0 || !strings.Contains(errs, "--yes") {
		t.Fatalf("rm without --yes: %d %s", code, errs)
	}
	if out, _, code := tmx(t, "rec", "rm", r.ID, "--yes"); code != 0 || !strings.Contains(out, "deleted") {
		t.Fatalf("rm: %d %s", code, out)
	}
	if _, errs, code := tmx(t, "rec", "show", r.ID); code == 0 || !strings.Contains(errs, "no recording") {
		t.Fatalf("show deleted: %d %s", code, errs)
	}
}
