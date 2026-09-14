# PetroNet's own image.
#
# Two binaries: this product's server — the platform plus whatever modules this
# repository carries — and the platform's migration runner, built from the exact
# core version go.mod pins. The platform's migrations and the catalogue come
# from that same version, so the schema an image applies and the apps it offers
# are the ones the code in it was written against. A module's own migrations do
# not: they travel inside the module, through nexus.Migrations.

FROM golang:1.26-alpine AS build

RUN apk add --no-cache git ca-certificates jq

WORKDIR /src

# Dependencies first, so a change to our own code does not re-download the core.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# What this build tells an app store it is.
#
# Derived from the core version go.mod pins rather than passed in, because it is
# not a choice: PlatformVersion is what every manifest's `"platform": ">=1.0.0"`
# is checked against, and the honest answer is the version of the platform
# actually compiled into this image. Left to a build argument it defaults to
# 0.0.0, and the instance then refuses every manifest it ships with — it cannot
# read its own catalogue, so it will not start.
#
# Which package holds it is asked rather than assumed. PlatformVersion moved
# from internal/platform to internal/kernel/config after the core's v1.13.0,
# and `-X` naming a symbol that does not exist is **silently ignored** by the
# linker: a stale path builds cleanly, produces an image that reports the
# default, and that image then refuses every manifest it ships with and never
# comes up. Nothing in the build log says so. So the two candidates are probed
# in order and a core with neither fails the build here, loudly.
#
# The `%%-*` strips a pseudo-version's suffix, and it is not cosmetic. go.mod
# pins the core at a commit rather than a tag while main waits for v2.0.0, so
# `go list` answers 1.15.1-0.20260829032520-77d79397382c — a semver PRERELEASE,
# and Masterminds/semver excludes prereleases from every range that has none.
# Reported as-is, this image fails `"platform": ">=1.0.0"` on every manifest it
# ships with, cannot read its own catalogue, and never comes up. Verified, not
# guessed: cs.Check(1.15.1-0.2026...) is false for both >=1.0.0 and >=1.10.0.
#
# ponytail: strips to the base version, which overstates by a prerelease.
# Harmless while every manifest asks for >=1.0.0; the line becomes a no-op the
# day go.mod goes back to a tag, which is the real fix.
RUN core=$(go list -m -f '{{.Version}}' github.com/gerege-systems/open-gerege-nexus/backend) \
    && version=${core#v} \
    && version=${version%%-*} \
    && base=github.com/gerege-systems/open-gerege-nexus/backend/internal \
    && for pkg in kernel/config platform; do \
         if go list "$base/$pkg" >/dev/null 2>&1; then sym="$base/$pkg.PlatformVersion"; break; fi; \
       done \
    && { [ -n "${sym:-}" ] || { echo "no PlatformVersion package in ${core}"; exit 1; }; } \
    && echo "building as platform ${version} (${sym##*/internal/})" \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-w -s -X ${sym}=${version}" \
    -o /bin/petronet .

# Бинарьт үнэхээр орсон цөмийн хувилбар — go.mod-оос биш, бинарийн buildinfo-оос.
# deploy.sh үүнийг go.mod-той тулгаж, pin-ээ солисон атлаа хуучин кэштэй образ
# ажиллаж байгааг deploy бүрд барина. Эцсийн образд Go байхгүй тул энд уншина.
RUN go version -m /bin/petronet \
    | awk '$1 == "dep" && $2 == "github.com/gerege-systems/open-gerege-nexus/backend" { print $3 }' \
    > /bin/core-version \
    && test -s /bin/core-version

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" \
    -o /bin/migrate github.com/gerege-systems/open-gerege-nexus/backend/cmd/migrate

# The console's first operator, for the deployments the wizard cannot reach.
#
# The wizard opens the console as its last step, but only while the deployment
# has no organisation: its token dies the moment one exists. Every console
# opened after that day — this deployment's own included, whose organisation was
# made before the wizard could offer the step — is opened here, from a shell,
# and an image without this binary has no way to open one at all.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" \
    -o /bin/operator-bootstrap github.com/gerege-systems/open-gerege-nexus/backend/cmd/operator-bootstrap

# Байгууллага үүсгэх.
#
# Шинэ ААН-ийг системд оруулах ажил нь консолын дэлгэцээс хийгддэг ч тэр нь
# операторын session шаарддаг. Эхний нэвтрүүлэлтийн үед — 200 гаруй ААН-ийг
# багцаар оруулах өдөр — shell-ээс хийх зам байх ёстой. Мөн шидтэн өнгөрсний
# дараа хоёр дахь байгууллага үүсгэх цорын ганц бус зам нь энэ.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" \
    -o /bin/tenant-bootstrap github.com/gerege-systems/open-gerege-nexus/backend/cmd/tenant-bootstrap

# Шатахууны операторын бүртгэлийг ачаалах тушаал. Зөвхөн гараар дуудагдана;
# зүгээр л дүрсэнд байснаараа ямар ч бичлэг үүсгэхгүй.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-w -s" \
    -o /bin/petro-import ./cmd/petro-import

# The platform's migration files, which are data in the module rather than code.
# The path carries the version, so this reads it back rather than guessing.
RUN core=$(go list -m -f '{{.Dir}}' github.com/gerege-systems/open-gerege-nexus/backend) \
    && mkdir -p /out/db && cp -r "$core/db/migrations" /out/db/migrations \
    && chmod -R u+w /out/db

# The catalogue: the core's, merged with this repository's own.
#
# The core's half cannot come out of the module cache like the migrations do:
# the Go module is rooted at the core's `backend/` and `catalog/` sits beside
# it, outside the module. So it comes from the tag `go list` just reported —
# the alternative is committing the platform's entries here, where they go
# stale the day the core is bumped.
#
# A pseudo-version has no tag to ask for, so the archive is fetched by the
# commit the pseudo-version ends in. Same file either way — GitHub serves
# `archive/<ref>.tar.gz` for a tag or a sha — and the branch disappears the day
# go.mod goes back to a tag.
#
# Ours first, so a duplicate id resolves to this repository's entry: jq's sort
# is stable and `unique_by` keeps the first of each group. This repository's
# half is empty today; the merge is here rather than later because an instance
# whose binary carries an app its catalogue does not name refuses to start, and
# that is a failure to discover on the day a module is added, not on the day it
# is deployed.
RUN core=$(go list -m -f '{{.Version}}' github.com/gerege-systems/open-gerege-nexus/backend) \
    && case "$core" in \
         *-*) ref="${core##*-}" ;; \
         *)   ref="refs/tags/backend/${core}" ;; \
       esac \
    && wget -qO- "https://github.com/gerege-systems/open-gerege-nexus/archive/${ref}.tar.gz" \
    | tar -xz -C /tmp \
    && cp -r /tmp/open-gerege-nexus-*/catalog /out/catalog \
    && if [ -d catalog/manifests ]; then mkdir -p /out/catalog/manifests \
       && cp -f catalog/manifests/*.json /out/catalog/manifests/; fi \
    && if [ -d catalog/chronicle ]; then mkdir -p /out/catalog/chronicle \
       && cp -f catalog/chronicle/*.json /out/catalog/chronicle/; fi \
    && jq -s 'add | unique_by(.id)' catalog/apps.json /tmp/open-gerege-nexus-*/catalog/apps.json \
       > /out/catalog/apps.json \
    && test -f /out/catalog/apps.json \
    && for id in $(jq -r '.[].id' catalog/apps.json); do \
         jq -e --arg id "$id" 'map(select(.id == $id)) | length == 1' /out/catalog/apps.json \
         > /dev/null || { echo "catalogue merge lost $id"; exit 1; }; \
       done

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -u 10001 petronet

WORKDIR /app

COPY --from=build /bin/petronet /app/petronet
COPY --from=build /bin/core-version /app/CORE_VERSION
COPY --from=build /bin/migrate  /app/migrate
COPY --from=build /bin/operator-bootstrap /app/operator-bootstrap
COPY --from=build /bin/tenant-bootstrap /app/tenant-bootstrap
COPY --from=build /bin/petro-import /app/petro-import
COPY --from=build /out/db       /app/db
COPY --from=build /out/catalog  /app/catalog

USER petronet
EXPOSE 8080

CMD ["/app/petronet"]
