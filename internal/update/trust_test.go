package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/blackcode-ch/tmx-go/internal/hiddenexec"
)

// Pin the identity of the compiled public release key without duplicating its bytes.
const ownerPublicFingerprint = "507f6209f615997ed3deaa7391822230a1322893782946699173847af1ce7014"

// legacyVerifySums is the verifier of v0.1.0-v0.1.10 (internal/update/sign.go at 0c36197), kept
// here word for word: every owner release must still pass it, or those installs cannot update.
func legacyVerifySums(sums, sig []byte, keys []ed25519.PublicKey) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return errors.New("SHA256SUMS.sig is not a signature")
	}
	for _, k := range keys {
		if ed25519.Verify(k, sums, raw) {
			return nil
		}
	}
	return errUnsigned
}

func readFixture(t *testing.T, path ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The owner's key is built in and trusted: a release build with no update config can update.
func TestOwnerReleaseKeyIsTrusted(t *testing.T) {
	if len(releaseKeys) == 0 {
		t.Fatalf("keys.go does not hold the owner's release key: %v", releaseKeys)
	}
	keys, err := publicKeys(releaseKeys)
	if err != nil || len(keys) != len(releaseKeys) {
		t.Fatalf("release keys: %v", err)
	}
	fingerprint := sha256.Sum256(keys[0])
	if hex.EncodeToString(fingerprint[:]) != ownerPublicFingerprint {
		t.Fatal("compiled public release key identity changed")
	}
	ownerKey := releaseKeys[0]

	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "v0.1.10", Executable: "tmx.exe", GOOS: "windows", GOARCH: "amd64"})
	if st := u.Status(); st.State == StateOff {
		t.Fatalf("a release build with the built-in key is off: %s", st.Reason)
	}
	if got := u.Keys(); len(got.ReleaseKeys) != 1 || got.ReleaseKeys[0] != ownerKey || len(got.FleetKeys) != 0 {
		t.Fatalf("GET /api/update/keys: %+v", got)
	}
	// Pinned as an extra key too, it is just another key (nothing refuses it).
	if _, err := newKeySet([]config.FleetKey{{ID: "owner", Type: config.KeyEd25519, Key: ownerKey}}, nil); err != nil {
		t.Fatal(err)
	}
}

// The real v0.1.10 release and voice engine, signed by CI with TMX_RELEASE_SIGNING_KEY through
// scripts/release-sums.sh, verify with this build's built-in key, as daemon release and as extra,
// and still with the verifier installed v0.1.10 clients run.
func TestOwnerSignedReleasesVerify(t *testing.T) {
	owner, _ := publicKeys(releaseKeys)
	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "v0.1.9", Executable: "tmx.exe", GOOS: "windows", GOARCH: "amd64"})
	ks, err := u.trustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		dir, purpose, tag, file string
	}{
		{"v0.1.10", FleetSignatureContext, "v0.1.10", "tmx_v0.1.10_windows_amd64.exe"},
		{"whisper-engine-v1.9.5", FleetExtraSignatureContext, "whisper-engine-v1.9.5", "whisper-cli_darwin_arm64"},
	} {
		sums, sig := readFixture(t, c.dir, "SHA256SUMS"), readFixture(t, c.dir, "SHA256SUMS.sig")
		if err := verifySums(c.purpose, u.Repo(), c.tag, sums, sig, ks); err != nil {
			t.Errorf("%s: %v", c.dir, err)
		}
		if err := legacyVerifySums(sums, sig, owner); err != nil {
			t.Errorf("%s with the v0.1.10 verifier: %v", c.dir, err)
		}
		if _, ok := parseSums(sums)[c.file]; !ok {
			t.Errorf("%s: SHA256SUMS does not list %s", c.dir, c.file)
		}
		// The owner's format is never a rotation statement's, and one changed byte breaks it.
		if verifySums(FleetKeysContext, u.Repo(), c.tag, sums, sig, ks) == nil {
			t.Errorf("%s: accepted as a rotation statement", c.dir)
		}
		if verifySums(c.purpose, u.Repo(), c.tag, append(sums, '\n'), sig, ks) == nil {
			t.Errorf("%s: accepted changed sums", c.dir)
		}
	}
	if _, err := u.VerifySums("whisper-engine-v1.9.5", readFixture(t, "whisper-engine-v1.9.5", "SHA256SUMS"), readFixture(t, "whisper-engine-v1.9.5", "SHA256SUMS.sig")); err != nil {
		t.Fatalf("voice engine: %v", err)
	}
	// Signed by a key this build does not know: refused.
	u = New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "v0.1.9", Executable: "tmx.exe", GOOS: "windows", GOARCH: "amd64", Keys: []string{base64.StdEncoding.EncodeToString(make([]byte, 32))}})
	if _, err := u.VerifySums("v0.1.10", readFixture(t, "v0.1.10", "SHA256SUMS"), readFixture(t, "v0.1.10", "SHA256SUMS.sig")); !errors.Is(err, errUnsigned) {
		t.Fatalf("another key: %v", err)
	}
}

