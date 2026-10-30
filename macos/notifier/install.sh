#!/bin/sh
set -eu

app=$1
lavagna=$2
case "$lavagna" in
	/*) ;;
	*) echo "lavagna-notifier: expected an absolute lavagna binary path" >&2; exit 2 ;;
esac
src=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

bundle=$work/Lavagna.app
mkdir -p "$bundle/Contents/MacOS" "$bundle/Contents/Resources" "$work/AppIcon.iconset"
swiftc -O -o "$bundle/Contents/MacOS/lavagna-notifier" "$src/main.swift"
for size in 16 32 128 256 512; do
	sips -z $size $size "$src/AppIcon.png" --out "$work/AppIcon.iconset/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z $double $double "$src/AppIcon.png" --out "$work/AppIcon.iconset/icon_${size}x${size}@2x.png" >/dev/null
done
iconutil -c icns "$work/AppIcon.iconset" -o "$bundle/Contents/Resources/AppIcon.icns"
cp "$src/Info.plist" "$bundle/Contents/Info.plist"
printf '%s\n' "$lavagna" > "$bundle/Contents/Resources/lavagna-path"
codesign --force --sign - "$bundle"

mkdir -p "$(dirname "$app")"
rm -rf "$app"
mv "$bundle" "$app"
/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister -f "$app"
open -g "$app" --args authorize
