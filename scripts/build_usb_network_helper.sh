#!/usr/bin/env bash
# Run inside the JetKVM Linux buildkit, using the firmware's uClibc toolchain.
set -euo pipefail
version=2.93
archive_sha256=cc967771abdafeb43d10db18932d6b59fd4bed2c69c22acf8cb96aff6920d55f
buildkit=${BUILDKIT_PATH:-/opt/jetkvm-native-buildkit}
compiler=${USB_HELPER_CC:-$buildkit/bin/arm-rockchip830-linux-uclibcgnueabihf-gcc}
output=${1:-bin/dnsmasq}
helper_work=$(mktemp -d)
trap 'rm -rf "$helper_work"' EXIT
curl -fsSL --max-time 60 "https://thekelleys.org.uk/dnsmasq/dnsmasq-$version.tar.gz" -o "$helper_work/source.tar.gz"
printf '%s  %s\n' "$archive_sha256" "$helper_work/source.tar.gz" | sha256sum -c -
tar -xf "$helper_work/source.tar.gz" -C "$helper_work"
make -C "$helper_work/dnsmasq-$version" -j4 CC="$compiler" CFLAGS=-Os LDFLAGS=-Wl,--gc-sections \
    COPTS='-DNO_IDN -DNO_DBUS -DNO_UBUS -DNO_LUASCRIPT -DNO_DNSSEC -DNO_TFTP -DNO_SCRIPT -DNO_AUTH -DNO_IPSET -DNO_NFTSET'
mkdir -p "$(dirname "$output")"
cp "$helper_work/dnsmasq-$version/src/dnsmasq" "$output"
"${compiler%-gcc}-strip" "$output"
sha256sum "$output"
