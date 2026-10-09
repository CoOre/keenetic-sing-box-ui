# Changelog

Генерируется из коммитов скриптом `scripts/changelog.sh` (`make changelog`).

## [v0.1.7] — 2026-10-09

### Новое

- **share:** поддержка TUIC v5 ([3162d1b](https://github.com/CoOre/keenetic-sing-box-ui/commit/3162d1b))
  - добавлен протокол tuic: разбор ссылок tuic://uuid:password@host:port (формы v2rayN/NekoBox и Clash/Meta: allow_insecure/skip-cert-verify, congestion_control/congestion-controller, udp_relay_mode/udp-relay-mode, reduce_rtt/reduce-rtt, disable_sni, пароль в ?password=)
  - TUIC v4 (без пароля или version≠5) отклоняется с понятной ошибкой: sing-box умеет только v5
  - outbound tuic: обязательный TLS без uTLS/Reality, ALPN h3 по умолчанию (без него QUIC-хендшейк падает с «no application protocol»), congestion_control, udp_relay_mode, zero_rtt_handshake, tls.disable_sni
  - валидация TUIC: UUID в формах sing-box (36 с дефисами или 32 hex), скобки и urn:uuid: снимаются; неизвестные congestion_control/udp_relay_mode сбрасываются на значения по умолчанию, а не выкидывают сервер из подписки; UUID у vless/vmess не нормализуется (ключ сервера в подписке)
  - значения настроек из ссылок приводятся к виду sing-box (NEW-RENO, newreno → new_reno); insecure=true/allowInsecure=true принимаются и для hy2/vless
  - multiplex включается по списку разрешённых протоколов share.SupportsMultiplex (vless, vmess, trojan, shadowsocks) вместо исключений для QUIC
  - в редакторе сервера: протокол TUIC, раздел с congestion control, передачей UDP и 0-RTT, переключатель «Не отправлять SNI»; ALPN сбрасывается при смене TCP ↔ QUIC; бейдж TUIC в списке серверов
  - добавлены тесты разбора, валидации и сборки; README; пересобраны web/dist-ассеты
- **update:** «Что нового» для ядра sing-box и веб-интерфейса ([e9f5679](https://github.com/CoOre/keenetic-sing-box-ui/commit/e9f5679))
  - добавлен эндпоинт GET /api/update/changelog?target=singbox|ui: версии после установленной (до 15) или описание установленной версии (флаг installed)
  - sing-box: разделы из docs/changelog.md ветки stable (пререлизы пропускаются, дубли заголовков отсеиваются, относительные ссылки — на sing-box.sagernet.org); веб-интерфейс: тексты GitHub-релизов
  - кэш на час, перезагрузка при новой версии не чаще раза в 10 минут, пауза 2 минуты после ошибки с отдачей старых данных; загрузка в фоне без привязки к запросу, параллельные запросы делят одну загрузку
  - добавлен пакет internal/proxyretry: «напрямую → через прокси» с таймаутом на попытку и Permanent-ошибками без повтора; на него переведены установка обновлений, changelog и подписки
  - ProxiedClient переиспользует один http.Transport на порт (утечка keep-alive соединений) и сам проверяет отсутствие настроек
  - в карточке «Обновления» кнопка «Что нового» с раскрывающимся списком изменений; защита от гонок запросов
  - добавлен web/src/lib/markdown.ts: безопасный рендер (экранирование, только http(s)-ссылки), списки, таблицы, код
  - добавлены тесты changelog, proxyretry, ProxiedClient и live-тест (-tags live), пересобраны web/dist-ассеты

### Прочее

- **release:** changelog из коммитов и текст релиза в CI ([2d4c178](https://github.com/CoOre/keenetic-sing-box-ui/commit/2d4c178))
  - добавлен scripts/changelog.sh: разделы по Conventional Commits (Новое, Исправления, Производительность, Рефакторинг, Прочее, Несовместимые изменения), тело коммита переносится как есть, ссылки на коммиты и compare; chore(release) пропускается
  - CI: release-job тянет всю историю и публикует релиз с телом из scripts/changelog.sh notes <tag>
  - добавлены make changelog [NEXT=…] (атомарная запись CHANGELOG.md) и make release V=vX.Y.Z (CHANGELOG.md, коммит chore(release), тег; отказ при грязном дереве, существующем теге и без новых коммитов)
  - добавлен CHANGELOG.md по тегам v0.1.0–v0.1.6, описан процесс в README
- **hooks:** добавлен .githooks/pre-commit ([023fa8d](https://github.com/CoOre/keenetic-sing-box-ui/commit/023fa8d))
  - golangci-lint по staged .go и svelte-check по web/src; подключается make hooks

**Полный список изменений:** [v0.1.6...v0.1.7](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.6...v0.1.7)

## [v0.1.6] — 2026-10-06

### Новое

- **dns:** настраиваемый DNS — серверы, резервные цепочки, правила и перехват клиентов ([9e31bac](https://github.com/CoOre/keenetic-sing-box-ui/commit/9e31bac))
  - добавлены настройки DNS в settings.json (internal/config/dns.go):
    - серверы по URL: UDP/TCP, DoT, DoH, DoH3, DoQ, DHCP и системный local; у каждого выход напрямую или через VPN
    - цепочки «основной → резервные» для сервера по умолчанию, доменов из маршрутизации и ручных правил на DNS-действиях sing-box 1.14 evaluate/respond: при таймауте, SERVFAIL или REFUSED запрос уходит следующему серверу, таймаут настраивается
    - домены маршрутизации и URL-списков резолвятся через выбранную цепочку; домены цепочек хранятся одной копией в inline rule_set
    - стратегия IPv4/IPv6, валидация и нормализация настроек; без поля dns поведение прежнее (DoT 8.8.8.8)
  - добавлены 38 проверенных пресетов по категориям, включая блокировщики рекламы (AdGuard, Mullvad, Control D, DNS4EU, Comss); у заблокированных напрямую в РФ VPN включается по умолчанию
  - добавлен direct-инбаунд dns-in (по умолчанию :1053) с hijack-dns
  - добавлен опциональный перехват DNS клиентов LAN: nat-цепочка ksbui_dns первой в PREROUTING, REDIRECT udp/tcp 53 с RFC1918 на dns-in с учётом политики Keenetic; перехват снимается, если dns-in не слушает или watchdog видит упавший sing-box; CaptureInstalled проверяет и DNS-jump
  - ipset-резолвер и трассировка при перехвате резолвят через sing-box (с откатом на системный DNS), чтобы IP у клиентов и в ipset совпадали
  - добавлены эндпоинты /api/dns (чтение, сохранение с валидацией) и /api/dns/lookup: ответ всей цепочки плюс опрос каждого сервера через служебные правила по source_port 20530+ без кэша, с отметкой, кто ответил
  - PUT /api/settings больше не перезаписывает DNS и проверяет конфликт портов
  - для DNS-серверов с прямым выходом detour не пишется: detour "direct" проходит check, но роняет старт sing-box
  - минимальная версия sing-box поднята до 1.14.0 (singbox.MinVersion): предупреждения на «Обзоре», в «Обновлениях», на экране DNS и после установки через opkg
  - добавлен экран «DNS»: цепочки серверов с выбором пресета и тумблером VPN, «Заблокированные сайты», тумблер перехвата, проверка домена с разбивкой по серверам, свёрнутое «Дополнительно» (свои правила, IP-версия, таймаут, порт)
  - добавлены тесты конфига, валидации, цепочек, фаервола, API и резолвера; запуск реального sing-box ≥1.14 (SINGBOX_BIN); live-проверка пресетов (-tags live); пересобраны web/dist-ассеты
- **subs:** подписки по URL с автообновлением серверов ([6d9e055](https://github.com/CoOre/keenetic-sing-box-ui/commit/6d9e055))
  - добавлен пакет internal/subs:
    - хранилище подписок subs.json (интервал, user-agent, автоприменение, статус последней загрузки)
    - разбор ответа провайдера: share-ссылки plain/base64 (std/url, с переносами строк и BOM), отсев служебных псевдосерверов (0.0.0.0, loopback, порт 1), понятные ошибки для JSON/Clash YAML/HTML
    - разбор заголовков Subscription-Userinfo (трафик, срок) и Profile-Title (в т.ч. base64:)
    - раннер: обновление по расписанию, повтор через 30 мин после ошибки, запасная загрузка через локальный прокси sing-box
  - добавлены в servers.Store поле sub_id, SyncSub (сохраняет ID и флаг основного у совпадающих серверов) и DeleteSub; Save не сбрасывает sub_id
  - добавлены эндпоинты /api/subs (список, добавление с первой загрузкой, изменение, удаление вместе с серверами, ручное обновление)
  - логика «Применить» вынесена в applyServers и переиспользована для автоприменения, которое срабатывает только при запущенном sing-box
  - экспортирован update.ProxiedClient для загрузки подписок через прокси
  - добавлен subs.json в полный бэкап
  - добавлена карточка «Подписки» на экране «Серверы»: трафик, срок, ошибки, модалка с интервалом, автоприменением и User-Agent; у серверов из подписки показан тег подписки, кнопки правки и удаления скрыты
  - добавлен target make hooks (core.hooksPath = .githooks)
  - добавлены тесты subs, SyncSub и API подписок, пересобраны web/dist-ассеты

**Полный список изменений:** [v0.1.5...v0.1.6](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.5...v0.1.6)

## [v0.1.5] — 2026-09-22

### Новое

- **servers:** Hysteria 2, новый редактор сервера и выбор основного сервера ([d83c316](https://github.com/CoOre/keenetic-sing-box-ui/commit/d83c316))
  - добавлен протокол hysteria2: outbound с obfs salamander, port hopping (server_ports/hop_interval), up/down Mbps, TLS без uTLS/Reality; парсинг ссылок hysteria2:// и hy2:// (мультипорт, IPv6, mport)
  - multiplex не добавляется к hysteria2-outbound (sing-box check отвергает поле)
  - добавлена валидация сервера при сохранении (Server.Validate): обязательные поля по типу, trim public_key
  - добавлен выбор основного сервера: флаг primary в servers.json, POST /api/servers/primary с живым переключением selector'а proxy через clash API, default selector'а при apply и повторное выставление после рестарта (cache_file иначе восстанавливает старый выбор)
  - GET /api/servers отдаёт tag каждого сервера и текущий выбор selector'а (selected/auto_now)
  - экран «Серверы» переделан: список с кнопками «Сделать основным»/«Авто», предупреждение при direct; добавление и правка в модалке ServerEditor (разбор ссылки при вставке, выбор протокола, свёрнутые секции TLS/Транспорт/Hysteria 2)
  - исправлены тумблер Reality (пробел в public_key), пустые варианты Flow/Fingerprint, добавлены ALPN и insecure в форму
  - удалены неиспользуемые Dashboard.svelte и ServersPanel.svelte
  - обновлены скриншоты в README (фейковые данные), добавлены тесты share/config/servers/API, пересобраны web/dist-ассеты

**Полный список изменений:** [v0.1.4...v0.1.5](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.4...v0.1.5)

## [v0.1.4] — 2026-07-07

### Новое

- **backup:** экспорт и импорт полного состояния одним архивом ([922fe43](https://github.com/CoOre/keenetic-sing-box-ui/commit/922fe43))
  - добавлен пакет internal/backup и эндпоинты /api/backup/export|import (tar.gz со всем стейтом, валидация до записи, атомарный коммит)
  - после импорта перезапускаются sing-box и фаервол, self-restart UI через init-скрипт или /opt/etc/initrc (новое поле UIInitrc)
  - добавлен веб-блок «Полный бэкап» с подтверждением импорта и ожиданием перезапуска UI
  - добавлены тесты backup/API, пересобраны web/dist-ассеты
- **web:** экран «Соединения» — живые подключения через clash API ([9ea929d](https://github.com/CoOre/keenetic-sing-box-ui/commit/9ea929d))
  - добавлен компонент ScreenConnections: снапшот-опрос GET /api/clash/connections раз в 2 с, расчёт скорости per-connection по дельте байтов, пауза опроса
  - реализованы поиск, фильтр по типу outbound (все/прокси/директ), сортировка по скорости/трафику/времени старта
  - добавлено закрытие соединений: методы api.clashConnectionClose/clashConnectionsCloseAll (DELETE /api/clash/connections[/{id}]) с подтверждением «закрыть все»
  - добавлены типы ClashConnMeta/ClashConnection/ClashConnectionsSnapshot в types.ts
  - добавлен пункт «Соединения» в сайдбар и роутинг Shell, иконки connections/pause в Icon.svelte
  - пересобраны web/dist-ассеты
- **trace:** диагностика POST /api/diag/trace — резолв, ipset, conntrack, outbound ([efb7133](https://github.com/CoOre/keenetic-sing-box-ui/commit/efb7133))
  - добавлен пакет internal/trace: отчёт по цели — резолвинг, совпадения правил, вердикт per-IP, conntrack-флоу, текущий outbound
  - добавлен Engine.TestSetMembership для живой проверки ipset-принадлежности
  - добавлен эндпоинт POST /api/diag/trace и веб-блок «Трассировка маршрута» в диагностике
  - добавлены тесты trace/membership/API, пересобраны web/dist-ассеты
- **update:** автопроверка и автообновление sing-box и веб-интерфейса ([cde5b2e](https://github.com/CoOre/keenetic-sing-box-ui/commit/cde5b2e))
  - добавлен пакет internal/update: Manager с фоновой проверкой GitHub-релизов, детачнутой установкой (не гибнет с HTTP-соединением) и self-restart UI через init-скрипт
  - реализован CompareVersions с поддержкой git-describe-суффиксов (dev-сборка новее тега) и prerelease-версий
  - добавлены эндпоинты GET /api/update/status, POST /api/update/check, POST /api/update/apply; settingsSave декодирует поверх сохранённых настроек, чтобы старые клиенты не сбрасывали новые поля
  - добавлены настройки auto_update_singbox/auto_update_ui/update_check_hours (по умолчанию 6 ч, максимум 168)
  - обобщён singbox.Github под произвольные репозитории (BinName/Suffix, NewGithubUI), extractSingBox переименован в extractBinary
  - добавлен loopback mixed-инбаунд на порту+1 в tproxy/redirect-режимах и ретрай скачивания релизов через локальный прокси sing-box
  - добавлены UIBin/UIInit в system.Paths, экспортирован system.SingBoxVersion
  - добавлен web-компонент UpdatesCard (статус, ручная установка, тумблеры автообновления, интервал), бейдж обновлений в сайдбаре, методы api.updateStatus/updateCheck/updateApply и типы
  - добавлены тесты internal/update и проверка loopback-инбаунда в assemble_test.go
  - пересобраны web/dist-ассеты

### Прочее

- **lint:** tagged switch по nSrc в parseConntrackLine (QF1003) ([8dfeaa3](https://github.com/CoOre/keenetic-sing-box-ui/commit/8dfeaa3))
  - заменена цепочка if/else if на switch в internal/trace/trace.go
- **lint:** исключить (net.PacketConn).Close из errcheck ([db346eb](https://github.com/CoOre/keenetic-sing-box-ui/commit/db346eb))
  defer conn.Close() в ProbeMTU — best-effort cleanup, как и остальные
  Close в списке исключений

**Полный список изменений:** [v0.1.3...v0.1.4](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.3...v0.1.4)

## [v0.1.3] — 2026-06-29

### Новое

- **diag:** подбор MTU/MSS-clamp, статус туннеля и реассерт capture в watchdog ([28dc581](https://github.com/CoOre/keenetic-sing-box-ui/commit/28dc581))
  - добавлен internal/system/mtu.go: ProbeMTU (ICMP+DF бинарный поиск path MTU) и SetMSSClamp/ClearMSSClamp через отдельную mangle-цепочку ksbui_mss
  - добавлены эндпоинты POST /api/diag/mtu, POST/DELETE /api/diag/mtu/clamp и хелпер serverIPv4 в internal/api/api.go
  - добавлен метод Engine.CaptureInstalled (internal/transparent/rules.go) для проверки наличия PREROUTING-перехвата
  - переработан watchdog: вынесен assertFirewall, добавлен реассерт фаервола, если capture-правила снесло при живом sing-box
  - добавлен web-компонент TunnelStatus и блок «Подбор MTU» в ScreenDiagnostics, методы api.probeMTU/applyMSSClamp/clearMSSClamp/clashDelay
  - добавлена зависимость golang.org/x/net, обновлён golang.org/x/crypto в go.mod/go.sum
  - добавлены тесты mtu_test.go и TestCaptureInstalled
  - пересобраны web/dist-ассеты

### Прочее

- refresh ([8154e82](https://github.com/CoOre/keenetic-sing-box-ui/commit/8154e82))

**Полный список изменений:** [v0.1.2...v0.1.3](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.2...v0.1.3)

## [v0.1.2] — 2026-06-11

### Новое

- **web:** общий график трафика и hash-навигация ([23fb248](https://github.com/CoOre/keenetic-sing-box-ui/commit/23fb248))
  - переработан блок трафика: обе серии в одной системе координат, гладкие кривые Catmull-Rom, текущие скорости поверх графика
  - реализовано EMA-сглаживание скоростей (alpha=0.35) против «расчёски» на бёрстовом трафике
  - добавлена hash-навигация между экранами с восстановлением маршрута при загрузке
  - пересобран web/dist
- **config:** домены URL-списков, h2mux и фикс чтения логов ([faae04e](https://github.com/CoOre/keenetic-sing-box-ui/commit/faae04e))
  - переработана сборка tproxy-конфига: final=proxy для всех режимов, удалены внутренние правила выбора domain_suffix/ip_cidr (выбор делает iptables-слой)
  - домены из URL-списков передаются в domain_suffix-правила sing-box (ExtraRouteDomains)
  - исключены ключи cidr4/cidr6 opencck-источников: облачные ASN-блоки утаскивали через прокси посторонние сервисы
  - реализован isHostCIDR: auto-источники отдают только host-маршруты (/32, /128), подсети сохраняются лишь для type=cidr
  - добавлен h2mux (max_connections=4) на прокси-аутбаунды при включённой настройке multiplex
  - ограничено чтение хвоста лога 256 КиБ: io.ReadAll целого файла пробивал GOMEMLIMIT и убивал UI-процесс
  - добавлены тесты lists, обновлены тесты Assemble
- **transparent:** селективный TPROXY по route-ipset и reject-набор ([562e5a0](https://github.com/CoOre/keenetic-sing-box-ui/commit/562e5a0))
  - добавлен матч --match-set route_net dst в TPROXY-правила: в sing-box попадает только трафик из route-набора, остальное уходит напрямую на скорости ядра
  - route-ipset создаётся и сидируется в обоих прозрачных режимах, резолвер доменов включён для tproxy
  - добавлена настройка reject_cidr: отказ на FORWARD (TCP-reset / ICMP unreachable) для придушенных CDN, реализован ipset reject_net
  - переработан filter-leaf: applyFilter общий для обоих режимов, reject проверяется раньше exclude RETURN, QUIC-блок остался только в redirect
  - исправлен FORWARD-jump: -j вместо -g (политика FORWARD — DROP, goto-провал ронял весь немаршрутизируемый трафик)
  - добавлена настройка multiplex в Settings и поле «Блокировать (reject)» в веб-интерфейсе
  - обновлены тесты transparent под селективную модель и filter-leaf

**Полный список изменений:** [v0.1.1...v0.1.2](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.1...v0.1.2)

## [v0.1.1] — 2026-06-05

### Новое

- **transparent:** сделать TProxy рекомендуемым режимом с UDP/QUIC и починить загрузку модулей ([88b322f](https://github.com/CoOre/keenetic-sing-box-ui/commit/88b322f))
  - модули ядра ищутся в /lib/modules и /lib/system-modules (modulesOSDirs)
  - добавлен fallback на `busybox insmod`, когда нет отдельного insmod (insmodFile)
  - QUIC reject применяется только в режиме redirect (TCP-only); tproxy/tun проксируют UDP
  - Up очищает таблицы неактивного режима (cleanTable), чтобы nat REDIRECT и mangle TPROXY не сосуществовали
  - определение реального статуса процесса через init-скрипт status (Service.Running)
  - лимит тела settingsSave поднят с 4 KiB до 256 KiB под большие списки CIDR/доменов
  - обновлены UI и README: TProxy рекомендуется, нужен компонент «Модули ядра для Netfilter»
  - добавлены тесты modules_test.go, проверки QUIC/Running в assemble/system
- **install:** add --from-release to deploy prebuilt binary without compiling ([03523b4](https://github.com/CoOre/keenetic-sing-box-ui/commit/03523b4))
  scripts/install-router.sh can now fetch the prebuilt aarch64 binary from
  the GitHub release (--from-release / --release-tag / ROUTER_RELEASE),
  verify its sha256, and deploy it — no Go/Node toolchain required. Build
  from source remains the default. Document the no-compile path in README.

### Прочее

- **transparent:** починить TestInsmodFile_FallsBackToBusybox на Linux CI ([02829c1](https://github.com/CoOre/keenetic-sing-box-ui/commit/02829c1))
  Ключи фейкового раннера выставлены по резолвнутому toolPath(): на Linux CI insmod резолвится в /sbin/insmod, поэтому ключ "insmod" не матчился, голый insmod "успешен" по Default и busybox-fallback не проверялся.
- correct Entware/OPKG install steps for Keenetic ([4f67716](https://github.com/CoOre/keenetic-sing-box-ui/commit/4f67716))
  Per official Keenetic docs: ext4 formatting is mandatory, install the
  'Open Package support' (OPKG) component, select the drive on the OPKG
  page, verify via system log. Add links to official EN/RU guides.

**Полный список изменений:** [v0.1.0...v0.1.1](https://github.com/CoOre/keenetic-sing-box-ui/compare/v0.1.0...v0.1.1)

## [v0.1.0] — 2026-06-03

### Новое

- keenetic-sing-box-ui — initial public release ([e877f24](https://github.com/CoOre/keenetic-sing-box-ui/commit/e877f24))
  Web UI for managing sing-box on Keenetic routers (Entware/aarch64):
  server/outbound management, config assembly, transparent proxy via
  REDIRECT+ipset, sing-box install/update, diagnostics, auth and HTTPS.
  Single Go binary with embedded Svelte frontend. One-command deploy to a
  live router via scripts/install-router.sh (make install-router).

