package update

import (
	"bufio"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/blackcode-ch/tmx-go/internal/config"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
)

// Release integrity: CI writes SHA256SUMS (one "hash  name" line per file of the release) and
// SHA256SUMS.sig. An install accepts a download only when SHA256SUMS carries a valid signature of
// a key it trusts and the download's SHA-256 is the one SHA256SUMS lists. SHA256SUMS.sig comes in
// one of two formats:
//
// The owner's (scripts/release-sums.sh; every release since v0.1.0 and the voice engine): one
// line, the base64 Ed25519 signature of the exact SHA256SUMS bytes (an OpenSSL "pkeyutl -sign
// -rawin") by one of the owner's release keys compiled into this build (keys.go). Its tag is
// bound through SHA256SUMS: the file an install looks for there carries the version in its name.
// This is the only format installs up to v0.1.10 read, so every owner release keeps it.
//
// The extra keys' (only when the owner pinned extra keys in update.fleet_keys; none by default),
// one signature per line (at most maxSigLines):
//
//	<key id>:<type>:<base64 payload>   type "ed25519" or "webauthn"
//	<key id>:<base64 signature>        the original form, an ed25519 signature
//
// ed25519: the payload is the 64-byte Ed25519 signature of signatureMessage(purpose, repo, tag,
// SHA256SUMS): the purpose (FleetSignatureContext or FleetExtraSignatureContext), repository and
// tag, each LF-terminated, then the exact SHA256SUMS bytes (an OpenSSL "pkeyutl -sign -rawin").
//
// webauthn: the payload is JSON {authenticator_data, client_data_json, signature} (base64url), a
// WebAuthn assertion by the pinned credential whose challenge is webauthnChallenge (see there).
//
// A line that does not parse makes the whole file invalid. A line naming a key or type that is
// not pinned, or whose signature does not verify, counts for nothing; the release passes only
// when every type update.require lists has at least one valid signature by a pinned key of that
// type. The require policy is for the extra keys only: it never blocks the owner's signature.
// The two formats cannot be confused: base64 has no ':' and every extra-key line has one.

var errUnsigned = errors.New("the release is not signed with a key this tmx knows")

var errBadSig = errors.New("SHA256SUMS.sig is not a signature")

const maxSigLines = 8

// pinnedKey is a decoded config.FleetKey.
type pinnedKey struct {
	id, typ  string
	ed       ed25519.PublicKey
	wa       webauthncose.EC2PublicKeyData
	rpIDHash [32]byte
	origin   string
}

// keySet is what an install trusts: the owner's release keys (the original format) and the
// decoded pinned extra keys plus their require policy (the extra keys' format).
type keySet struct {
	release []ed25519.PublicKey
	keys    []pinnedKey
	require []string
}

func (ks keySet) empty() bool { return len(ks.release) == 0 && len(ks.keys) == 0 }

// newKeySet decodes validated config keys (the pinned extra keys).
func newKeySet(keys []config.FleetKey, require []string) (keySet, error) {
	ks := keySet{require: require}
	for _, k := range keys {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k.Key))
		if err != nil {
			return keySet{}, errors.New("invalid fleet public key")
		}
		p := pinnedKey{id: k.ID, typ: k.Type}
		switch k.Type {
		case config.KeyEd25519:
			if len(raw) != ed25519.PublicKeySize {
				return keySet{}, errors.New("invalid fleet public key")
			}
			p.ed = ed25519.PublicKey(raw)
		case config.KeyWebAuthn:
			if p.wa, err = config.ParseWebAuthnKey(raw); err != nil {
				return keySet{}, err
			}
			p.rpIDHash, p.origin = sha256.Sum256([]byte(k.RPID)), k.Origin
		default:
			return keySet{}, fmt.Errorf("unknown fleet key type %q", k.Type)
		}
		ks.keys = append(ks.keys, p)
	}
	if len(ks.require) == 0 {
		ks.require = []string{config.KeyEd25519}
	}
	return ks, nil
}

// publicKeys decodes the owner's release keys (base64 Ed25519; keys.go or Options.Keys).
func publicKeys(b64 []string) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	for _, k := range b64 {
		raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(k))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("bad release key %q", k)
		}
		out = append(out, ed25519.PublicKey(raw))
	}
	return out, nil
}

func signatureMessage(purpose, repo, tag string, sums []byte) []byte {
	return append([]byte(purpose+"\n"+repo+"\n"+tag+"\n"), sums...)
}

