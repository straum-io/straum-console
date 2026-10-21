#!/bin/sh
# Installs and uninstalls straum-console from the archives of
# `make release-snapshot` (dist/), read as file:// URLs, into a temporary
# prefix; fails on any difference from what the scripts promise.
set -eu
cd "$(dirname "$0")/.."
[ -f dist/checksums.txt ] || { echo "run make release-snapshot first" >&2; exit 1; }
version=$(sed -n 's/.*straum-console_\(.*\)_linux_amd64\.tar\.gz$/\1/p' dist/checksums.txt)
[ -n "$version" ] || { echo "no version in dist/checksums.txt" >&2; exit 1; }

prefix=$(mktemp -d)
trap 'rm -rf "$prefix"' EXIT
export CONSOLE_INSTALL_BASE_URL=file://$PWD/dist

sh install.sh --version "v$version" --prefix "$prefix"
"$prefix/bin/straum-console" --version | grep -q "straum-console $version" || { echo "installed binary is not $version" >&2; exit 1; }
[ -f "$prefix/share/straum-console/installed" ]

# A tampered archive is refused.
cp dist/checksums.txt dist/checksums.txt.orig
sed 's/^[0-9a-f]\{8\}/00000000/' dist/checksums.txt.orig >dist/checksums.txt
if sh install.sh --version "v$version" --prefix "$prefix/bad" 2>"$prefix/err"; then
	mv dist/checksums.txt.orig dist/checksums.txt
	echo "a checksum mismatch was installed" >&2
	exit 1
fi
mv dist/checksums.txt.orig dist/checksums.txt
grep -q "checksum mismatch" "$prefix/err" || { echo "refused, but not for the checksum:" >&2; cat "$prefix/err" >&2; exit 1; }

# A version that is not one is refused before any download.
if sh install.sh --version "v1.0.0/../../x" --prefix "$prefix/bad" 2>"$prefix/err"; then
	echo "a malformed version was accepted" >&2
	exit 1
fi
grep -q "not a version" "$prefix/err" || { cat "$prefix/err" >&2; exit 1; }

sh uninstall.sh --prefix "$prefix"
[ ! -e "$prefix/bin/straum-console" ] || { echo "binary left behind" >&2; exit 1; }
[ ! -e "$prefix/share/straum-console" ] || { echo "share/straum-console left behind" >&2; exit 1; }
echo "install-test: ok ($version)"
