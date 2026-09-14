#!/usr/bin/env bash
# petronet.mn-ийг барьж, шинэчлэх. Серверийн /opt/petronet/src дотроос
# ажиллана.
#
#   cd /opt/petronet/src && git pull && ./deploy.sh
#
# Юу хийдэг вэ: энэ бүтээгдэхүүний backend болон Fuel UI-тай web образыг
# барина, стекийг солиод эрүүл эсэхийг асууна.
set -euo pipefail

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_DIR="$(dirname "$SRC_DIR")"

[ -f "$APP_DIR/.env" ] || { echo "$APP_DIR/.env алга. .env.example-ээс хуулж, нууцуудыг нь бөглө." >&2; exit 1; }

# -q биш: чимээгүй build унавал зөвхөн «failed to solve» хэвлэгдэж, шалтгаан
# (npm, compiler) харагдахгүй.
docker build -t petronet:latest -f "$SRC_DIR/deploy/Dockerfile" "$SRC_DIR"
docker build -t petronet-web:latest "$SRC_DIR/frontend"

# Бүрхүүл ./brand-ийг nginx-ээр өгдөг тул байхгүй бол лого 404. chmod нь сайн
# дурын биш: www-data 0700 хавтас дотор орж чадахгүй.
mkdir -p "$APP_DIR/brand" && chmod 755 "$APP_DIR/brand"

cp "$APP_DIR/.env" "$SRC_DIR/deploy/.env"
docker compose -f "$SRC_DIR/deploy/docker-compose.yml" up -d --remove-orphans

# Шалгалт: гурван хариу — API эрүүл, бүрхүүл ирж байна, брэнд .env-ийнхээ
# нэрийг хэлж байна. Гурав дахь нь энэ байрлуулалтыг цөмийн анхдагчаас ялгадаг
# цорын ганц зүйл тул сайн дурын биш.
for i in $(seq 1 30); do
  curl -fsS http://127.0.0.1:8098/health >/dev/null 2>&1 && break
  [ "$i" -eq 30 ] && { echo "backend 60 секундэд эрүүл болсонгүй" >&2; docker compose -f "$SRC_DIR/deploy/docker-compose.yml" logs --tail 40 backend >&2; exit 1; }
  sleep 2
done

# `|| true`: мөр байхгүй бол grep 1 буцааж, pipefail скриптийг чимээгүй зогсооно.
brand="$(grep -E '^BRAND_NAME=' "$APP_DIR/.env" | cut -d= -f2- || true)"
for i in $(seq 1 30); do
  if body="$(curl -fsS http://127.0.0.1:3018/login 2>/dev/null)"; then
    [ -z "$brand" ] && break
    case "$body" in *"$brand"*) break ;; esac
  fi
  [ "$i" -eq 30 ] && { echo "бүрхүүл 60 секундэд «${brand:-хариу}» өгсөнгүй" >&2; exit 1; }
  sleep 2
done

echo "OK: $(grep -E '^PUBLIC_ORIGIN=' "$APP_DIR/.env" | cut -d= -f2) — $brand"
