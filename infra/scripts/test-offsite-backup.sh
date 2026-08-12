#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
temporary_root="$(mktemp -d)"
cleanup() {
  local target="$temporary_root"
  if [[ -n "$target" && "$target" == /tmp/* && -d "$target" ]]; then
    rm -rf -- "$target"
  fi
}
trap cleanup EXIT

backup_root="$temporary_root/backups"
remote_root="$temporary_root/remote"
fake_bin="$temporary_root/bin"
mkdir -p "$backup_root" "$remote_root" "$fake_bin"

make_generation() {
  local generation="$1" remote="$remote_root/$1"
  mkdir -p "$remote/minio"
  printf 'postgres fixture\n' > "$remote/postgres.dump"
  printf 'wukong fixture\n' > "$remote/wukongim-data.tar.gz"
  printf 'media fixture\n' > "$remote/minio/object.bin"
  cp "$ROOT_DIR/third_party/wukongim/versions.lock.json" "$remote/versions.lock.json"
  (
    cd "$remote"
    find . -type f ! -name SHA256SUMS ! -name _COMPLETE -print0 |
      sort -z |
      xargs -0 sha256sum > SHA256SUMS
  )
  cp "$remote/SHA256SUMS" "$remote/_COMPLETE"
}

cat > "$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
generation=""
download=""
for argument in "$@"; do
  case "$argument" in
    BACKUP_GENERATION=*) generation="${argument#BACKUP_GENERATION=}" ;;
    BACKUP_DOWNLOAD_DIRECTORY=*) download="${argument#BACKUP_DOWNLOAD_DIRECTORY=}" ;;
  esac
done
if [[ -z "$generation" || -z "$download" ]]; then
  echo "fake docker did not receive the expected download variables" >&2
  exit 2
fi
cp -a "$FAKE_REMOTE_ROOT/$generation/." "$BACKUP_DIR/$download/"
EOF
chmod 700 "$fake_bin/docker"

environment_file="$temporary_root/production.env"
cat > "$environment_file" <<EOF
BACKUP_DIR=$backup_root
BACKUP_OFFSITE_ENABLED=true
BACKUP_OFFSITE_ENDPOINT=https://backup.example.test
BACKUP_OFFSITE_ACCESS_KEY=test-access-key
BACKUP_OFFSITE_SECRET_KEY=test-secret-key-value
BACKUP_OFFSITE_BUCKET=nexachat-backups
BACKUP_OFFSITE_PREFIX=linli-im
PRODUCTION_ENDPOINT_MODE=domain
EOF

generation=20260812T120000Z
make_generation "$generation"
PATH="$fake_bin:$PATH" FAKE_REMOTE_ROOT="$remote_root" \
  bash "$SCRIPT_DIR/fetch-offsite-backup.sh" "$generation" "$environment_file"

published="$backup_root/$generation"
[[ -d "$published" && ! -e "$published/_COMPLETE" ]]
(cd "$published" && sha256sum -c SHA256SUMS >/dev/null)
cmp -s "$published/versions.lock.json" "$ROOT_DIR/third_party/wukongim/versions.lock.json"

corrupt_generation=20260812T120100Z
make_generation "$corrupt_generation"
printf 'corruption\n' >> "$remote_root/$corrupt_generation/postgres.dump"
if PATH="$fake_bin:$PATH" FAKE_REMOTE_ROOT="$remote_root" \
  bash "$SCRIPT_DIR/fetch-offsite-backup.sh" "$corrupt_generation" "$environment_file" \
  >"$temporary_root/corrupt.stdout" 2>"$temporary_root/corrupt.stderr"; then
  echo "off-site fetch published a generation with a checksum mismatch" >&2
  exit 1
fi
[[ ! -e "$backup_root/$corrupt_generation" ]]
[[ -d "$backup_root/.offsite-download-$corrupt_generation" ]]
grep -Eq 'FAILED|checksum' "$temporary_root/corrupt.stdout" "$temporary_root/corrupt.stderr"

unsafe_environment="$temporary_root/unsafe.env"
cat > "$unsafe_environment" <<EOF
BACKUP_DIR=$backup_root
BACKUP_OFFSITE_ENABLED=true
BACKUP_OFFSITE_ENDPOINT=http://backup.example.test
BACKUP_OFFSITE_ACCESS_KEY=test-access-key
BACKUP_OFFSITE_SECRET_KEY=test-secret-key-value
BACKUP_OFFSITE_BUCKET=nexachat-backups
BACKUP_OFFSITE_PREFIX=../escape
PRODUCTION_ENDPOINT_MODE=domain
EOF
if PATH="$fake_bin:$PATH" FAKE_REMOTE_ROOT="$remote_root" \
  bash "$SCRIPT_DIR/fetch-offsite-backup.sh" 20260812T120200Z "$unsafe_environment" \
  >"$temporary_root/unsafe.stdout" 2>"$temporary_root/unsafe.stderr"; then
  echo "off-site fetch accepted an unsafe endpoint or object prefix" >&2
  exit 1
fi
grep -q 'off-site backup configuration is unsafe' "$temporary_root/unsafe.stderr"

echo "off-site backup fetch integrity verification passed"
