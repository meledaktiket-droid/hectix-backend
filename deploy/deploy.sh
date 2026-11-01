#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")"
git pull --ff-only
docker compose build
docker compose run --rm api migrate
docker compose up -d --remove-orphans
docker image prune -f >/dev/null
docker compose ps
