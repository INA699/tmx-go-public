package update

import (
	"fmt"

	"github.com/blackcode-ch/tmx-go/internal/config"
)

// releaseKeys are the owner's Ed25519 release keys (base64, 32 bytes): a release whose
// SHA256SUMS.sig is their signature of SHA256SUMS (sign.go, the original format) installs. CI
// signs every v* release with the private key, the GitHub secret TMX_RELEASE_SIGNING_KEY
// (scripts/release-sums.sh). `make release-key` makes a new key pair, stores the private key as
// that secret and adds the public key here. Keep the old key listed until every install has a
// release that knows the new one. Empty = this build installs no updates.
var releaseKeys = []string{
	"8aFwoLBhKp5XN9ovjPIsR3iDzsjSx0iyY/3o1kIQ0Dc=", // 2026-10-27
	// release-key: new keys go above this line
}

// FleetKeyID is the id of the first extra key (and of the legacy update.fleet_public_key).
const FleetKeyID = config.LegacyFleetKeyID

// FleetSignatureContext separates extra-key signatures from signatures of raw sums.
// The purpose, repository, and tag each precede SHA256SUMS as an LF-terminated line.
const FleetSignatureContext = "tmx-fleet-release-v1"

// FleetExtraSignatureContext isolates on-demand extras from daemon releases.
const FleetExtraSignatureContext = "tmx-fleet-extra-v1"

// FleetWebAuthnContext and FleetExtraWebAuthnContext start the preimage of a webauthn signature's
// challenge (webauthnChallenge), for releases and extras.
const (
	FleetWebAuthnContext      = "tmx-fleet-release-v2"
	FleetExtraWebAuthnContext = "tmx-fleet-extra-v2"
)

// trustedKeys is what an install trusts: the owner's release keys (Options.Keys, by default
// releaseKeys) and the extra keys the owner pinned in config.json (update.fleet_keys, with
// update.require; none by default). An invalid pinned set turns updates off (fail closed).
func (u *Updater) trustedKeys() (keySet, error) {
	owner, err := publicKeys(u.o.Keys)
	if err != nil {
		return keySet{}, fmt.Errorf("update keys invalid: %w", err)
	}
	s := u.settings()
	keys, require, err := s.PinnedKeys()
	if err != nil {
		return keySet{}, fmt.Errorf("update keys invalid: %w", err) // fail closed: every release is refused
	}
	ks, err := newKeySet(keys, require)
	if err != nil {
		return keySet{}, fmt.Errorf("update keys invalid: %w", err)
	}
	ks.release = owner
	return ks, nil
}
