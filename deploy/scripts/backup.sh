#!/usr/bin/env bash
#
# PetroNet System — өгөгдлийн сангийн нөөцлөлт.
#
# Цөмийн `deploy/scripts/backup.sh`-ийн хуулбар (open-gerege-nexus, c6cc566).
# Хуулсан шалтгаан нь: Go модуль ажиллах кодыг л зөөдөг, ажиллагааны скрипт
# репотойгоо үлддэг. Distribution бүр өөрийн хуулбартай байх ёстой бөгөөд
# ялгаа нь зөвхөн доорх анхдагч утгууд.
#
# ЭДГЭЭР АНХДАГЧ УТГА ЧУХАЛ. Энэ хост дээр урьд нь хөрш систем ажиллаж байсан
# бөгөөд түүний postgres контейнер яг цөмийн анхдагч нэртэй
# (`gerege_nexus_postgres`) байв. Анхдагчийг нь солихоо мартсанаас энэ скрипт
# хөршийн сангаас dump авч, хөршийн `platform_backups`-д «амжилттай» гэж
# бичсэн — нэг ч алдааны мөр гаргалгүйгээр. Нэг удаа яг тэр болсон.
#
# Хөрш 2026-09-03-нд алга болсон ч анхдагчийг нь буцаах шалтгаан алга: нэрээ
# өөрөө хэлдэг скрипт нь дараагийн хөршид ч аюулгүй.
#
# Энэ платформ дээр CP-4 хүртэл нөөцлөлт БАЙГААГҮЙ. Тиймээс энэ файл нь
# "консол дээр харуулах статус" гэхээсээ илүү, эхлээд нөөцлөлт өөрөө юм.
#
# Хийдэг зүйл нь гурав: pg_dump авах, хуучныг цэвэрлэх, үр дүнг өгөгдлийн санд
# бүртгэх. Гурав дахь нь консол уншдаг мөр (`platform_backups`) бөгөөд түүнгүй
# бол нөөцлөлт ажиллаж байгаа эсэхийг хэн ч мэдэхгүй — cron-ий чимээгүй
# бүтэлгүйтэл нь сэргээх өдрөө л илэрдэг.
#
# Cron дээр (жишээ нь өдөр бүр 03:15 цагт):
#
#   15 3 * * * /opt/petronet/src/deploy/scripts/backup.sh >> /var/log/petronet-backup.log 2>&1
#
# Тохируулга (env эсвэл дуудахын өмнө export):
#
#   BACKUP_DIR       — хаана хадгалах (анхдагч /var/backups/petronet)
#   BACKUP_KEEP_DAYS — хэдэн хоног хадгалах (анхдагч 14)
#   POSTGRES_CONTAINER — postgres контейнерийн нэр (анхдагч gerege_petronet_postgres)
#   POSTGRES_DB / POSTGRES_USER — анхдагч platform_db / postgres
#
# Толгойн эдгээр утга нь кодынхтой таарч байх ёстой. Өмнө нь цөмийн анхдагчийг
# (gerege_nexus_postgres) баримтжуулсан атлаа код нь энэ системийнхийг хэрэглэдэг
# байсан — тэр файлын өөрийнх нь анхааруулга яг хөршийн сангаас dump авсан
# тухай юм (аудитын №46).
#   TEXTFILE_DIR     — node_exporter-ийн textfile хавтас
#                      (анхдагч /var/lib/node_exporter, хоосон бол бичихгүй)
#
# Өөр байршил руу илгээх (бүгд хоосон бол алхам нь бүхэлдээ алгасагдана):
#
#   BACKUP_AGE_RECIPIENT — age-ийн НИЙТИЙН түлхүүр. Хостод зөвхөн энэ байна:
#                          эвдэрсэн платформ өөрийн илгээсэн зүйлээ уншиж
#                          чадахгүй. Хувийн түлхүүр нь операторт байна.
#   BACKUP_S3_ENDPOINT / BACKUP_S3_BUCKET / BACKUP_S3_KEY / BACKUP_S3_SECRET
#
#   restic (rest-server, жишээ нь backups.gecore.mn) — S3-аас хамааралгүй,
#   хоёулаа тохируулагдвал хоёуланд нь илгээнэ. Нууцыг .env-д биш, root-ийн
#   файлд (анхдагч /etc/petronet/backup-restic.env, 600) хадгална:
#     RESTIC_REPOSITORY=rest:https://<user>:<pass>@<host>/restic/<user>/
#     RESTIC_PASSWORD=<repo-гийн шифрлэлтийн нууц үг>
#   restic нь repo-гоо өөрөө шифрлэдэг тул age хэрэггүй. Хостод хоёртын файл
#   суулгахгүй, pin хийсэн контейнерээс ажиллана (BACKUP_RESTIC_IMAGE).
#
#   Cron ямар ч env дамжуулдаггүй тул BACKUP_ENV_FILE (анхдагч
#   /etc/petronet/backup.env) байвал эхэнд уншигдана — дээрх BACKUP_* утгуудыг
#   тэнд бичнэ.
#
# ЭНЭ СКРИПТ НЬ ХАНГАЛТТАЙ ГЭДЭГ АМЛАЛТ БИШ. Нэг хостын дискэн дээрх нөөцлөлт
# нь тэр хостыг алдвал хамт алга болно: docs/OPERATIONS.md бичсэнээр
# өөр байршил руу хуулах (rclone, rsync, S3) нь дараагийн алхам. Гэхдээ
# байхгүйгээс энэ дээр нь бүтээх боломжтой зүйл байсан нь дээр.

