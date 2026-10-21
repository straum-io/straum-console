#!/bin/sh
# Removes what install.sh installed, as listed in its manifest; stops and
# disables the systemd unit first if it was installed.
#
#   sh uninstall.sh [--prefix DIR | --user]
set -eu

prefix=/usr/local
die() { echo "uninstall.sh: $*" >&2; exit 1; }
while [ $# -gt 0 ]; do
	case $1 in
	--prefix) [ $# -ge 2 ] || die "--prefix needs a value"; prefix=$2; shift 2 ;;
	--user) prefix=$HOME/.local; shift ;;
	-h | --help) sed -n '2,6p' "$0" 2>/dev/null || true; exit 0 ;;
	*) die "unknown option $1" ;;
	esac
done

manifest=$prefix/share/straum-console/installed
[ -f "$manifest" ] || die "nothing installed under $prefix (no $manifest)"

sudo=
[ -w "$prefix/share/straum-console" ] || sudo=sudo
unit=0
if grep -qx '/etc/systemd/system/straum-console.service' "$manifest"; then
	unit=1
	[ -w /etc/systemd/system ] || sudo=sudo
	if command -v systemctl >/dev/null; then
		$sudo systemctl disable --now straum-console 2>/dev/null || true
	fi
fi

# Only the paths the manifest lists, and only under the prefix or the unit.
while IFS= read -r path; do
	case $path in
	*/../* | */..) echo "skipped $path: not a plain path" >&2 ;;
	"$prefix"/* | /etc/systemd/system/straum-console.service) $sudo rm -f -- "$path" && echo "removed $path" ;;
	"") ;;
	*) echo "skipped $path: not under $prefix" >&2 ;;
	esac
done <"$manifest"
$sudo rmdir "$prefix/share/straum-console" 2>/dev/null || true
if [ "$unit" = 1 ] && command -v systemctl >/dev/null; then
	$sudo systemctl daemon-reload 2>/dev/null || true
fi
echo "straum-console uninstalled"
