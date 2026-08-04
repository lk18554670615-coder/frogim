#!/usr/bin/env bash

load_env_file() {
  local env_file="$1" line key value
  if [[ ! -f "$env_file" ]]; then
    echo "missing environment file: $env_file" >&2
    return 1
  fi
  while IFS= read -r line || [[ -n "$line" ]]; do
    line="${line%$'\r'}"
    [[ -z "$line" || "$line" =~ ^[[:space:]]*# ]] && continue
    if [[ "$line" != *=* ]]; then
      echo "invalid environment line (expected KEY=VALUE)" >&2
      return 1
    fi
    key="${line%%=*}"
    value="${line#*=}"
    if [[ ! "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      echo "invalid environment variable name: $key" >&2
      return 1
    fi
    if (( ${#value} >= 2 )) && { [[ "$value" == \'*\' ]] || [[ "$value" == \"*\" ]]; }; then
      value="${value:1:${#value}-2}"
    fi
    export "$key=$value"
  done < "$env_file"
}
