# Signed macOS releases

The release workflow signs each macOS executable with a Developer ID Application
certificate and submits it to Apple's notary service before GoReleaser creates
archives, checksums, or the Homebrew cask. A signing or notarization failure stops
the release. Linux and Windows builds are unchanged. Snapshot builds skip signing
and notarization and do not require Apple credentials.

## One-time setup

### Export the signing identity

In Keychain Access, open **My Certificates** and select
**Developer ID Application: Yusuke Mineo (8KMDCBT7AD)**. Expand it to confirm that
its private key is present. Export the identity as a password-protected
**Personal Information Exchange (.p12)** file outside this repository.
Export only this identity, not all certificates in the keychain.

A downloaded `.cer` file alone does not include the private key and cannot be
used for CI signing. An Apple Development certificate is not a substitute for
the Developer ID Application identity.

### Create notarization credentials

At [Apple Account](https://account.apple.com/), create an **app-specific password**
under **Sign-In and Security**. Use the Apple Account that belongs to developer
team `8KMDCBT7AD`. This password lets `notarytool` authenticate without your normal
Apple Account password or an interactive two-factor prompt.

### Register GitHub Actions secrets

Set these repository secrets for `EkeMinusYou/gelf`:

| Secret | Value |
| --- | --- |
| `MACOS_SIGN_P12` | Base64-encoded contents of the exported `.p12` file |
| `MACOS_SIGN_PASSWORD` | The password used when exporting that file |
| `APPLE_ID` | The Apple Account email address used for notarization |
| `APPLE_APP_SPECIFIC_PASSWORD` | The app-specific password created above |

With an authenticated GitHub CLI, upload the certificate without printing it:

```bash
base64 < /absolute/path/to/gelf-signing.p12 | gh secret set MACOS_SIGN_P12 --repo EkeMinusYou/gelf
gh secret set MACOS_SIGN_PASSWORD --repo EkeMinusYou/gelf
gh secret set APPLE_ID --repo EkeMinusYou/gelf
gh secret set APPLE_APP_SPECIFIC_PASSWORD --repo EkeMinusYou/gelf
```

The last three commands prompt for their values. Keep credentials out of shell
arguments, chat messages, and the repository. The workflow imports the certificate
into a temporary keychain and deletes that keychain when the job finishes.

The signing identity and Team ID are configured in
`.github/workflows/release.yml`. Update them if the signing team changes.

## Publish and verify

For a CI test before publishing, run the **release** workflow manually and leave
**Publish the GitHub release and update the Homebrew cask** unchecked (the
default). This signs and notarizes the macOS binaries and builds the archives,
but skips publication. It also skips Git tag validation so an untagged branch can
be tested. Do not use snapshot mode for this check: snapshots skip signing.

After the workflow changes and secrets are in place, push a new version tag using
the project's existing release process. The release job runs on macOS and checks
that Apple's final notarization status is `Accepted` for each macOS binary.
Tag-triggered runs publish as before. To publish manually, select the version tag
and enable the publish checkbox.
Notarization has a 30-minute timeout per binary; a timeout stops publication and
prints the submission result so its ID can be checked with `notarytool info`.

Install or upgrade the published cask on a Mac, then check the signature:

```bash
brew update
brew upgrade --cask ekeminusyou/gelf/gelf
codesign --verify --strict --verbose=2 "$(command -v gelf)"
codesign --display --verbose=4 "$(command -v gelf)"
gelf version
```

For a first installation, use `brew install ekeminusyou/gelf/gelf` instead of the
upgrade command. Confirm that the displayed authority is the Developer ID
Application identity and that normal execution succeeds without clearing the
quarantine attribute.

Bare command-line executables cannot have notarization tickets stapled to them.
Gatekeeper retrieves the ticket from Apple, so the first verification may require
internet access. The notarized executable is unchanged when GoReleaser packages
it in the final `.tar.gz` archive. Existing releases remain unchanged; the first
release made with this workflow is the first signed and notarized release.

## References

- [Apple: Customizing the notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow)
- [Apple: Migrating to the latest notarization tool](https://developer.apple.com/documentation/technotes/tn3147-migrating-to-the-latest-notarization-tool)
- [GoReleaser: Build hooks](https://goreleaser.com/customization/builds/hooks/)
- [GitHub: Installing an Apple certificate on macOS runners](https://docs.github.com/en/actions/how-tos/deploy/deploy-to-third-party-platforms/sign-xcode-applications)
