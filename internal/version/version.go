// Package version pins cc-fuse's release version — the single source of
// truth for the boot log (and any future About surface); the git tag
// v<Version> mirrors it when cutting a release, and CHANGELOG.md carries
// the details.
package version

// Version is the current release, semver without the leading v.
const Version = "0.4.2"
