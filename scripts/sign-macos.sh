#!/usr/bin/env bash
set -euo pipefail

binary_path="${1:?Usage: sign-macos.sh BINARY GOOS IS_SNAPSHOT}"
target_os="${2:?Target OS is required}"
is_snapshot="${3:?Snapshot flag is required}"

if [[ "$target_os" != darwin || "$is_snapshot" == true ]]; then
  exit 0
fi

: "${MACOS_SIGN_IDENTITY:?MACOS_SIGN_IDENTITY is required for macOS releases}"
: "${MACOS_NOTARY_PROFILE:?MACOS_NOTARY_PROFILE is required for macOS releases}"

# Guard empty array expansions below for macOS's Bash 3.2 with nounset enabled.
keychain_args=()
if [[ -n "${MACOS_KEYCHAIN_PATH:-}" ]]; then
  keychain_args=(--keychain "$MACOS_KEYCHAIN_PATH")
fi

codesign --force --timestamp --options runtime \
  --identifier com.github.EkeMinusYou.gelf \
  --sign "$MACOS_SIGN_IDENTITY" ${keychain_args[@]+"${keychain_args[@]}"} "$binary_path"
codesign --verify --strict --verbose=2 "$binary_path"

notary_dir="$(mktemp -d "${TMPDIR:-/tmp}/gelf-notary.XXXXXX")"
trap 'rm -rf "$notary_dir"' EXIT
ditto -c -k "$binary_path" "$notary_dir/gelf.zip"

# A bare CLI executable cannot carry a stapled ticket. Gatekeeper retrieves the
# ticket from Apple using the signed binary's hash, including after tar.gz extraction.
submit_status=0
xcrun notarytool submit "$notary_dir/gelf.zip" \
  --keychain-profile "$MACOS_NOTARY_PROFILE" ${keychain_args[@]+"${keychain_args[@]}"} \
  --wait --timeout 30m --output-format json > "$notary_dir/result.json" \
  || submit_status=$?
cat "$notary_dir/result.json"

notary_status="$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1])).get("status", ""))' "$notary_dir/result.json")"
if [[ "$submit_status" != 0 || "$notary_status" != Accepted ]]; then
  submission_id="$(python3 -c 'import json, sys; print(json.load(open(sys.argv[1])).get("id", ""))' "$notary_dir/result.json")"
  if [[ -n "$submission_id" ]]; then
    xcrun notarytool log "$submission_id" \
      --keychain-profile "$MACOS_NOTARY_PROFILE" ${keychain_args[@]+"${keychain_args[@]}"} || true
  fi
  echo "Notarization did not succeed; refusing to publish $binary_path." >&2
  exit 1
fi
