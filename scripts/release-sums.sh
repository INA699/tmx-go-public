#!/usr/bin/env bash
# CI: writes SHA256SUMS for every file in the current folder and signs it into SHA256SUMS.sig with
# the release key (secret TMX_RELEASE_SIGNING_KEY, an Ed25519 PEM; DESIGN.md "Updates"). Installs
# update themselves only to releases whose SHA256SUMS carries that signature. Works on macOS and
# Linux. Without the key the release stays unsigned (a warning), so installs will not take it.
set -euo pipefail
rm -f SHA256SUMS SHA256SUMS.sig
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum -- * > SHA256SUMS
else
  shasum -a 256 -- * > SHA256SUMS
fi
if [ -z "${TMX_RELEASE_SIGNING_KEY:-}" ]; then
  echo "::warning::TMX_RELEASE_SIGNING_KEY is not set: this release is unsigned, so installs will not update to it"
  cat SHA256SUMS
  exit 0
fi
# Ed25519 signing (pkeyutl -rawin) needs OpenSSL 3; macOS's own openssl is LibreSSL.
openssl=""
for o in openssl /opt/homebrew/opt/openssl@3/bin/openssl /usr/local/opt/openssl@3/bin/openssl /opt/homebrew/bin/openssl; do
  if command -v "$o" >/dev/null 2>&1 && "$o" version 2>/dev/null | grep -q '^OpenSSL 3'; then openssl="$o"; break; fi
done
[ -n "$openssl" ] || { echo "::error::signing needs OpenSSL 3 (macOS: brew install openssl@3)"; exit 1; }
key=$(mktemp)
trap 'rm -f "$key"' EXIT
chmod 600 "$key"
printf '%s\n' "$TMX_RELEASE_SIGNING_KEY" > "$key"
"$openssl" pkeyutl -sign -inkey "$key" -rawin -in SHA256SUMS | base64 | tr -d '\n' > SHA256SUMS.sig
echo >> SHA256SUMS.sig
cat SHA256SUMS
