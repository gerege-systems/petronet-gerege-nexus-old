#!/usr/bin/env bash
#
# PetroNet System — шинэ системийн анхны тохиргоо.
#
# Энэ файл байгаагийн шалтгаан: анхны тохиргоо нь shell дээрх нэг удаагийн
# сесс байсан бөгөөд түүний үр дүн нь хагас дуусдаг байв. Grafana-гийн OAuth
# клиент бүртгэгдсэн атлаа `.env`-д итгэмжлэл нь хэзээ ч бичигдээгүй — нэвтрэх
# товч ажиллахгүй, гэхдээ хэн ч мэдэхгүй хэдэн сар өнгөрсөн. Хийсэн зүйл нь
# скрипт биш бол дараагийн систем дээр давтагдахгүй.
#
# Хоёр үе шат. Дунд нь хүн гар аргаар хийх ёстой хоёр алхам байгаа бөгөөд тэр
# нь санаатай: эхний байгууллага ба эхний оператор хоёрын нууц үг терминал дээр
# бичигдэнэ, флаг эсвэл орчны хувьсагчаар биш. Тэр хоёр нь shell-ийн түүх,
# процессын жагсаалт, контейнерийн inspect гурвуулаад үлддэг.
#
#   ./deploy/scripts/first-run.sh prepare   # (сонголтоор) цэвэрлэх, миграц, .env
#   docker exec -it gerege_petronet_backend /app/tenant-bootstrap …
#   docker exec -it gerege_petronet_backend /app/operator-bootstrap …
#   PETRONET_OVERSIGHT_SLUGS=<яамны slug>,<агентлагийн slug> \
#       ./deploy/scripts/first-run.sh finish
#
# Өгөгдлийг устгах нь `PETRONET_WIPE=yes-i-mean-it` гэж ЗӨВХӨН тодорхой
# хэлсэн үед л явагдана. Түүнгүйгээр `prepare` нь байгаа сан дээр миграц ба
# тохиргоог л хийнэ.
set -euo pipefail

SRC_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
APP_DIR="$(dirname "$SRC_DIR")"
ENV_FILE="$APP_DIR/.env"
COMPOSE="docker compose -f $SRC_DIR/deploy/docker-compose.yml"
PG=gerege_petronet_postgres
BACKEND=gerege_petronet_backend

[ -f "$ENV_FILE" ] || { echo "$ENV_FILE алга." >&2; exit 1; }

psql_db() { docker exec -i "$PG" psql -v ON_ERROR_STOP=1 -U postgres -d platform_db "$@"; }

# .env-ийн түлхүүрийг тавих: байвал солино, байхгүй бол нэмнэ.
set_env() {
  local key="$1" value="$2"
  if grep -q "^${key}=" "$ENV_FILE"; then
    sed -i "s|^${key}=.*|${key}=${value}|" "$ENV_FILE"
  else
    printf '%s=%s\n' "$key" "$value" >> "$ENV_FILE"
  fi
}

# --------------------------------------------------------------------- prepare

prepare() {
  echo "→ нөөцлөлт"
  mkdir -p /var/backups/petronet-firstrun
  local dump="/var/backups/petronet-firstrun/before-first-run-$(date +%Y%m%d-%H%M%S).sql.gz"
  docker exec "$PG" pg_dump -U postgres -d platform_db | gzip > "$dump"
  # Уншиж чадахгүй нөөц нь нөөц биш: gzip-ийн бүрэн бүтэн байдлыг шалгана.
  gzip -t "$dump"
  echo "  $dump ($(du -h "$dump" | cut -f1))"

  if [ "${PETRONET_WIPE:-}" = "yes-i-mean-it" ]; then
    echo "→ өгөгдлийн санг цэвэрлэж байна"
    $COMPOSE stop backend web >/dev/null
    docker exec "$PG" psql -U postgres -d postgres \
      -c "DROP DATABASE platform_db WITH (FORCE)" \
      -c "CREATE DATABASE platform_db OWNER postgres"
    echo "  platform_db шинээр үүслээ"
  else
    echo "→ цэвэрлэхгүй (PETRONET_WIPE тавиагүй) — байгаа сан дээр үргэлжилнэ"
  fi

  echo "→ миграц ба стек"
  cp "$ENV_FILE" "$SRC_DIR/deploy/.env"
  $COMPOSE up -d --remove-orphans >/dev/null
  local i
  for i in $(seq 1 60); do
    curl -fsS http://127.0.0.1:8098/health >/dev/null 2>&1 && break
    [ "$i" -eq 60 ] && { echo "backend эрүүл болсонгүй" >&2; $COMPOSE logs --tail 40 backend >&2; exit 1; }
    sleep 2
  done
  echo "  backend эрүүл"

  cat <<'NEXT'

Дараагийн хоёр алхмыг ГАРААР — нууц үг терминал дээр бичигдэнэ:

  docker exec -it gerege_petronet_backend /app/tenant-bootstrap \
      -org "Аж үйлдвэр, эрдэс баялгийн яам" -slug aueby \
      -email та@жишээ.mn -name "Таны нэр"

  docker exec -it gerege_petronet_backend /app/operator-bootstrap \
      -email та@жишээ.mn -name "Таны нэр"

Дараа нь:  ./deploy/scripts/first-run.sh finish
NEXT
}

