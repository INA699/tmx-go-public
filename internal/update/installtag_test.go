package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// tagged adds one more published release (its own files under /assets/<tag>/), signed by key in
// the owner's format.
func (g *fakeGitHub) tagged(tag string, key ed25519.PrivateKey, prerelease bool) {
	name := "tmx_" + tag + "_linux_amd64"
	bin := []byte("binary " + tag)
	h := sha256.Sum256(bin)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(h[:]), name)
	g.mu.Lock()
	defer g.mu.Unlock()
	r := release{TagName: tag, Prerelease: prerelease, HTMLURL: "https://example.invalid/" + tag}
	add := func(n string, b []byte) {
		g.files[tag+"/"+n] = b
		r.Assets = append(r.Assets, asset{Name: n, Size: int64(len(b)), URL: g.srv.URL + "/assets/" + tag + "/" + n})
	}
	add(name, bin)
	add("SHA256SUMS", []byte(sums))
	add("SHA256SUMS.sig", signOwner(key, []byte(sums)))
	g.releases[tag] = r
}

// InstallTag installs exactly the tag asked for (not the newest), and refuses anything that is
// not a published, newer v* release on this channel, before installing anything.
func TestInstallTag(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	for _, tag := range []string{"v0.1.0", "v0.2.0", "v0.3.0"} {
		g.tagged(tag, key, false)
	}
	g.tagged("v0.4.0-rc1", key, true)
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "v0.1.1")
	h := newHarness(t, g, pub, "v0.1.1", exe, "linux", false)
	ctx := context.Background()

	for tag, want := range map[string]string{
		"fleet-v0.2.0":      "not a release tag",
		"v0.2.0-3-gabcdef0": "not a release tag",
		"v0.9.0":            "no published release v0.9.0",
		"v0.4.0-rc1":        "no published release v0.4.0-rc1 on the stable channel",
		"v0.1.0":            "not newer than the running v0.1.1",
	} {
		if _, err := h.u.InstallTag(ctx, tag, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", tag, err, want)
		}
	}
	select {
	case <-h.restarts:
		t.Fatal("a refused tag installed")
	default:
	}
	h.u.mu.Lock()
	want := h.u.want
	h.u.mu.Unlock()
	if want != "" {
		t.Fatalf("a refused tag stayed pinned: %q", want)
	}

	go h.u.Run(ctx)
	if _, err := h.u.InstallTag(ctx, "v0.2.0", true); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.restarts:
	case <-time.After(10 * time.Second):
		t.Fatalf("no install: %+v", h.u.Status())
	}
	if b, _ := os.ReadFile(exe); string(b) != "binary v0.2.0" {
		t.Fatalf("installed %q, want v0.2.0 (not the newest)", b)
	}
	// The pin lasts one install (cleared as install returns, just after the restart); without it
	// the newest is found again.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		h.u.mu.Lock()
		w := h.u.want
		h.u.mu.Unlock()
		if w == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the pin %q outlived the install", w)
		}
	}
	if r, err := h.u.latestRelease(ctx, "o/r", "stable"); err != nil || r.TagName != "v0.3.0" {
		t.Fatalf("without a pin: %s %v", r.TagName, err)
	}
}

// One pinned tag at a time: while an install of one tag is pending, another tag is refused, so an
// approval for one tag never installs another (review of #122). The same tag again is fine.
func TestInstallTagOneAtATime(t *testing.T) {
	g := newFakeGitHub(t)
	key, pub := newKey(t)
	for _, tag := range []string{"v0.2.0", "v0.3.0"} {
		g.tagged(tag, key, false)
	}
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "v0.1.1")
	h := newHarness(t, g, pub, "v0.1.1", exe, "linux", false)
	ctx := context.Background()
	if _, err := h.u.InstallTag(ctx, "v0.2.0", false); err != nil { // queued, Run not started
		t.Fatal(err)
	}
	if _, err := h.u.InstallTag(ctx, "v0.3.0", false); err == nil || !strings.Contains(err.Error(), "v0.2.0 is already pending") {
		t.Fatalf("a second tag while one is pending: %v", err)
	}
	if _, err := h.u.InstallTag(ctx, "v0.2.0", false); err != nil {
		t.Fatalf("the same tag again: %v", err)
	}
}

// The staged binary must report exactly the version: "v0.2.0-dirty" is not v0.2.0
// (the probation's "to" must equal the running version, rollback.StartProbation).
func TestCheckWantsTheExactVersion(t *testing.T) {
	g := newFakeGitHub(t)
	_, pub := newKey(t)
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "v0.1.1")
	h := newHarness(t, g, pub, "v0.1.1", exe, "linux", false)
	for out, ok := range map[string]bool{
		"tmx v0.2.0":          true,
		"tmx v0.2.0\n":        true,
		"tmx v0.2.0-dirty":    false,
		"tmx v0.2.0-1-gabcd1": false,
		"tmx xv0.2.0":         false,
	} {
		h.u.o.Probe = func(context.Context, string) (string, error) { return out, nil }
		err := h.u.check(context.Background(), h.u.target, staged{version: "v0.2.0", path: exe})
		if (err == nil) != ok {
			t.Errorf("%q: %v", out, err)
		}
	}
}