set -euo pipefail
# Dump нь бүх сан: иргэдийн мэдээлэл, нууц үгийн hash. cron-ийн анхдагч umask
# 022 нь түүнийг хостын дурын хэрэглэгчид уншигдахаар үлдээдэг байв.
umask 077

BACKUP_ENV_FILE="${BACKUP_ENV_FILE:-/etc/petronet/backup.env}"
if [ -f "${BACKUP_ENV_FILE}" ]; then
    set -a
    # shellcheck disable=SC1090
    . "${BACKUP_ENV_FILE}"
    set +a
fi
BACKUP_RESTIC_ENV_FILE="${BACKUP_RESTIC_ENV_FILE:-/etc/petronet/backup-restic.env}"
BACKUP_RESTIC_IMAGE="${BACKUP_RESTIC_IMAGE:-restic/restic:0.18.1}"

BACKUP_DIR="${BACKUP_DIR:-/var/backups/petronet}"
BACKUP_KEEP_DAYS="${BACKUP_KEEP_DAYS:-14}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-gerege_petronet_postgres}"
POSTGRES_DB="${POSTGRES_DB:-platform_db}"
POSTGRES_USER="${POSTGRES_USER:-postgres}"
TEXTFILE_DIR="${TEXTFILE_DIR:-/var/lib/node_exporter}"
BACKUP_AGE_RECIPIENT="${BACKUP_AGE_RECIPIENT:-}"
BACKUP_S3_ENDPOINT="${BACKUP_S3_ENDPOINT:-}"
BACKUP_S3_BUCKET="${BACKUP_S3_BUCKET:-}"
BACKUP_S3_KEY="${BACKUP_S3_KEY:-}"
BACKUP_S3_SECRET="${BACKUP_S3_SECRET:-}"
offsite_ok=0

