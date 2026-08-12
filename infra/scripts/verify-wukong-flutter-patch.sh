#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
package_dir="$root_dir/third_party/wukongim/flutter-sdk-1.7.9-patched"
lock_file="$root_dir/third_party/wukongim/versions.lock.json"
patched_files=(
  "lib/manager/connect_manager.dart:b0309e9172b925e1fbd79adab4aa42c73089d72c39c1c9c8771e70036699c0b9"
  "lib/manager/event_manager.dart:824adf231e2c6ddef1b4cc1e883833bc491b28d8f7f252815bddc469018ea61e"
  "lib/proto/proto.dart:060fa3db9c175f986f856c944fedfa958ab0eff0b5db9b0b712c89e7f63c74ca"
  "lib/proto/packet.dart:fe79c11811b1a7414269af8d707fbec857f3b3f58c0dda735269eedce4444fc7"
  "lib/entity/msg.dart:10a02d8b70a33e67cb40ee7aec7101e887e030d34b22d1933e7bbf40d0da4c13"
  "lib/wkim.dart:1ae479b38e70c5bd1a61996e114817d114c74ecd5c304af0892f492573386ab7"
)

for entry in "${patched_files[@]}"; do
  relative_path="${entry%%:*}"
  expected="${entry##*:}"
  actual="$(sha256sum "$package_dir/$relative_path" | awk '{print $1}')"
  if [[ "$actual" != "$expected" ]]; then
    echo "WuKong Flutter patch hash mismatch: file=$relative_path expected=$expected actual=$actual" >&2
    exit 1
  fi
  grep -Fq '"'"$relative_path"'": "'"$expected"'"' "$lock_file"
done

test -s "$package_dir/LICENSE"
grep -Fq 'path: "../../third_party/wukongim/flutter-sdk-1.7.9-patched"' "$root_dir/apps/mobile/pubspec.lock"

echo "WuKong Flutter 1.7.9 patch verified: ${#patched_files[@]} files"
