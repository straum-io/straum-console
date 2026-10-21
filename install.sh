#!/bin/sh
# Installs straum-console from a GitHub release: the archive for this OS and
# CPU, checked against the release's checksums.txt.
#
#   curl -fsSL https://raw.githubusercontent.com/straum-io/straum-console/main/install.sh | sh
#   sh install.sh [--version vX.Y.Z] [--prefix DIR | --user] [--systemd]
#
# --prefix DIR   install under DIR (default /usr/local; sudo if needed)
# --user         install under ~/.local, no sudo
# --systemd      Linux: also install the example unit (not enabled; set the
#                nodes in it first)
#
# What it installs is listed in <prefix>/share/straum-console/installed,
# which uninstall.sh reads. CONSOLE_INSTALL_BASE_URL replaces the download
# base (for a mirror, or a local `make release-snapshot`).
set -eu

repo=straum-io/straum-console
version=
prefix=/usr/local
systemd=0

die() { echo "install.sh: $*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case $1 in
	--version) [ $# -ge 2 ] || die "--version needs a value"; version=$2; shift 2 ;;
	--prefix) [ $# -ge 2 ] || die "--prefix needs a value"; prefix=$2; shift 2 ;;
	--user) prefix=$HOME/.local; shift ;;
	--systemd) systemd=1; shift ;;
	-h | --help) sed -n '2,17p' "$0" 2>/dev/null || true; exit 0 ;;
	*) die "unknown option $1" ;;
	esac
done

case $(uname -s) in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "no build for $(uname -s); see the release page for Windows" ;;
esac
case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "no build for CPU $(uname -m)" ;;
esac
[ "$systemd" = 0 ] || [ "$os" = linux ] || die "--systemd is for Linux"

# HTTPS only, redirects included; a mirror or a local snapshot set by
# CONSOLE_INSTALL_BASE_URL may also be http or file.
protos='=https'
[ -z "${CONSOLE_INSTALL_BASE_URL:-}" ] || protos='=https,http,file'
fetch() { curl -fsSL --proto "$protos" "$1" -o "$2" || die "could not download $1"; }

if [ -z "$version" ]; then
	[ -z "${CONSOLE_INSTALL_BASE_URL:-}" ] || die "give --version with CONSOLE_INSTALL_BASE_URL"
	version=$(curl -fsSL --proto '=https' "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	[ -n "$version" ] || die "no release of $repo is published yet; build from source (make build)"
fi
# A version goes into URLs and paths: only vX.Y.Z with an optional suffix.
case $version in
v[0-9]*.[0-9]*.[0-9]* | [0-9]*.[0-9]*.[0-9]*) ;;
*) die "not a version: $version" ;;
esac
case $version in *[!A-Za-z0-9.+-]*) die "not a version: $version" ;; esac
case $version in v*) ;; *) version=v$version ;; esac
base=${CONSOLE_INSTALL_BASE_URL:-https://github.com/$repo/releases/download/$version}
archive=straum-console_${version#v}_${os}_${arch}.tar.gz

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
fetch "$base/$archive" "$tmp/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt"

want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive is not in checksums.txt"
if command -v sha256sum >/dev/null; then
	got=$(sha256sum "$tmp/$archive" | awk '{ print $1 }')
else
	got=$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')
fi
[ "$got" = "$want" ] || die "checksum mismatch for $archive: refusing to install"
tar -xzf "$tmp/$archive" -C "$tmp"

# sudo only where the prefix is not writable.
sudo=
mkdir -p "$prefix" 2>/dev/null || true
if [ ! -w "$prefix" ] || { [ "$systemd" = 1 ] && [ ! -w /etc/systemd/system ]; }; then
	command -v sudo >/dev/null || die "$prefix is not writable and there is no sudo"
	sudo=sudo
fi

manifest=$prefix/share/straum-console/installed
$sudo mkdir -p "$prefix/bin" "$prefix/share/straum-console"
$sudo install -m 0755 "$tmp/straum-console" "$prefix/bin/straum-console"
$sudo install -m 0644 "$tmp/LICENSE" "$tmp/README.md" "$tmp/CHANGELOG.md" "$prefix/share/straum-console/"
installed="$prefix/bin/straum-console
$prefix/share/straum-console/LICENSE
$prefix/share/straum-console/README.md
$prefix/share/straum-console/CHANGELOG.md"

if [ "$systemd" = 1 ]; then
	unit=/etc/systemd/system/straum-console.service
	sed "s|^ExecStart=.*|ExecStart=$prefix/bin/straum-console|" "$tmp/docs/straum-console.service" >"$tmp/unit"
	$sudo install -m 0644 "$tmp/unit" "$unit"
	installed="$installed
$unit"
	! command -v systemctl >/dev/null || $sudo systemctl daemon-reload || true
fi
printf '%s\n%s\n' "$installed" "$manifest" | $sudo tee "$manifest" >/dev/null

echo "straum-console $version installed: $prefix/bin/straum-console"
case :$PATH: in *":$prefix/bin:"*) ;; *) echo "add $prefix/bin to PATH" ;; esac
if [ "$systemd" = 1 ]; then
	echo "set the nodes in $unit (STRAUM_CONSOLE_NODES), then: sudo systemctl enable --now straum-console"
fi
