// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 The tmx authors

package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/release"
)

func TestReleaseKeygenPrintsOnlyThePublicKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv(release.EnvHome, home)
	var out, errw bytes.Buffer
	if code := run([]string{"release", "keygen"}, &out, &errw); code != 0 {
		t.Fatalf("keygen: %d %s", code, errw.String())
	}
	key, err := os.ReadFile(release.KeyFile(home, "fleet-release-v1"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String()+errw.String(), strings.TrimSpace(string(key))) {
		t.Fatal("keygen printed the private key")
	}
	for _, want := range []string{"public key:", "fingerprint:", `"fleet_keys"`, `"fleet-release-v1"`} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %s:\n%s", want, out.String())
		}
	}
	out.Reset()
	if code := run([]string{"release", "keygen"}, &out, &errw); code == 0 || !strings.Contains(errw.String(), "exists") {
		t.Fatalf("second keygen: %d %s", code, errw.String())
	}
	if b, _ := os.ReadFile(release.KeyFile(home, "fleet-release-v1")); !bytes.Equal(b, key) {
		t.Fatal("key replaced")
	}
	if code := run([]string{"release", "sign"}, &out, &errw); code == 0 {
		t.Fatal("sign without a tag")
	}
}
