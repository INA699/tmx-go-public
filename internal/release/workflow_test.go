package release

import (
	"regexp"
	"testing"

	"github.com/blackcode-ch/tmx-go/internal/update"
)

// The signer treats a tag as data (security-review #118): one strict pattern (bash ERE == Go RE2
// here) refuses the reviewer's forged-row tag. Its fleet-v* tags are not releases a daemon
// installs (updates come from the owner's v* releases, signed in CI; DESIGN.md "Updates"), so
// nothing it signs can reach an install unless a v* release carries it for a pinned extra key.
// The release workflow's own tag checks are in internal/update (TestReleaseWorkflow).
func TestSignerTagPattern(t *testing.T) {
	rx := regexp.MustCompile(`^fleet-v[0-9]+\.[0-9]+\.[0-9]+(-rc[1-9][0-9]*)?$`)
	evil := `fleet-v1"or(false))|empty,("MTIzCXRydWUJZmFsc2U="|@base64d),(.|select(""==`
	for _, tag := range []string{evil, "fleet-v1.2.0-rc0", "fleet-v1.2.0-rc01", "fleet-v1.2", "v1.2.0", "fleet-v1.2.0\n"} {
		if rx.MatchString(tag) || tagRx.MatchString(tag) {
			t.Errorf("%q accepted", tag)
		}
	}
	for _, tag := range []string{"fleet-v1.2.0", "fleet-v1.2.0-rc1", "fleet-v10.0.3-rc12"} {
		if !rx.MatchString(tag) || !tagRx.MatchString(tag) {
			t.Errorf("%q: pattern %v, signer %v", tag, rx.MatchString(tag), tagRx.MatchString(tag))
		}
		if _, ok := update.ParseVersion(tag); ok {
			t.Errorf("%q: a daemon takes it as a release", tag)
		}
	}
}
