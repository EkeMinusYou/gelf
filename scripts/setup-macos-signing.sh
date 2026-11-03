#!/usr/bin/env bash
set -euo pipefail
umask 077

: "${RUNNER_TEMP:?This script must run on a GitHub-hosted macOS runner}"
: "${GITHUB_ENV:?GITHUB_ENV is required}"
: "${MACOS_SIGN_P12:?Set the MACOS_SIGN_P12 repository secret}"
: "${MACOS_SIGN_PASSWORD:?Set the MACOS_SIGN_PASSWORD repository secret}"
: "${APPLE_ID:?Set the APPLE_ID repository secret}"
: "${APPLE_APP_SPECIFIC_PASSWORD:?Set the APPLE_APP_SPECIFIC_PASSWORD repository secret}"
: "${APPLE_TEAM_ID:?APPLE_TEAM_ID is required}"
: "${MACOS_SIGN_IDENTITY:?MACOS_SIGN_IDENTITY is required}"
: "${MACOS_NOTARY_PROFILE:?MACOS_NOTARY_PROFILE is required}"

certificate_path="$RUNNER_TEMP/gelf-signing.p12"
keychain_path="$RUNNER_TEMP/gelf-signing.keychain-db"
trap 'rm -f "$certificate_path"' EXIT
keychain_password="$(openssl rand -base64 32)"
echo "::add-mask::$keychain_password"

printf '%s' "$MACOS_SIGN_P12" | base64 --decode > "$certificate_path"
security create-keychain -p "$keychain_password" "$keychain_path"
security set-keychain-settings -lut 21600 "$keychain_path"
security unlock-keychain -p "$keychain_password" "$keychain_path"
security import "$certificate_path" -P "$MACOS_SIGN_PASSWORD" \
  -t cert -f pkcs12 -k "$keychain_path" -T /usr/bin/codesign -T /usr/bin/security
security set-key-partition-list -S apple-tool:,apple:,codesign: \
  -s -k "$keychain_password" "$keychain_path" > /dev/null
security list-keychains -d user -s "$keychain_path"

if ! security find-identity -v -p codesigning \
  | grep -Fq "\"$MACOS_SIGN_IDENTITY\""; then
  echo "The .p12 must contain the configured Developer ID Application identity and its private key." >&2
  exit 1
fi

# Let notarytool validate the credentials before any release artifacts are built.
xcrun notarytool store-credentials "$MACOS_NOTARY_PROFILE" \
  --apple-id "$APPLE_ID" --team-id "$APPLE_TEAM_ID" \
  --password "$APPLE_APP_SPECIFIC_PASSWORD" --keychain "$keychain_path"

echo "MACOS_KEYCHAIN_PATH=$keychain_path" >> "$GITHUB_ENV"