// webauthnChallenge is the challenge a webauthn release signature must carry:
//
//	SHA-256(context + "\n" + repo + "\n" + tag + "\n" + SHA256SUMS bytes)
//
// with context FleetWebAuthnContext ("tmx-fleet-release-v2") for a release and
// FleetExtraWebAuthnContext ("tmx-fleet-extra-v2") for extras; clientDataJSON.challenge is its
// base64url encoding (no padding), as browsers write it.
func webauthnChallenge(purpose, repo, tag string, sums []byte) ([]byte, bool) {
	var ctx string
	switch purpose {
	case FleetSignatureContext:
		ctx = FleetWebAuthnContext
	case FleetExtraSignatureContext:
		ctx = FleetExtraWebAuthnContext
	case FleetKeysContext:
		ctx = FleetKeysWebAuthnContext
	default:
		return nil, false
	}
	h := sha256.Sum256(signatureMessage(ctx, repo, tag, sums))
	return h[:], true
}

// sigLine is one parsed SHA256SUMS.sig line.
type sigLine struct {
	id, typ string
	payload []byte
}

func parseSigFile(b []byte) ([]sigLine, error) {
	var out []sigLine
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ":")
		var l sigLine
		switch len(parts) {
		case 2:
			l = sigLine{id: parts[0], typ: config.KeyEd25519}
		case 3:
			l = sigLine{id: parts[0], typ: parts[1]}
		default:
			return nil, errBadSig
		}
		raw, err := base64.StdEncoding.DecodeString(parts[len(parts)-1])
		if err != nil || l.id == "" || l.typ == "" || len(raw) == 0 {
			return nil, errBadSig
		}
		if l.typ == config.KeyEd25519 && len(raw) != ed25519.SignatureSize {
			return nil, errBadSig
		}
		l.payload = raw
		out = append(out, l)
	}
	if len(out) == 0 || len(out) > maxSigLines {
		return nil, errBadSig
	}
	return out, nil
}

// ownerSig returns the signature of a SHA256SUMS.sig in the owner's format: one line, no ':'.
// ok is false for the extra keys' format; err is set when it is the owner's format but not a
// signature.
func ownerSig(b []byte) (sig []byte, ok bool, err error) {
	s := strings.TrimSpace(string(b))
	if s == "" || strings.Contains(s, ":") {
		return nil, false, nil
	}
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(raw) != ed25519.SignatureSize {
		return nil, true, errBadSig
	}
	return raw, true, nil
}

// verifySums checks sig over sums: the owner's signature of sums by a release key, or the extra
// keys' signatures of the purpose, repository, tag and sums under their policy (see above). A
// rotation statement (FleetKeysContext) is for the extra keys only, so it never takes the
// owner's format.
func verifySums(purpose, repo, tag string, sums, sig []byte, ks keySet) error {
	if raw, ok, err := ownerSig(sig); ok {
		if err != nil {
			return err
		}
		if purpose == FleetKeysContext {
			return errUnsigned
		}
		for _, k := range ks.release {
			if ed25519.Verify(k, sums, raw) {
				return nil
			}
		}
		return errUnsigned
	}
	if repo == "" || tag == "" || strings.ContainsAny(repo+tag, "\r\n") || len(ks.keys) == 0 {
		return errUnsigned
	}
	lines, err := parseSigFile(sig)
	if err != nil {
		return err
	}
	valid := map[string]bool{}
	for _, l := range lines {
		for _, k := range ks.keys {
			if k.id != l.id || k.typ != l.typ || valid[k.typ] {
				continue
			}
			switch k.typ {
			case config.KeyEd25519:
				valid[k.typ] = ed25519.Verify(k.ed, signatureMessage(purpose, repo, tag, sums), l.payload)
			case config.KeyWebAuthn:
				valid[k.typ] = verifyWebAuthn(k, purpose, repo, tag, sums, l.payload) == nil
			}
		}
	}
	for _, t := range ks.require {
		if !valid[t] {
			return errUnsigned
		}
	}
	return nil
}

// parseSums reads SHA256SUMS (sha256sum's format: "<hex>  <name>" or "<hex> *<name>").
func parseSums(b []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 2 || len(f[0]) != 64 {
			continue
		}
		out[strings.TrimPrefix(f[1], "*")] = strings.ToLower(f[0])
	}
	return out
}
