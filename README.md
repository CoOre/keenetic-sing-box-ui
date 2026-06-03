# keenetic-sing-box-ui

[![CI](https://github.com/CoOre/keenetic-sing-box-ui/actions/workflows/ci.yml/badge.svg)](https://github.com/CoOre/keenetic-sing-box-ui/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/CoOre/keenetic-sing-box-ui?sort=semver)](https://github.com/CoOre/keenetic-sing-box-ui/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
![Platform](https://img.shields.io/badge/platform-Keenetic%20%C2%B7%20aarch64-blue)
![Go](https://img.shields.io/github/go-mod/go-version/CoOre/keenetic-sing-box-ui)

Веб-интерфейс для управления [sing-box](https://github.com/SagerNet/sing-box) на
роутерах **Keenetic** (Entware, архитектура aarch64). Один статически
слинкованный Go-бинарник со встроенным фронтендом (Svelte) — без внешних
зависимостей на роутере.

Возможности:

- управление серверами/outbound'ами (импорт share-ссылок, проверка конфига);
- сборка и применение конфигурации sing-box;
- прозрачный проксинг через `REDIRECT + ipset` (без TUN/TPROXY — работает на
  Keenetic без `/lib/modules`), селективная маршрутизация по доменам и CIDR;
- установка/обновление самого sing-box;
- диагностика, логи, управление сервисом, парольная аутентификация и HTTPS.

> ⚠️ Прозрачный проксинг изменяет правила firewall роутера (iptables/ipset).
> Используйте на свой риск и держите доступ к веб-админке Keenetic, чтобы
> откатить изменения при необходимости.

## Скриншоты

| Обзор | Маршрутизация | Безопасность |
| --- | --- | --- |
| [![Обзор](docs/screenshots/02-overview.png)](docs/screenshots/02-overview.png) | [![Маршрутизация](docs/screenshots/03-routing.png)](docs/screenshots/03-routing.png) | [![Безопасность](docs/screenshots/04-security.png)](docs/screenshots/04-security.png) |

## Архитектура

| Путь            | Что это                                                        |
| --------------- | ------------------------------------------------------------- |
| `cmd/`          | точка входа (`main`, подкоманды `token`, `firewall`)          |
| `internal/`     | бизнес-логика: config, servers, singbox, transparent, auth, … |
| `web/`          | фронтенд на Svelte + Vite, собирается в `web/dist`            |
| `web/embed.go`  | встраивает `web/dist` в бинарник через `go:embed`             |
| `packaging/`    | init-скрипты Entware (`S99…`)                                  |
| `scripts/`      | `install-router.sh` — деплой на живой роутер                  |

## Сборка

Требуется **Go ≥ 1.25** и **Node ≥ 22** (для сборки фронтенда).

```bash
make build          # фронтенд + бинарник под текущую платформу → dist/
make build-arm64    # кросс-сборка под роутер (linux/arm64) → dist/
make test           # go test ./...
make run            # локальный запуск на 127.0.0.1:9091
```

`make build` сначала собирает фронтенд (`npm install && npm run build` в `web/`),
затем компилирует Go-бинарник со встроенными ассетами.

## Деплой на роутер

Установка выполняется **одной командой**. Предусловие: на роутере уже должен
быть рабочий Entware на одном из USB-дисков (см. ниже).

```bash
make install-router
```

Скрипт сам определит адрес роутера по шлюзу по умолчанию вашей машины и
спросит логин (по умолчанию `admin`) и пароль. Дальше он собирает `linux/arm64`,
заливает бинарник по SFTP, прописывает автозапуск через `/opt/etc/initrc` и
стартует сервис. После завершения интерфейс доступен на `http://<роутер>:9091/`.

Чтобы не вводить параметры каждый раз, можно зафиксировать их в `.env`
(`scripts/install-router.sh` подхватывает его автоматически):

```bash
cp .env.example .env && $EDITOR .env   # все поля опциональны
```

Либо передать флагами/переменными окружения (переопределяют `.env` и
автоопределение):

```bash
make install-router ROUTER_HOST=192.168.1.1 ROUTER_USER=admin ROUTER_PASSWORD=secret
# или напрямую:
scripts/install-router.sh --host 192.168.1.1 --reboot
```

Приоритет источников хоста/учётных данных: флаг → переменная окружения / `.env`
→ автоопределение шлюза (хост) / интерактивный запрос (логин и пароль).
Скрипт кроссплатформенный — работает на macOS и Linux.

Полный список параметров — в шапке `scripts/install-router.sh`. Хост-зависимости
скрипта: `bash`, `sshpass`, `ssh`, `sftp`, `make`, `go`, `curl`.

### Предусловие: Entware на роутере

Stock-SSH Keenetic на порту 22 — это CLI (KCommand), а не shell, поэтому
обычная установка по `ssh` невозможна; KeeneticOS также затирает диск opkg при
загрузке, если на нём нет «благословлённого» Entware. Поэтому нужен диск с уже
установленным Entware:

1. Веб-админка → **Управление компонентами** → установить **OPKG**.
2. **Приложения** → **OPKG** → выбрать диск → установить.
3. После перезагрузки запустить `make install-router`.

## Релизы и CI

GitHub Actions (`.github/workflows/ci.yml`) на каждый push гоняет `go vet`,
`go test`, `golangci-lint` и кросс-сборку. По тегу `v*` собирается
`make package` и публикуется релиз с `.tar.gz` под aarch64 и `sha256sums.txt`.

```bash
git tag v0.1.0 && git push origin v0.1.0
```

## Лицензия

[MIT](LICENSE)