# ---------------------------------------------------------------------- finish

finish() {
  echo "→ Grafana-гийн нэвтрэлт"
  # Клиентийг платформ өөрөө бүртгэдэг; энд хийх зүйл нь нууцыг сэлгэж, түүнийг
  # .env рүү бичих — яг энэ алхам нь өмнө нь орхигдож, товч нь ажиллахгүй байсан.
  # Нууцыг эхлээд үүсгэнэ: `oauth2_clients_secret_matches_type` шалгуур нь
  # confidential клиентийг нууцгүйгээр оруулахыг татгалздаг тул «оруулаад дараа
  # нь сэлгэх» гэсэн дараалал ажиллахгүй.
  local cid secret hash owner
  secret="$(openssl rand -hex 32)"
  hash="$(printf '%s' "$secret" | sha256sum | cut -d' ' -f1)"
  cid="$(psql_db -tAc "SELECT client_id FROM workspace.oauth2_clients WHERE client_id LIKE 'grafana-%' ORDER BY created_at LIMIT 1" || true)"

  if [ -z "$cid" ]; then
    # Эзэмшигч нь эхний байгууллага: OAuth клиент тенантад харьяалагддаг ба
    # ажиглалт нь платформын өөрийн хэрэгсэл тул системийг нээсэн байгууллага
    # түүнийг эзэмшинэ.
    owner="$(psql_db -tAc "SELECT id::text FROM registry.tenants WHERE kind = 'organisation' ORDER BY created_at LIMIT 1" || true)"
    if [ -z "$owner" ]; then
      echo "  байгууллага үүсээгүй тул Grafana-гийн клиентийг үүсгэсэнгүй"
    else
      cid="grafana-monitor-$(openssl rand -hex 8)"
      psql_db -c "
        INSERT INTO workspace.oauth2_clients
               (tenant_id, client_id, client_secret_hash, client_name, client_type,
                redirect_uris, grant_types, scopes, post_logout_redirect_uris)
        VALUES ('$owner'::uuid, '$cid', '$hash', 'Ажиглалт (Grafana)', 'confidential',
                ARRAY['https://monitor.petronet.mn/grafana/login/generic_oauth'],
                ARRAY['authorization_code','refresh_token'],
                ARRAY['openid','profile','email','roles'],
                ARRAY['https://monitor.petronet.mn/grafana/login'])" >/dev/null
      echo "  клиент үүслээ: $cid"
    fi
  else
    psql_db -c "UPDATE workspace.oauth2_clients
                   SET client_secret_hash = '$hash', secret_rotated_at = NOW(), updated_at = NOW()
                 WHERE client_id = '$cid'" >/dev/null
    echo "  нууц сэлгэгдлээ: $cid"
  fi

  if [ -n "$cid" ]; then
    set_env GRAFANA_OAUTH_ENABLED true
    set_env GRAFANA_OAUTH_NAME PetroNet
    set_env GRAFANA_OAUTH_CLIENT_ID "$cid"
    set_env GRAFANA_OAUTH_CLIENT_SECRET "$secret"
    set_env OAUTH_REDIRECT_HOSTS monitor.petronet.mn
    chmod 600 "$ENV_FILE"
    cp "$ENV_FILE" "$SRC_DIR/deploy/.env"
    $COMPOSE up -d grafana >/dev/null
    echo "  .env-д бичигдэж, Grafana шинэчлэгдлээ"
  fi

  echo "→ хяналтын байгууллага"
  # Аль байгууллага зохицуулагч болохыг slug-аар нь хэлнэ, ба тэдгээрийг энд
  # хатуу бичих аргагүй: анхны тохиргооны шидтэн байгууллагын slug-ийг улсын
  # бүртгэлийн дугаараар нь тавьдаг тул систем бүрд өөр. Тавиагүй бол алхам нь
  # алгасагдаж, юуг тавихыг хэлнэ — таамгаар нэг байгууллагад бүх компанийн
  # өгөгдөл рүү хандах эрх өгөхөөс хоосон орхих нь дээр.
  if [ -n "${PETRONET_OVERSIGHT_SLUGS:-}" ]; then
    # Жагсаалтыг psql-ийн хувьсагчаар дамжуулна, SQL мөрөнд наахгүй: `'`-тэй
    # утга шууд асуулга болж, «a, b» дахь зай чимээгүй таарахгүй болдог байв.
    local added
    added="$(psql_db -tA -v slugs="$PETRONET_OVERSIGHT_SLUGS" <<'SQL'
WITH ins AS (
  INSERT INTO petro_oversight_bodies (tenant_id, name, scope)
  SELECT id, name, 'national' FROM registry.tenants
   WHERE slug = ANY (SELECT btrim(s) FROM unnest(string_to_array(:'slugs', ',')) AS s)
  ON CONFLICT (tenant_id) DO UPDATE SET name = EXCLUDED.name, scope = EXCLUDED.scope
  RETURNING 1)
SELECT count(*) FROM ins;
SQL
)"
    echo "  хяналтын байгууллага: ${added} (заасан: ${PETRONET_OVERSIGHT_SLUGS})"
  else
    echo "  PETRONET_OVERSIGHT_SLUGS тавиагүй — алгаслаа"
    echo "  жишээ: PETRONET_OVERSIGHT_SLUGS=9129294,amgtg $0 finish"
  fi
  psql_db -tAc "SELECT '  ' || t.slug || ' — ' || o.scope
                  FROM petro_oversight_bodies o JOIN registry.tenants t ON t.id = o.tenant_id"

  echo "→ бүтээгдэхүүний толь ба бодлого"
  psql_db -tAc "SELECT '  бүтээгдэхүүн: ' || count(*) FROM petro_products"
  psql_db -tAc "SELECT '  бодлогын хувилбар: ' || COALESCE(max(version)::text, 'алга') FROM petro_policy"

  echo "→ тайлангийн үе"
  # Хуваарьт ажил үүнийг өөрөө үүсгэдэг; энд зөвхөн үүссэн эсэхийг хэлнэ.
  psql_db -tAc "SELECT '  нээлттэй үе: ' || count(*) FROM petro_report_periods WHERE status = 'open'"

  echo "→ шалгалт"
  local url
  for url in https://petronet.mn/ https://petronet.mn/api/v1/petro/public/daily \
             https://docs.petronet.mn/ https://monitor.petronet.mn/ \
             https://dwh.petronet.mn/ https://backups.petronet.mn/ https://admin.petronet.mn/; do
    printf '  %s  %s\n' "$(curl -fsS -o /dev/null -w '%{http_code}' -L "$url" || echo ERR)" "$url"
  done

  echo "→ нөөцлөлтийн хуваарь"
  if [ -f /etc/cron.d/petronet-backup ]; then
    echo "  /etc/cron.d/petronet-backup бий"
  else
    install -m 644 /dev/stdin /etc/cron.d/petronet-backup <<'CRON'
# PetroNet System — өдөр бүрийн нөөцлөлт.
15 3 * * * root /opt/petronet/src/deploy/scripts/backup.sh >> /var/log/petronet-backup.log 2>&1
CRON
    echo "  /etc/cron.d/petronet-backup нэмэгдлээ"
  fi

  cat <<'REMAINING'

Гараар шийдэх үлдсэн зүйлс (энэ скрипт таамаглахгүй):
  FUEL_DAILY_GRANT_MNT   иргэний өдрийн эрх — бодлогын тоо
  MAP_RASTER_UPSTREAM    газрын зургийн тайлын эх сурвалж
  BRAND_LOGO_URL         лого ба хоёр icon
  petro_policy           хүлцэл, давтамж — анхдагч нь 0.3/0.3/0.5, өдөр тутам
  nginx/admin.*.conf     консолын хаягийн хязгаарлалт
REMAINING
}

case "${1:-}" in
  prepare) prepare ;;
  finish)  finish ;;
  *) echo "usage: $0 prepare|finish   (цэвэрлэхэд: PETRONET_WIPE=yes-i-mean-it $0 prepare)" >&2; exit 2 ;;
esac