// scripts/release-sums.sh, as CI runs it on a release, writes what both the v0.1.10 verifier and
// this one accept (the format both directions of an update depend on).
func TestReleaseSumsScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("CI runs the script on macOS or Linux")
	}
	var openssl string
	for _, o := range []string{"openssl", "/opt/homebrew/opt/openssl@3/bin/openssl", "/usr/local/opt/openssl@3/bin/openssl"} {
		if out, err := hiddenexec.Command(o, "version").Output(); err == nil && strings.HasPrefix(string(out), "OpenSSL 3") {
			openssl = o
		}
	}
	if openssl == "" {
		t.Skip("no OpenSSL 3 here (the script needs it to sign)")
	}
	script, err := filepath.Abs("../../scripts/release-sums.sh")
	if err != nil {
		t.Fatal(err)
	}
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for name, body := range map[string]string{"tmx_v0.2.0_linux_amd64": "binary v0.2.0", "tmx-desktop_v0.2.0_darwin_arm64.app.zip": "zip"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := hiddenexec.Command("bash", script)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TMX_RELEASE_SIGNING_KEY="+string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release-sums.sh: %v\n%s", err, out)
	}
	sums, sig := readFile(t, dir, "SHA256SUMS"), readFile(t, dir, "SHA256SUMS.sig")
	if err := legacyVerifySums(sums, sig, []ed25519.PublicKey{pub}); err != nil {
		t.Fatalf("the v0.1.10 verifier: %v", err)
	}
	if err := verifySums(FleetSignatureContext, "o/r", "v0.2.0", sums, sig, keySet{release: []ed25519.PublicKey{pub}}); err != nil {
		t.Fatalf("this verifier: %v", err)
	}
	if got := parseSums(sums); len(got) != 2 || got["tmx_v0.2.0_linux_amd64"] == "" {
		t.Fatalf("SHA256SUMS: %q", sums)
	}
}

func readFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Extra keys the owner pinned (none by default) sign in their own format, bound to purpose,
// repository and tag; the owner's format by such a key, or their format by an unpinned key, is
// refused.
func TestPinnedExtraKeySignatures(t *testing.T) {
	priv, pub := newKey(t)
	other, _ := newKey(t)
	var cfg config.UpdateConfig
	if err := json.Unmarshal([]byte(`{"fleet_public_key":"`+pub+`"}`), &cfg); err != nil {
		t.Fatal(err)
	}
	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Settings: func() config.UpdateConfig { return cfg }})
	sums := []byte(strings.Repeat("0", 64) + "  fixture\n")
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signatureMessage(FleetExtraSignatureContext, u.Repo(), "whisper-engine-v1", sums)))
	raw := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sums))
	wrongContext := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, append([]byte("upstream-release-v1\n"), sums...)))
	for _, bad := range []string{sig, raw, "upstream:" + sig, FleetKeyID + ":" + raw, FleetKeyID + ":" + wrongContext, FleetKeyID + ":" + base64.StdEncoding.EncodeToString(ed25519.Sign(other, signatureMessage(FleetExtraSignatureContext, u.Repo(), "whisper-engine-v1", sums)))} {
		if _, err := u.VerifySums("whisper-engine-v1", sums, []byte(bad)); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if _, err := u.VerifySums("whisper-engine-v1", sums, []byte("fleet-release-v1:"+sig)); err != nil {
		t.Fatalf("pinned key's signature rejected: %v", err)
	}
	// Not pinned (the default): the same signature counts for nothing.
	u = New(Options{Paths: config.Paths{Home: t.TempDir()}})
	if _, err := u.VerifySums("whisper-engine-v1", sums, []byte("fleet-release-v1:"+sig)); !errors.Is(err, errUnsigned) {
		t.Fatalf("unpinned extra key: %v", err)
	}
}

