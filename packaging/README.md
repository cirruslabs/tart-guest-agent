# Release signing

GoReleaser signs the final Darwin universal executable in its post-merge hook,
before creating archives and checksums. Both slices must use the identifier
`org.cirruslabs.tart-guest-agent` and pass strict signature verification.
Linux builds and packages are not signed by this hook.

Tagged releases require the existing Cirrus Labs Developer ID Application
identity (team `9M2P8L4D89`). Configure `MACOS_SIGN_P12` (base64-encoded PKCS#12)
and `MACOS_SIGN_PASSWORD` in the protected GitHub `publish` environment using
the approved credential-provisioning process. The release job imports the
identity into a temporary keychain and fails if it is missing or invalid.
There is no unsigned or ad-hoc release fallback. This signs the executable;
it does not add Apple notarization.

Secretless validation on macOS:

```sh
bash packaging/test-darwin-signing.sh
goreleaser release --skip=publish --snapshot --clean
bash packaging/verify-darwin-archive.sh snapshot dist
```

For a non-publishing Developer ID check, copy a built universal executable,
set `MACOS_SIGN_IDENTITY` to an existing valid signing identity (and optionally
`MACOS_SIGN_KEYCHAIN`), then run `bash packaging/sign-darwin.sh release <copy>`.
Do not sign an already checksummed release artifact in place.
