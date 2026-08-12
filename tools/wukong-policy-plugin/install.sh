#!/bin/sh
set -eu

source_file=/opt/im-policy/wk.plugin.im-policy-linux-amd64.wkp
checksum_file=/opt/im-policy/wk.plugin.im-policy-linux-amd64.wkp.sha256
target_directory=/data/plugins
target_file="$target_directory/wk.plugin.im-policy-linux-amd64.wkp"

expected="$(awk '{print $1}' "$checksum_file")"
actual="$(sha256sum "$source_file" | awk '{print $1}')"
if [ "$actual" != "$expected" ]; then
  echo "WuKongIM policy plugin checksum mismatch" >&2
  exit 1
fi

mkdir -p "$target_directory"
chown 10001:10001 "$target_directory"
chmod 0750 "$target_directory"
temporary="$target_file.tmp"
cp "$source_file" "$temporary"
chmod 0555 "$temporary"
mv -f "$temporary" "$target_file"
printf '%s  %s\n' "$expected" "$(basename "$target_file")" > "$target_directory/SHA256SUMS"
chmod 0444 "$target_directory/SHA256SUMS"
echo "installed checksum-verified built-in WuKongIM policy plugin $expected"