func TestExtraKeySignedContextBinding(t *testing.T) {
	priv, pub := newKey(t)
	ks := mustKeySet(t, []config.FleetKey{{ID: FleetKeyID, Type: config.KeyEd25519, Key: pub}})
	sums := []byte(strings.Repeat("0", 64) + "  fixture\n")
	const repo, tag = "o/r", "v1.2.3"
	for _, tc := range []struct {
		name, purpose, repo, tag string
		valid                    bool
	}{
		{"daemon", FleetSignatureContext, repo, tag, true},
		{"extras", FleetExtraSignatureContext, repo, tag, false},
		{"other repository", FleetSignatureContext, "other/repo", tag, false},
		{"other tag", FleetSignatureContext, repo, "v9.9.9", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig := []byte(FleetKeyID + ":" + base64.StdEncoding.EncodeToString(ed25519.Sign(priv, signatureMessage(tc.purpose, tc.repo, tc.tag, sums))))
			err := verifySums(FleetSignatureContext, repo, tag, sums, sig, ks)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if tc.valid && verifySums(FleetExtraSignatureContext, repo, tag, sums, sig, ks) == nil {
				t.Fatal("daemon signature accepted for extras")
			}
		})
	}
}

// Releases come from v* tags: fleet-v* tags, the rolling "latest" release of older builds and
// the voice engine's tags are never a release, nor is a version read from a file name.
func TestReleaseTagFilter(t *testing.T) {
	for _, tag := range []string{"fleet-v99.0.0", "fleet-v1.2.3-rc1", "latest", "whisper-engine-v1"} {
		if _, ok := ParseVersion(tag); ok {
			t.Errorf("accepted tag %q", tag)
		}
		u := &Updater{target: target{goos: "linux", goarch: "amd64"}}
		if _, err := u.releaseVersion(release{TagName: tag, Assets: []asset{{Name: "tmx_v99.0.0_linux_amd64"}}}); err == nil {
			t.Errorf("accepted %q through the file name", tag)
		}
	}
	for _, tag := range []string{"v0.1.10", "v1.2.3", "v1.2.3-rc1", "v0.1.10-3-g4eae55b"} {
		if _, ok := ParseVersion(tag); !ok {
			t.Errorf("refused %q", tag)
		}
	}
}

func TestReleaseDiscovery(t *testing.T) {
	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, GH: func() string { return "" }, APIBase: "https://fixture.invalid", HTTP: &http.Client{Transport: releaseVectorTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/repos/o/r/releases" {
			t.Fatalf("forbidden release endpoint: %s", r.URL.Path)
		}
		b, _ := json.Marshal([]release{{TagName: "fleet-v99.0.0"}, {TagName: "latest", Prerelease: true}, {TagName: "whisper-engine-v1.9.5", Prerelease: true}, {TagName: "v1.2.3"}, {TagName: "v2.0.0", Draft: true}, {TagName: "v3.0.0", Prerelease: true}, {TagName: "v2.5.0-rc1"}})
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}})
	for _, e := range []string{"TMX_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(e, "")
	}
	// v2.5.0-rc1 is not marked prerelease, but an rc tag never reaches the stable channel.
	r, err := u.latestRelease(context.Background(), "o/r", "stable")
	if err != nil || r.TagName != "v1.2.3" {
		t.Fatalf("release = %s, %v", r.TagName, err)
	}
	r, err = u.latestRelease(context.Background(), "o/r", "latest")
	if err != nil || r.TagName != "v3.0.0" {
		t.Fatalf("prerelease = %s, %v", r.TagName, err)
	}
}

func TestUpdateAutoDefaultsOff(t *testing.T) {
	for _, c := range []*config.UpdateConfig{nil, {}, {Auto: new(bool)}} {
		if *c.Resolved().Auto {
			t.Fatal("automatic updates enabled by default")
		}
	}
}

func TestAutoOffMakesNoBackgroundRequests(t *testing.T) {
	_, pub := newKey(t)
	dir := t.TempDir()
	u := New(Options{Paths: config.Paths{Home: dir}, Version: "v1.0.0", Executable: filepath.Join(dir, "tmx.exe"), GOOS: "windows", GOARCH: "amd64", Keys: []string{pub}, FirstCheck: time.Millisecond,
		HTTP: &http.Client{Transport: releaseVectorTransport(func(*http.Request) (*http.Response, error) {
			t.Error("automatic network request while auto is off")
			return nil, context.Canceled
		})}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	u.Run(ctx)
}

// Without a usable release key (none built in, or a bad one) updates are off; a bad extra key in
// config.json turns them off too (TestInvalidKeysFailClosed).
func TestNoReleaseKeyIsOff(t *testing.T) {
	for _, keys := range [][]string{{}, {"PLACEHOLDER"}} {
		u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "v1.0.0", Executable: "tmx.exe", GOOS: "windows", GOARCH: "amd64", Keys: keys})
		if s := u.Check(context.Background()); s.State != StateOff {
			t.Fatalf("keys %q: updater on", keys)
		}
	}
}

