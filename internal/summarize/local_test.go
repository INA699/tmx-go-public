// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package summarize

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// fakeOllama answers /api/generate; with noVision it refuses any request that carries images.
func fakeOllama(t *testing.T, noVision bool) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	var mu sync.Mutex
	got := &[]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/generate" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		*got = append(*got, body)
		mu.Unlock()
		if _, has := body["images"]; has && noVision {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"this model does not support images"}`))
			return
		}
		_, _ = w.Write([]byte(`{"response":"{\"summary\":\"Budget CHF 48,500 reviewed\"}"}`))
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

func localService(cfg config.SummaryConfig) *Service {
	return New(Options{Settings: func() config.SummaryConfig { return cfg }})
}

func framesAsk(t *testing.T, n int) ask {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, FramesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	a := ask{dir: dir, system: "sys", prompt: "Transcript: hello", frames: n, model: "x"}
	for i := range n {
		p := FramesDir + "/f-" + strings.Repeat("0", 8) + string(rune('0'+i)) + ".jpg"
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte{0xff, 0xd8, 0xff, byte(i)}, 0o644); err != nil {
			t.Fatal(err)
		}
		a.paths = append(a.paths, p)
	}
	return a
}

// The default writes the report on this machine: Ollama on loopback, the frames as images, JSON asked for.
func TestLocalIsTheDefaultAndSendsFrames(t *testing.T) {
	srv, got := fakeOllama(t, false)
	s := localService(config.SummaryConfig{LocalURL: srv.URL})
	if !s.local() {
		t.Fatal("the default backend must be local (client recordings never reach a cloud API)")
	}
	text, err := s.run(t.Context(), framesAsk(t, 9))
	if err != nil {
		t.Fatal(err)
	}
	if sum, err := parseAnswer(text); err != nil || !strings.Contains(sum.Summary, "48,500") {
		t.Fatalf("answer %q: %+v %v", text, sum, err)
	}
	b := (*got)[0]
	if b["model"] != DefaultLocalModel || b["format"] != "json" || b["system"] != "sys" {
		t.Fatalf("request %+v", b)
	}
	if imgs, _ := b["images"].([]any); len(imgs) != LocalFrames {
		t.Fatalf("images: %d, want %d spread over 9 frames", len(imgs), LocalFrames)
	}
}

// A model without vision still writes the report, from the transcript alone.
func TestLocalFallsBackToTextWithoutVision(t *testing.T) {
	srv, got := fakeOllama(t, true)
	s := localService(config.SummaryConfig{Backend: "local", LocalURL: srv.URL, LocalModel: "qwen3:14b"})
	if _, err := s.run(t.Context(), framesAsk(t, 2)); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 || (*got)[1]["images"] != nil || (*got)[1]["model"] != "qwen3:14b" {
		t.Fatalf("requests %+v", *got)
	}
}

// Client content never leaves the machine: a non-loopback address is refused before anything is sent.
func TestLocalRefusesARemoteAddress(t *testing.T) {
	for _, u := range []string{"http://localhost:11434", "http://192.0.2.10:11434", "https://127.0.0.1:11434", "http://example.com", "ftp://127.0.0.1"} {
		s := localService(config.SummaryConfig{LocalURL: u})
		if _, err := s.run(t.Context(), ask{dir: t.TempDir(), prompt: "p"}); err == nil || !strings.Contains(err.Error(), "local_url") {
			t.Fatalf("%s: %v", u, err)
		}
	}
	if s := localService(config.SummaryConfig{Backend: "claude"}); s.local() {
		t.Fatal("backend claude is the opt-in cloud path")
	}
}

// A redirect from whatever answers on the port is never followed: the transcript and frames stay on this machine.
func TestLocalNeverFollowsARedirect(t *testing.T) {
	away := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the request followed a redirect")
	}))
	t.Cleanup(away.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, away.URL+"/api/generate", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	s := localService(config.SummaryConfig{LocalURL: srv.URL})
	if _, err := s.run(t.Context(), ask{dir: t.TempDir(), prompt: "p"}); err == nil {
		t.Fatal("a redirect answer was taken as a summary")
	}
}
