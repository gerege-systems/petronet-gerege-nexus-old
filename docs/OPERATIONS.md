# Ажиллагаа

Байршуулалт, ажиглалт, нөөцлөлт. `deploy/`, `.github/workflows/deploy.yml` ба
ажиллаж байгаа системээс уншиж бичигдсэн.

[Баримтын төв](README.md) · [Архитектур](ARCHITECTURE.md) ·
[Гарын авлага](RUNBOOKS.md)

---

## Зургаан нэр, нэг хост

| Нэр | Юу | Ард нь |
| --- | --- | --- |
| `petronet.mn` | Платформ өөрөө — frontend ба `/api/v1/*` | Next.js + Go |
| `admin.petronet.mn` | Операторын консол — `/api/platform/v1/*` | Мөн тэр хоёр |
| `dwh.petronet.mn` | Дата агуулахын зураглал | Статик, өөрийн контейнер |
| `backups.petronet.mn` | Шифрлэгдсэн нөөцийн сан (MinIO) + тайлбар хуудас | MinIO + nginx |
| `monitor.petronet.mn` | Grafana | Ажиглалтын стек |
| `docs.petronet.mn` | Энэ баримт | Статик, MkDocs |
| `plan.petronet.mn` | Төслийн баримт — шаардлага, төлөвлөгөө, жишиг | Статик, MkDocs |

Хоёр урсгал өөр origin дээр байгаа нь гоо зүйн биш: cookie нь hostname-аар
хязгаарлагддаг тул тусдаа нэр нь тусдаа ambient authority гэсэн үг.

