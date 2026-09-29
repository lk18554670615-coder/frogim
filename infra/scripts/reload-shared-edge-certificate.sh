#!/bin/sh
# Run on the existing Linux host after successful certificate renewal.
# Does not restart business services or modify certificates.
set -eu
compose=${1:?absolute edge compose JSON required}
case "$compose" in /*) ;; *) echo 'absolute compose path required' >&2; exit 1;; esac
test -f "$compose"
docker compose --project-name frogim-edge --env-file /dev/null --file "$compose" exec -T gateway caddy validate --config /config/Caddyfile --adapter caddyfile
docker compose --project-name frogim-edge --env-file /dev/null --file "$compose" exec -T gateway caddy reload --force --address 127.0.0.1:2019 --config /config/Caddyfile --adapter caddyfile
