#!/bin/sh
# Makes the files of one GitHub release of this firmware, next to bin/jetkvm_app:
#   jetkvm_app.sig  its signature (the key in internal/ota/update_key.go)
#   releases        what devices download to learn the newest version; this exact name,
#                   because they ask for <repo>/releases/latest/download/releases
# Usage: scripts/fork_release.sh <version> <signing key fingerprint> [owner/repo]
# Build first, with the same version:  make frontend && make build_release VERSION=<version>
set -eu
version=$1 key=$2 repo=${3:-imkebe/kvm}
cd "$(dirname "$0")/../bin"
# A release whose binary reports another version makes devices update again and again.
grep -aq "$version" jetkvm_app || { echo "bin/jetkvm_app was not built as $version" >&2; exit 1; }
hash=$( (sha256sum jetkvm_app 2>/dev/null || shasum -a 256 jetkvm_app) | cut -d' ' -f1)
# loopback: the passphrase is asked on the terminal itself; the curses prompt does not show in every terminal.
gpg --pinentry-mode loopback --yes --detach-sign --local-user "$key" --output jetkvm_app.sig jetkvm_app
url=https://github.com/$repo/releases/download/$version
# systemVersion 0.0.0: never newer than a device's, so the system image is not updated this way.
printf '{"appVersion":"%s","appUrl":"%s/jetkvm_app","appHash":"%s","appSigUrl":"%s/jetkvm_app.sig","systemVersion":"0.0.0","systemUrl":"","systemHash":""}\n' \
	"$version" "$url" "$hash" "$url" >releases
echo "Publish as a normal release, not a pre-release (devices follow \"latest\"):"
echo "  gh release create $version --repo $repo --title $version bin/jetkvm_app bin/jetkvm_app.sig bin/releases"