func TestUpdateCheckCannotTriggerInstallWithoutOwnerProof(t *testing.T) {
	_, pub := newKey(t)
	on := true
	u := New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "v1.0.0", Executable: "tmx.exe", GOOS: "windows", GOARCH: "amd64", Keys: []string{pub}, GH: func() string { return "" },
		Settings: func() config.UpdateConfig { return config.UpdateConfig{Auto: &on} },
		HTTP: &http.Client{Transport: releaseVectorTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"tag_name":"v2.0.0"}]`))}, nil
		})}})
	for _, e := range []string{"TMX_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(e, "")
	}
	if s := u.Check(context.Background()); !s.Available {
		t.Fatal(s)
	}
	if len(u.installs) != 0 {
		t.Fatal("public check queued an automatic install")
	}
}

func TestAutoDisabledDuringCheckDoesNotInstall(t *testing.T) {
	_, pub := newKey(t)
	on, off := true, false
	cfg := config.UpdateConfig{Auto: &on}
	var u *Updater
	u = New(Options{Paths: config.Paths{Home: t.TempDir()}, Version: "v1.0.0", Executable: "tmx.exe", GOOS: "windows", GOARCH: "amd64", Keys: []string{pub}, GH: func() string { return "" },
		Settings: func() config.UpdateConfig { return cfg },
		Save:     func(next config.UpdateConfig) error { cfg = next; return nil },
		HTTP: &http.Client{Transport: releaseVectorTransport(func(r *http.Request) (*http.Response, error) {
			// The owner disables auto after discovery starts but before it returns.
			if _, err := u.Configure(&off, nil); err != nil {
				t.Fatal(err)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`[{"tag_name":"v2.0.0"}]`))}, nil
		})}})
	for _, e := range []string{"TMX_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
		t.Setenv(e, "")
	}
	if s := u.checkRelease(context.Background(), true); !s.Available || s.Auto {
		t.Fatal(s)
	}
	if len(u.installs) != 0 {
		t.Fatal("in-flight check queued an install after auto was disabled")
	}
}

// The owner's signature installs whatever extra keys are pinned and whatever their require
// policy asks; a rotation statement in a release changes nothing while no extra key is pinned,
// and never touches the owner's key.
func TestOwnerReleaseBesideExtraKeys(t *testing.T) {
	g := newFakeGitHub(t)
	owner, pub := newKey(t)
	k1, p1 := newKey(t)
	k2, p2 := newKey(t)
	exe := filepath.Join(t.TempDir(), "bin", "tmx")
	writeExe(t, exe, "v0.1.1")
	h := newHarness(t, g, pub, "v0.1.1", exe, "linux", false)
	byOwner := func(sums []byte) []byte { return signOwner(owner, sums) }

	// No extra key pinned: a statement (by a key this machine does not know) is ignored.
	s1 := statement(t, "o/r", 1, edKey("k1", p1))
	g.releaseSigned("v0.2.0", byOwner, s1, []byte(SignKeysLine("k1", k1, "o/r", 1, s1)))
	if err := h.stageLatest(t); err != nil {
		t.Fatalf("owner release with a statement: %v", err)
	}
	if ids, n := h.pinned(); ids != "" || n != 0 {
		t.Fatalf("a statement pinned %q (serial %d) without any extra key pinned", ids, n)
	}

	// Extra keys pinned, even with a policy the owner's key cannot meet: the owner's release installs.
	h.mu.Lock()
	h.settings.FleetKeys = []config.FleetKey{edKey("k1", p1)}
	h.mu.Unlock()
	g.releaseSigned("v0.2.0", byOwner, nil, nil)
	if err := h.stageLatest(t); err != nil {
		t.Fatalf("owner release beside a pinned key: %v", err)
	}
	// ... and so does one by the pinned key, in its own format; a statement by it rotates only the
	// extra keys.
	s2 := statement(t, "o/r", 1, edKey("k2", p2))
	g.releaseSigned("v0.3.0", func(sums []byte) []byte { return []byte(SignReleaseLine("k2", k2, "o/r", "v0.3.0", sums)) }, s2, []byte(SignKeysLine("k1", k1, "o/r", 1, s2)))
	if err := h.stageLatest(t); err != nil {
		t.Fatalf("extra-key release: %v", err)
	}
	if ids, n := h.pinned(); ids != "k2" || n != 1 {
		t.Fatalf("after the statement: %s %d", ids, n)
	}
	g.releaseSigned("v0.4.0", byOwner, nil, nil)
	if err := h.stageLatest(t); err != nil {
		t.Fatalf("owner release after a rotation: %v", err)
	}
	// An owner-format signature by an extra key is not the owner's.
	g.releaseSigned("v0.4.0", func(sums []byte) []byte { return signOwner(k2, sums) }, nil, nil)
	if err := h.stageLatest(t); err == nil {
		t.Fatal("an extra key's raw signature passed as the owner's")
	}
}