# Хэмжигдэхгүй нөөцлөлт нь нөөцлөлт байхгүйтэй бараг адил: cron-ий чимээгүй
# бүтэлгүйтэл нь сэргээх өдрөө л илэрдэг. Өгөгдлийн сан дахь мөр нь консол
# уншдаг, харин энэ нь Prometheus уншдаг — өөрөөр хэлбэл шөнө дунд хэн нэгэнд
# сэрэмжлүүлэг илгээж чадах цорын ганц хувилбар. Бичих арга нь TLS-ийн
# хугацааны ажилтай яг ижил (docs/OPERATIONS.md): атомик бичилт, учир нь
# node_exporter хагас бичигдсэн файлыг уншиж болохгүй.
write_metrics() {
    local ok="$1" size="$2"
    [ -n "${TEXTFILE_DIR}" ] && [ -d "${TEXTFILE_DIR}" ] || return 0
    local out="${TEXTFILE_DIR}/petronet_backup.prom" tmp
    tmp="$(mktemp "${out}.XXXXXX")" || return 0
    {
        echo "# HELP nexus_backup_last_run_timestamp_seconds When the backup job last ran, successful or not"
        echo "# TYPE nexus_backup_last_run_timestamp_seconds gauge"
        echo "nexus_backup_last_run_timestamp_seconds $(date +%s)"
        echo "# HELP nexus_backup_last_success_timestamp_seconds When a backup last succeeded"
        echo "# TYPE nexus_backup_last_success_timestamp_seconds gauge"
        if [ "${ok}" = "true" ]; then
            echo "nexus_backup_last_success_timestamp_seconds $(date +%s)"
            echo "# HELP nexus_backup_last_size_bytes Size of the last successful dump"
            echo "# TYPE nexus_backup_last_size_bytes gauge"
            echo "nexus_backup_last_size_bytes ${size}"
        else
            # Өмнөх амжилтын мөчийг хадгална — эс бөгөөс нэг бүтэлгүйтэл нь
            # "хэзээ ч амжилттай болоогүй"-тэй ялгагдахаа болино.
            local previous
            previous="$(awk '/^nexus_backup_last_success_timestamp_seconds /{print $2}' "${out}" 2>/dev/null)"
            [ -n "${previous}" ] && echo "nexus_backup_last_success_timestamp_seconds ${previous}"
        fi
        echo "# HELP nexus_backup_last_ok Whether the last run succeeded"
        echo "# TYPE nexus_backup_last_ok gauge"
        echo "nexus_backup_last_ok $([ "${ok}" = "true" ] && echo 1 || echo 0)"
        # Тохируулаагүй систем дээр 0 хэвээр байна, тэр нь зөв: хуулбар өөр
        # газар байхгүй гэдэг нь хэмжигдэх ёстой баримт.
        echo "# HELP nexus_backup_offsite_ok Whether the encrypted copy reached the off-site store"
        echo "# TYPE nexus_backup_offsite_ok gauge"
        echo "nexus_backup_offsite_ok ${offsite_ok}"
    } > "${tmp}"
    chmod 0644 "${tmp}"
    mv -f "${tmp}" "${out}"
}

stamp="$(date +%Y%m%d-%H%M%S)"
target="${BACKUP_DIR}/petronet-${stamp}.sql.gz"
started="$(date --iso-8601=seconds 2>/dev/null || date +%Y-%m-%dT%H:%M:%S%z)"

mkdir -p "${BACKUP_DIR}"

# Бүртгэлийг үргэлж бичнэ — амжилттай ч, амжилтгүй ч. Амжилтгүй нөөцлөлтийн
# тухай чимээгүй байх нь нөөцлөлт огт хийхгүй байхтай ижил хор уршигтай.
record() {
    local ok="$1" size="$2" detail="$3"
    docker exec -i "${POSTGRES_CONTAINER}" \
        psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" \
        -c "INSERT INTO platform_backups (kind, started_at, finished_at, size_bytes, ok, detail)
            VALUES ('backup', '${started}', NOW(), ${size}, ${ok}, \$detail\$${detail}\$detail\$)" \
        >/dev/null 2>&1 || echo "backup: өгөгдлийн санд бүртгэж чадсангүй" >&2
}

# Тогтмол /tmp зам биш: root-оор ажиллах скрипт урьдчилан тавьсан symlink-ээр
# дурын файлыг дарж бичиж болно.
errfile="$(mktemp)"
trap 'rm -f "${errfile}"' EXIT

if ! docker exec -i "${POSTGRES_CONTAINER}" \
        pg_dump -U "${POSTGRES_USER}" -d "${POSTGRES_DB}" --no-owner --clean --if-exists \
        2>"${errfile}" | gzip -9 > "${target}"; then
    detail="$(tail -c 500 "${errfile}" || true)"
    rm -f "${target}"
    record false NULL "pg_dump failed: ${detail}"
    write_metrics false 0
    echo "backup: pg_dump амжилтгүй" >&2
    exit 1
fi

size="$(wc -c < "${target}" | tr -d ' ')"

# Хоосон гаралт нь амжилттай харагдах бүтэлгүйтлийн сонгодог хэлбэр: pg_dump
# алдаагүй дуусаад юу ч бичээгүй байх. Хэдэн килобайтаас бага бол сэжигтэй.
if [ "${size}" -lt 10240 ]; then
    record false "${size}" "the dump is only ${size} bytes"
    write_metrics false "${size}"
    echo "backup: гаралт хэтэрхий жижиг (${size} байт)" >&2
    exit 1