Эдгээрээс гадна native клиентүүдийн дөрвөн **төхөөрөмжийн шугам**
(`desktop.` / `mobile.` / `kiosk.` / `pos.petronet.mn`) байна — тэдгээр нь өөр
үйлчилгээ БИШ, ижил frontend ба API зөвхөн өөр origin дээр. Бүртгэл ба асаах
дараалал: [Байрлуулалт § Төхөөрөмжийн шугамууд](DEPLOYMENT.md#төхөөрөмжийн-шугамууд).

Платформын нүүр хуудас эдгээрийг карт болгож харуулна. Хаягууд нь кодод биш,
системийн орчинд (`SERVICE_URL_ADMIN`, `_DWH`, `_BACKUPS`, `_MONITOR`,
`_DOCS`, мөн үндэсний `_EID`): `admin.petronet.mn` бол PetroNet System-ийнх,
өөр хэн ч биш. Тохируулаагүй үйлчилгээ зурагдахгүй, нэг ч тохируулаагүй бол
хэсэг бүхэлдээ зурагдахгүй.

Консолын хаалга нь мөн платформын landing дизайнтай: нэвтрэх маягт нь hero
дотор, доор нь эрхийн загвар ба impersonation-ий нөхцөлүүд. Session-гүй ирсэн
хүн ард нь юу байгааг мэдэлгүй үлдэх ёсгүй — тэр хаяг юу болохыг хэлэх нь
нууц задруулах биш, нэвтрэх шалгалт нь ард нь хэвээр байна.

### Статик хуудсууд контейнерт

`dwh.*` нь `deploy/docker-compose.dwh.yml`-ийн `nginx:alpine` контейнерээс
үйлчилнэ — read-only файлын систем, `no-new-privileges`, зөвхөн loopback.
Хостын nginx-ээс шууд файл өгч ч болох байсан (`docs.*` тэгдэг) ч тэр төсөл
дараа нь Metabase, Cube, Postgres нэмнэ: тэдгээр нь бүгд контейнер болох тул
нэгдүгээрх нь ч контейнер байсан нь хожим stack-ийн хажууд зөөхийг compose
файлд мөр нэмэх ажил болгоно.

## Байршуулалт

CI ногоон болсны дараа `deploy.yml`:

```
ghcr.io-д backend ба frontend image бүтээж түлхэх
   ↓  (tag = commit sha, мөн latest)
хост руу ssh: compose файл хуулах → pull → migrate → restart
```

Хоёр зүйл зориуд:

- **Хувилбар нь image дотор ldflags-аар шатаагдана.** Тэгэхгүй бол систем
  бүр өөрийн хувилбарыг мэдэхгүй болно — каталог `"platform": ">=1.1.0"`
  шаардахад хариулах зүйлгүй.
- **Хуучныг шинэ нь бэлэн болтол буулгахгүй.** GHCR дөнгөж түлхсэн хувийн
  image-д `denied` гэж хариулах нь ховор биш бөгөөд нэг удаа тэр нь тухайн
  ажиллагааны өөрийнх нь түлхсэн image-ийн rollout-ыг унагаасан.

### Тохиргоо CI-гаас ирнэ, хостоос биш

`deploy.yml` нь серверийн `.env`-ийг **ажиллагаа бүрд шинээр бичдэг** —
«written fresh every deploy so the server never drifts from CI». Энэ нь зөв
зан төлөв: хост дээр гараар хийсэн засвар нь хаана ч бүртгэгдээгүй, хэн ч
давтаж чадахгүй тохиргооны эх сурвалж болно.

Үр дагавар нь мартагдамхай: **хостын `.env` рүү гараар нэмсэн мөр дараагийн
rollout дээр арчигдана.** Шинэ тохиргоо нэмэх зам нь гурван газарт бичих:

```
deploy.yml  env блок          ${{ vars.X }} эсвэл ${{ secrets.X }}
deploy.yml  envs: жагсаалт    ssh-action тэр хувьсагчийг дамжуулна
deploy.yml  .env heredoc      X=${X}
```

Нууц биш утга — нийтийн hostname, URL — нь `vars`, нууц нь `secrets`.
Тавиагүй `vars` нь хоосон мөр болж, хэрэглэгч тал нь «тохируулаагүй» гэж
уншина.

Энэ баримтыг бичих үед үйлчилгээний хаягуудыг хостын `.env`-д гараар нэмээд
дараагийн deploy дээр алга болохыг харсны дараа л зөв газарт нь тавьсан.

**Хоосон нууц чимээгүй өнгөрдөггүй.** Тохируулаагүй нууц нь `.env`-д хоосон
мөр болж бичигдэж, платформ түүгээрээ асдаг: rollout юу ч анзаарахгүй,
алдаа нь тэр утгыг хамгийн түрүүнд асуусан зүйл дээр гарна. Тиймээс хоосон
байж болохгүй утгуудыг эхний байт серверт хүрэхээс өмнө шалгана. Зөвхөн
хоосон эсэхийг шалгаж, зөвхөн нэрийг хэвлэнэ — тавигдсаныг батлахын тулд
утгыг нь echo хийдэг шалгалт бол түүнийг лог уншдаг бүх хүнд нийтэлсэн хэрэг.

### Нууцын сан — Infisical

Нууцууд GitHub-ийн secret-ээс эсвэл **Infisical**-аас ирж болно. `deploy.yml`
дахь утга бүр `${{ secrets.X || env.X }}` хэлбэртэй: GitHub-д байвал түүнийг,
байхгүй бол сангаас ирснийг авна.

Тэгсний ач холбогдол нь **нэг нэгээр нь шилжүүлж болох** явдал. Үйлдвэрлэлийн
нууцыг нэг шөнөд бүхэлд нь зөөх шаардлагагүй: санд нэгийг тавь, GitHub-аас
тэрийг устга, нэг rollout ажиглаж үз, дараагийнх руу шилж. Аль ч мөчид
буцах зам нь secret-ээ буцааж тавих.

Нэвтрэлт нь **OIDC** — client secret байхгүй. GitHub ажиллагаа бүрд богино
настай токен гаргаж, Infisical түүнийг шалгана. Тэгснээр «нууц хадгалах газар
руу орох нууц» гэдэг асуудал огт үүсэхгүй.

Асаах: Infisical дээр machine identity үүсгээд, репод дараах **variable**-ууд
тавина (нууц биш — эдгээр нь зөвхөн хаяг ба танигч):

```
INFISICAL_IDENTITY_ID      machine identity-ийн ID
INFISICAL_PROJECT_SLUG     төслийн slug
INFISICAL_DOMAIN           сангийн хаяг
INFISICAL_ENV_SLUG         анхдагч prod
INFISICAL_SECRET_PATH      анхдагч /
```

`INFISICAL_IDENTITY_ID` тавиагүй бол алхам ажиллахгүй бөгөөд бүх зүйл
өмнөх шигээ GitHub-ийн secret-ээс ирнэ.

**Санд ордоггүй зүйлс.** `DEPLOY_SSH_KEY`, `DEPLOY_HOST`, `DEPLOY_USER` нь
GitHub-д үлдэнэ: тэдгээр нь хост руу хүрэх зам бөгөөд санд хүрэхээс өмнө
хэрэгтэй болдог. `GITHUB_TOKEN` нь GitHub-ийн өөрийнх.

**Санд байх ёстой ч CI-д хэзээ ч ордоггүй зүйлс.** Хостын дээрх нөөцлөлт,
ажиглалтын түлхүүрүүд — ялангуяа age-ийн хувийн түлхүүр — нь deploy-гоор
дамждаггүй. Тэдгээрийн хувьд сан нь цорын ганц хуулбар: хост алдвал
сэргээх эх сурвалж өөр байхгүй.

## CI-ийн Windows worker

macOS, iOS, Android гурвыг GitHub-ийн үүлэн runner барина. **Windows нь өөр**:
WinUI-ийн XamlCompiler ажиллуулахад Windows SDK ба Visual Studio Build Tools
хэрэгтэй бөгөөд тэдгээрийн хувилбарыг өөрсдөө барихын тулд өөрийн төмөр дээр
барина.

| | |
| --- | --- |
| Хост | Windows 11 Pro, 4 цөм, 16 ГБ |
| Хандалт | SSH (OpenSSH) |
| Runner | `petronet-win11`, шошго `petronet-win` |
| Байрлал | `C:\actions-runner-petronet`, сервис нь `actions.runner.gerege-systems-petronet-gerege-nexus.petronet-win11` |
| Сервисийн данс | `NT AUTHORITY\NETWORK SERVICE` — build хийхэд хангалттай, админ эрх хэрэггүй |
| Toolchain | .NET SDK 8.0.422 (`C:\dotnet`), git, VS Build Tools 2022: MSBuildTools + ManagedDesktopBuildTools + **VCTools** + Windows 11 SDK 26100 |

**VCTools (MSVC) нь заавал.** WinUI-ийн `Microsoft.UI.Xaml.Markup.Compiler.interop.targets`
нь `VC\Tools\MSVC`-г уншиж хамгийн сүүлийн хувилбарыг олдог — байхгүй бол
`DirectoryNotFoundException` шидэж build унана. `dotnet` SDK ганцаараа
хангалтгүй.

Тэр машин дээр **хоёр дахь runner** — `eid-platform-mn`-ийнх — зэрэгцэн
ажиллаж байгаа. Тиймээс job нь `runs-on: [self-hosted, windows, petronet-win]`
гэж ШОШГООР сонгоно: `self-hosted` дангаараа хоёр репогийн ажлыг хольж
явуулна.

**Энэ репо нийтийн, runner нь хуваалцсан төмөр.** Fork-оос ирсэн PR workflow-г
солиод тэр машин дээр дурын тушаал ажиллуулж чадах тул хамгаалалт хоёр
давхар:

1. `native-clients.yml`-ийн windows job нь `if:`-ээр fork-ийн PR дээр огт
   ажиллахгүй (зөвхөн энэ репогийн салбарууд).
2. Репогийн Actions тохиргоо `all_external_contributors` — гаднын хүний PR
   бүр workflow ажиллахын өмнө зөвшөөрөл хүснэ.

Хоёуланг нь сулруулах нь тэр машиныг гаднын хүнд нээж өгнө. Хэрэв Windows-ийн
шалгалт fork-ийн PR дээр ч хэрэгтэй болвол зөв зам нь `windows-latest` руу
буцаах, runner-ийг нээх БИШ.

### Runner дахин бүртгэх

Token хугацаа дуусах, эсвэл машин солигдох үед:

```bash
gh api -X POST repos/gerege-systems/petronet-gerege-nexus/actions/runners/registration-token --jq .token
# дараа нь тэр машин дээр:
#   C:\actions-runner-petronet\config.cmd --unattended --replace \
#     --url https://github.com/gerege-systems/petronet-gerege-nexus \
#     --token <TOKEN> --name petronet-win11 --labels petronet-win --runasservice
```

Ажлын хавтас (`_work`) машин дээр үлддэг тул job бүрийн төгсгөлд
`git clean -xdf` хийнэ — үгүй бол гурван form factor-ийн build гаралт дискийг
хэдхэн долоо хоногт дүүргэнэ.

### XAML-ийн алдааг хэрхэн УНШИХ вэ

`dotnet build` дээр WinUI-ийн XamlCompiler нь алдаатай XAML тулгарвал
**чимээгүй 1 буцаана**: stdout хоосон, `output.json` үүсэхгүй, MSBuild зөвхөн
`MSB3073 ... exited with code 1` гэж хэлнэ. Жинхэнэ мессежийг харах цорын ганц
зам нь компайлерыг MSBuild дотор ажиллуулах:

```powershell
& "C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools\MSBuild\Current\Bin\amd64\MSBuild.exe" `
    PetroNetDesktop.sln -p:Configuration=Release -p:FormFactor=Desktop -p:RestoreLockedMode=false -v:m
```

**amd64** хувилбарыг заавал: 32 битийн MSBuild нь RID-ыг `win-x86` гэж
таамаглаад `PlatformTarget=x64`-тэй зөрж NETSDK1032 өгнө. Мөн `-p:Platform`
БҮҮ дамжуул — solution нь зөвхөн `Any CPU` тохиргоотой.

Энэ замаар олдсон нэг жишээ: XML коммент дотор давхар зураас (`--`) байж
болохгүй, тиймээс CSS-ийн `--pn-blue` мэтийн нэрийг `.xaml` комментод шууд
бичих нь бүх Windows build-ийг унагаана (`WMC9999`).

## Ажиглалт

`deploy/docker-compose.monitoring.yml` — тусдаа стек, тусдаа сүлжээ,
платформын сүлжээнд зөвхөн scrape хийхээр холбогдоно.

| | | Хадгалах хугацаа |
| --- | --- | --- |
| Prometheus 3.1 | Хэмжүүр | 60 хоног / 20 GB |
| Loki 3.4 | Лог | 31 хоног |
| Tempo 2.7 | Trace | 72 цаг |
| Alloy 1.7 | Цуглуулагч | — |
| Grafana 11.5 | Дэлгэц | — |
| Alertmanager 0.28 | Сэрэмжлүүлэг | — |

Экспортерууд: node_exporter, cAdvisor, postgres_exporter, redis_exporter.

Trace 72 цаг байгаа шалтгаан: trace нь хүсэлт болсноос хойш минут, эсвэл цагийн
дотор уншигддаг. Долоо хоногийн настай trace-ыг хэн ч нээдэггүй.

### Хэмжүүр

Хэмжүүр бүр OpenTelemetry-ийн SDK-аар дамжиж, Prometheus exporter-ээр
`/metrics` дээр гарна. HTTP-ийнх нь semantic convention-ыг дагана:

```
http_server_request_duration_seconds{http_request_method,http_route,http_response_status_code}
```

Хүсэлтийн тоо нь тусдаа counter биш — тэр гистограммын `_count` цуврал.

`http.request.method` нь **хаалттай олонлог**. Танихгүй үйл үг
`_OTHER` болж нугалагдана: `PROPFIND` гэж дуудсан сканнер time series
үүсгэх ёсгүй.

Гадаад систем бүр нэг histogram-аар хэмжигдэнэ —
`external_request_duration_seconds{system,operation,status}` — системийн нэр
нь мөн адил хаалттай олонлог, танихгүй нь `other`.

### Exemplar

Trace асаалттай үед гистограммын сэмпл бүр `trace_id` авч явна. Латенси
графикийн удаан цэг дээр дарахад Grafana тэр яг тэр хүсэлтийн trace-ыг
нээнэ — «аль хүсэлт удаан байсан бэ» гэдгийг таамаглах хэрэггүй.

`/metrics` нь OpenMetrics хэлбэрээр үйлчилдэг байх ёстой, эс бөгөөс exemplar
дамжихгүй.

### Лог

Alloy Docker-ийн лог цуглуулж Loki руу өгнө. Backend нь `slog`-оор JSON
бичдэг тул `| json` ажиллана.

Хоёр урхи, хоёулаа PetroNet System дээр бодитоор гарсан:

- **LogQL-ийн шүүлт том жижиг үсэг ялгана.** `|= "audit"` нь юу ч олохгүй —
  бичигдсэн зүйл нь `"msg":"AUDIT_EVENT"`.
- **`$__rate_interval` нь зөвхөн Prometheus-ийнх.** Loki дээр `$__auto`.

Мөн: Grafana нь панелийг **панелийн** датасурс дээр ажиллуулдаг, target-ийнх
дээр биш. Хоёр нь зөрвөл LogQL нь Prometheus руу очиж parse алдаа өгнө.

### Дэлгэцүүд

`deploy/monitoring/grafana/dashboards/` — provisioning-оор ирдэг, гараар
үүсгэдэггүй:

`api-overview` · `infrastructure` · `logs` · `security` · `resilience` ·
`external-systems` · `monitoring-self`

Сүүлийнх нь хамгийн чухал: **ажиглалт өөрөө ажиллаж байгаа эсэх**. Унтарсан
Prometheus нь бүх зүйл хэвийн байгаа мэт харагддаг.

### Сэрэмжлүүлэг

31 дүрэм, зургаан файлд. Хамрах хүрээ:

- API — унтарсан, латенси, алдааны төсвийн шаталт (хурдан / удаан)
- Дэд бүтэц — диск, санах ой, Postgres, Redis, контейнерын дахин эхлэл, TLS
- Консол — нэвтрэлтийн бүтэлгүйтэл, түгжээ, break-glass ашиглалт,
  бүртгэгдээгүй бичилт
- Гадаад систем — удаан, доройтсон, бүтэлгүй
- Нөөцлөлт — бүтэлгүй, хуучирсан, хэзээ ч харагдаагүй, хостоос гараагүй
- Ажиглалт өөрөө — Alertmanager мэдэгдэхгүй байна, дүрэм тооцоолж чадахгүй
  байна, Loki лог татгалзаж байна, target унтарсан

`NexusTLSExpiryUnknown` нь тусгай: тэр нь «гэрчилгээ дуусах гэж байна» гэсэн
үг биш, **«хэн ч хэмжихгүй байна»** гэсэн үг. Хугацааг бичдэг cron ажил нь
гараар суулгагддаг бөгөөд нэг хост дээр огт суулгагдаагүй байсан.

### Брэндлэлт ба Монгол хэл

Ажиглалтын домэйн нь системийн нэг хэсэг мэт харагдах ёстой: Grafana дээр
PetroNet-ийн лого, нэр, tab icon, цагаан нэвтрэх карт, монгол хэл. Grafana
OSS-д брэндлэх цэг **байхгүй** (white labeling нь Enterprise) тул бүгд nginx
дээр хийгдэнэ:

```
deploy/scripts/setup_monitor_branding.sh
```

Скрипт нь ажиллаж буй Grafana-аас хоёр webpack chunk-ийн нэрийг уншиж (нэр нь
агуулгын hash агуулдаг), монгол орчуулгын chunk-ийг
`deploy/monitoring/grafana/branding/i18n/mn.txt`-ээс барьж, хэлний жагсаалт
дахь `Svenska`-г `Монгол` болгож, `/var/www/monitor` руу хэв маяг, скрипт,
логог тавиад nginx-ийн snippet-ийг үүсгэнэ. Төгсгөлд нь зургаан шалгалтыг
**домэйнээр дамжуулан** хийнэ — файл байрандаа байгаа эсэх биш, браузарт юу
ирж байгааг.

Гурван зүйлийг санах:

- **Grafana шинэчлэх бүрд дахин ажиллуулна.** chunk-ийн hash өөрчлөгдөхөд
  орлуулалт таарахаа болино; тэр үед швед хэл эргэж ирнэ, өөр юу ч эвдрэхгүй.
- Монгол хэл нь `sv-SE`-ийн үүрэнд сууна (Grafana-д `mn` locale байхгүй).
  Тиймээс `GRAFANA_DEFAULT_LANGUAGE=sv-SE` нь **зөвхөн** скрипт ажилласан
  хостод зөв.
- Орчуулга хэсэгчилсэн (255 түлхүүр). Дутуу түлхүүр англи руугаа буцдаг тул
  `mn.txt`-д мөр нэмээд скриптийг дахин ажиллуулахад л хангалттай.

Цөмийн хувилбараас нэг ялгаа: тэнд Grafana нь `/grafana/` дэд зам дээр сууж,
домэйны үндэс дээр landing хуудас үйлчилдэг. Энд Grafana нь үндэс дээрээ
сууна — энэ домэйны ард түүнээс өөр юу ч байхгүй — тул landing хуудас
хуулагдаагүй: үйлчилгээний картууд petronet.mn-ийн нүүр хуудсан дээр байна.

### Платформын бүртгэлээр Grafana руу нэвтрэх

Grafana нь PetroNet System-ийн өөрийн OIDC provider-оос хэн болохыг асууна:
операторууд аль хэдийн байгаа бүртгэлээрээ ордог, платформын админ нь
Grafana-ийн сервер админ болно (`platform_admin && 'GrafanaAdmin' || 'Viewer'`).
Бусад нь уншиж чадах ч юуг ч өөрчилж чадахгүй.

Дотоод админы нэвтрэлт (`GRAFANA_ADMIN_PASSWORD`) хэвээр байна. Унасан танигч
нь нээгдэхгүй ажиглалтын стек болох ёсгүй — тэр нь яг түүнийг хэрэгтэй
болгодог цаг.

Клиентийг платформ дээр бүртгэнэ (Хөгжүүлэгч → Аппликейшн, эсвэл SQL-ээр):

| Талбар | Утга |
| --- | --- |
| `redirect_uris` | `https://monitor.petronet.mn/grafana/login/generic_oauth` |
| `post_logout_redirect_uris` | `https://monitor.petronet.mn/grafana/login` |
| `grant_types` | `authorization_code`, `refresh_token` |
| `scopes` | `openid`, `profile`, `email`, `roles` |
| `client_type` | `confidential` |

`client_secret_hash` нь нууц үгийн SHA-256, hex-ээр (64 тэмдэгт) — бааз нь
нууцыг өөрийг нь хадгалдаггүй.

Хоёр урхи:

- **`OAUTH_REDIRECT_HOSTS`.** Redirect URI-ийн хост энэ жагсаалтад байх ёстой
  бөгөөд жагсаалт нь дэд домэйныг **өвлүүлдэггүй**: `petronet.mn` бичсэн нь
  `monitor.petronet.mn`-ыг зөвшөөрөхгүй. Хоосон орхивол цөмийн анхдагч
  (`nexus.gerege.mn`) хүчинтэй болж, redirect бүр татгалзагдана.
- **Гарах.** `GF_AUTH_SIGNOUT_REDIRECT_URL` дахь `client_id` нь заавал: logout
  цэг буцах хаягийг тэр клиентийн бүртгэлтэй жагсаалттай тулгадаг. Параметргүй
  бол «post_logout_redirect_uri is not registered» гэж унана — дутуу биш,
  буруу URI мэт уншигдана.

## Нөөцлөлт

`deploy/scripts/backup.sh`, cron дээр өдөр бүр.

```
pg_dump → хуучныг цэвэрлэх → platform_backups-д мөр бичих
        → node_exporter-ийн textfile руу хэмжүүр бичих
        → age-ээр шифрлэж off-site руу илгээх
```

Гурав дахь алхам нь консол уншдаг мөр. Дөрөв дэх нь Prometheus уншдаг —
**шөнө дунд хэн нэгэнд сэрэмжлүүлэг илгээж чадах цорын ганц хувилбар**.
Хэмжигдэхгүй нөөцлөлт нь нөөцлөлт байхгүйтэй бараг адил: cron-ий чимээгүй
бүтэлгүйтэл нь сэргээх өдрөө л илэрдэг.

Хостод зөвхөн age-ийн **нийтийн** түлхүүр байна. Эвдэрсэн платформ өөрийн
илгээсэн зүйлээ уншиж чадахгүй; хувийн түлхүүр нь операторт байна. Түүнийг
хост дээр үлдээвэл шифрлэлт нь утгагүй; түүнийг алдвал нөөцлөлт нь утгагүй.

Off-site сан нь MinIO, объектын хувилбарлалт асаалттай, байршуулалтын
түлхүүр нь **зөвхөн нэмэх** эрхтэй. Тэр нь эвдэрсэн хост нь өөрийн өмнөх
нөөцлөлтүүдийг устгаж чадахгүй гэсэн үг.

Хэмжүүр:

```
nexus_backup_last_run_timestamp_seconds
nexus_backup_last_success_timestamp_seconds
nexus_backup_last_size_bytes
nexus_backup_last_ok
nexus_backup_offsite_ok
```

Бүтэлгүйтсэн ажиллагаа нь өмнөх амжилтын мөчийг **хадгална** — эс бөгөөс нэг
бүтэлгүйтэл нь «хэзээ ч амжилттай болоогүй»-тэй ялгагдахаа болино.
Тохируулаагүй систем дээр `offsite_ok` нь 0 хэвээр байна, тэр нь зөв:
хуулбар өөр газар байхгүй гэдэг нь хэмжигдэх ёстой баримт.

### Сэргээлт

Нөөцлөлт нь сэргээгдэх хүртэл нөөцлөлт биш. Шалгах арга:

```sh
age -d -i key.txt backup.sql.gz.age | gunzip > restore.sql
createdb restore_check && psql restore_check < restore.sql
psql restore_check -c "select count(*) from information_schema.tables
                        where table_schema in ('workspace','registry','operator')"
```

Хаягдах өгөгдлийн сан руу — ажиллаж байгаа руу биш.

## nginx-ийн гурван урхи

Гурвуулаа энэ хост дээр бодитоор гарсан. Эхний хоёр нь **хариу 200 хэвээр
байхад** чимээгүй ажилладаг; гурав дахь нь домэйныг бүтнээр нь унагаадаг ч
`nginx -t` өнгөрдөг тул мөн адил анзаарагдахгүй өнгөрч болно. Тиймээс `curl -I` нь vhost нэмсний дараах заавал хийх
алхам.

**`add_header` өв залгамжлахгүй.** Хүү блок дотор НЭГ ч `add_header` байвал
дээд түвшнийхийг нь **бүгдийг нь** хаяна. `location /` дотор Cache-Control
нэмсэн нь server түвшний дөрвөн толгойг — CSP, HSTS, X-Frame-Options,
X-Content-Type-Options — чимээгүй унтраасан. Хуудас зөв харагдсаар, хариу
200 хэвээр; зөвхөн толгойг нь уншиж байж мэдэгдэнэ. Шийдэл нь бүх
тогтвортой `add_header`-ийг server түвшинд байлгах. Одоо nginx upstream-ийн
давхар хуулбарыг `proxy_hide_header`-ээр хасаад HSTS, frame, MIME, permission
толгойг нэг удаа өөрөө гаргана. CSP бол тогтвортой утга биш: Next.js хүсэлт
бүрд nonce үүсгэж HTML-ийн script-т суулгадаг тул зөвхөн тэр толгойг nginx
өөрчлөлгүй нэвтрүүлнэ.

**Certbot HTTP/2 бичдэггүй.** Тэр нь `listen 443 ssl` мөрийг өөрөө бичдэг ба
протоколыг нэмдэггүй. Үр дүнд нь 2026-08-28-ны HTTP/2 шилжилтийн дараа
үүсгэсэн vhost бүр — `docs`, `backups`, `monitor`, `cp` — HTTP/1.1 дээр
үлдсэн байв. `http2 on;` нь **тусдаа заавар** байх ёстой: тэгвэл гэрчилгээ
шинэчлэх бүрд алга болохгүй.

**Репогийн vhost-ыг дахин хуулах нь TLS-ийг арчина.** `nginx/*.conf` дахь
файлууд зөвхөн `listen 80` мөртэй — 443 блокийг certbot суулгасны дараа
өөрөө нэмдэг. Тиймээс файл өөрчлөгдөөд түүнийг `sites-available` дээр дахин
хуулбал гэрчилгээний хэсэг устаж, домэйн HTTPS дээр бүтэн унана. `nginx -t`
өнгөрнө: синтакс зөв, зүгээр л 80-ыг л сонсож байна. 2026-09-02-нд
`monitor.petronet.mn` дээр яг ингэж болсон. Хуулсан бол дараа нь заавал:

```bash
certbot --nginx -d <нэр> --non-interactive --redirect
```

```bash
curl -s -o /dev/null -w "http/%{http_version} %{http_code}\n" https://<нэр>/
curl -sI https://<нэр>/ | grep -iE 'content-security|strict-transport|x-frame'
```

## Баримтын сайтыг шинэчлэх

`docs.petronet.mn` ба `plan.petronet.mn` хоёр нь **өөрийн nginx контейнертэй**
бөгөөд тэдгээр нь репо доторх `docs/mkdocs/build/site`,
`docs/mkdocs/build-plan/site`-ыг bind mount-оор үйлчилдэг. Хостын nginx нь
зөвхөн 3021 / 3023 руу дамжуулна.

```bash
sh docs/mkdocs/deploy.sh fuelnet-gerege        # docs.petronet.mn
sh docs/mkdocs/deploy.sh fuelnet-gerege plan   # plan.petronet.mn
```

Хоёр урхи, хоёулаа **200 хариу өгсөөр** ажилладаг тул анзаарагдахгүй өнгөрнө:

**`/var/www/docs` руу хуулах нь ямар ч нөлөөгүй.** Цөмийн `deploy.sh` тэгж
хийдэг — тэнд nginx статик хавтаснаас уншдаг. Энд уншдаггүй. Файлууд очих ч
хэн ч уншихгүй, скрипт «published» гэж хэлээд хуудас 404 хэвээр үлдэнэ.

**Дахин угсарсны дараа контейнерыг ДАХИН АСААНА.** `mkdocs build` нь гаралтын
хавтсыг устгаад дахин үүсгэдэг («Cleaning site directory») тул inode нь
солигдоно. Контейнерийн mount хуучин inode дээр үлдэж, доторх nginx устсан
модыг үйлчилсээр байна: шинэ хуудас 404, хуучин хуудас хуучин агуулгаараа
200. `docker ps` эрүүл, `nginx -t` цэвэр, лог чимээгүй. `deploy.sh` үүнийг
өөрөө хийдэг болов.

## Гараар хийгддэг зүйлс

Байршуулалт автомат, гэхдээ эдгээр нь биш. Шинэ хост дээр мартагддаг зүйлс:

- `backup.sh`-ийн cron бичлэг
- TLS дуусах хугацааг бичдэг cron ажил
- Docker 29 дээр cAdvisor-ын containerd тохиргоо (`--containerd-namespace=moby`
  ба `pid: host`) — үүнгүйгээр нэг л цуврал экспортлогдоно, мөн энэ нь
  **дахин эхлүүлэхийг** шаарддаг, дахин үүсгэхийг биш
- Хуучин Docker image-ийн цэвэрлэгээ. `ghcr-retention.yml` нь GHCR талыг
  цэвэрлэдэг ч **хостын дискийг цэвэрлэдэггүй**: deploy бүр хоёр image нэмнэ
  ба тэдгээр нь өөрөө хэзээ ч явахгүй.
  [Гарын авлага](RUNBOOKS.md#nexusdiskfillingup).

Дэлгэрэнгүй алхмуудыг [Гарын авлага](RUNBOOKS.md).
