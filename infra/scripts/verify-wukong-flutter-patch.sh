#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
package_dir="$root_dir/third_party/wukongim/flutter-sdk-1.7.9-patched"
lock_file="$root_dir/third_party/wukongim/versions.lock.json"
patched_files=(
  "lib/common/options.dart:e34fb3d3a5d2ee559717af40edaa4fab19c4fa1ffe1f53fd19257358e8becd52"
  "lib/db/wk_db_helper.dart:1f3d571edff23caa3559b1e073223ae90a37db0bfd64ed3435a127bbc15ec7fa"
  "lib/manager/connect_manager.dart:f8ea2d59877e5ffaad026898031605f5b37665ea1203763c1d24603d454651f5"
  "lib/manager/channel_manager.dart:4c5af670d5f0fd63c0ca1fe7e70e60e6b6bb85a1a4e405dd50c040f17f8c14e1"
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