fi

find "${BACKUP_DIR}" -name 'petronet-*.sql.gz' -mtime "+${BACKUP_KEEP_DAYS}" -delete || true

# Өөр байршил руу.
#
# Дискэн дээрх нөөцлөлт нь тэр дискийг алдвал хамт алга болно. Энэ алхам нь
# хуулбарыг өөр газар үлдээнэ — гэхдээ эхлээд ШИФРЛЭНЭ. Хостод зөвхөн нийтийн
# түлхүүр байгаа тул эвдэрсэн платформ ч, нөөцийн санг барьсан хэн ч уншиж
# чадахгүй.
#
# Аль нэг тохиргоо дутуу бол алхам бүхэлдээ алгасагдана: тохируулаагүй систем
# энэ скриптийг ажиллуулж чадах ёстой.
offsite() {
    [ -n "${BACKUP_AGE_RECIPIENT}" ] || return 0
    [ -n "${BACKUP_S3_ENDPOINT}" ] && [ -n "${BACKUP_S3_BUCKET}" ] || return 0
    [ -n "${BACKUP_S3_KEY}" ] && [ -n "${BACKUP_S3_SECRET}" ] || return 0
    command -v age >/dev/null 2>&1 || { echo "backup: age суугаагүй" >&2; return 1; }

    local enc="${target}.age" key host date_hdr sig
    if ! age -r "${BACKUP_AGE_RECIPIENT}" -o "${enc}" "${target}"; then
        rm -f "${enc}"
        echo "backup: шифрлэж чадсангүй" >&2
        return 1
    fi

    key="$(basename "${enc}")"
    # AWS SigV4-ийн оронд S3-ийн хуучин, гэхдээ MinIO дэмждэг presigned биш
    # энгийн гарын үсэг ашиглахгүй: mc-г контейнерээс дуудна. Хостод шинэ
    # хоёртын файл суулгахгүй, MinIO-гийн өөрийнх нь клиент аль хэдийн бий.
    if ! docker run --rm --network host \
            -e MC_HOST_store="https://${BACKUP_S3_KEY}:${BACKUP_S3_SECRET}@${BACKUP_S3_ENDPOINT#https://}" \
            -v "${enc}:/upload/${key}:ro" \
            minio/mc:latest cp --quiet "/upload/${key}" "store/${BACKUP_S3_BUCKET}/${key}" >/dev/null 2>&1; then
        rm -f "${enc}"
        echo "backup: өөр байршил руу илгээж чадсангүй" >&2
        return 1
    fi
    rm -f "${enc}"
    offsite_ok=1
    echo "backup: өөр байршилд ${BACKUP_S3_BUCKET}/${key}"
    return 0
}

offsite || true

# restic rest-server руу — дээрхээс тусдаа хоёр дахь байршил.
#
# Repo нь append-only (сервер тал): эвдэрсэн энэ хост хуучин snapshot-ыг
# устгаж чадахгүй, retention-ийг repo-гийн эзэн хийнэ. Илгээснийг итгэхгүй,
# сүүлийн snapshot тэр файлыг агуулж буйг шалгана.
offsite_restic() {
    [ -f "${BACKUP_RESTIC_ENV_FILE}" ] || return 0
    local name
    name="$(basename "${target}")"
    if ! docker run --rm --network host --env-file "${BACKUP_RESTIC_ENV_FILE}" \
            -v "${target}:/backup/${name}:ro" \
            "${BACKUP_RESTIC_IMAGE}" backup --quiet --host petronet --tag daily "/backup/${name}" >/dev/null; then
        echo "backup: restic руу илгээж чадсангүй" >&2
        return 1
    fi
    if ! docker run --rm --network host --env-file "${BACKUP_RESTIC_ENV_FILE}" \
            "${BACKUP_RESTIC_IMAGE}" ls latest --host petronet 2>/dev/null | grep -qF "/backup/${name}"; then
        echo "backup: restic-ийн сүүлийн snapshot-д ${name} алга" >&2
        return 1
    fi
    offsite_ok=1
    echo "backup: restic snapshot ${name}"
    return 0
}
offsite_restic || true

record true "${size}" "${target}"
write_metrics true "${size}"
echo "backup: ${target} (${size} байт)"
