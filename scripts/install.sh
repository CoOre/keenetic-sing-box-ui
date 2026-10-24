#!/bin/sh
# install.sh — installs keenetic-sing-box-ui on a Keenetic/Netcraze router in
# one go, through the router's HTTP API (RCI) only: no SSH, no Entware needed
# beforehand. Needs just curl (plus python3 for --file/--relay).
#
#   sh scripts/install.sh [--host 192.168.1.1] [--user admin]
#   curl -fsSL https://raw.githubusercontent.com/CoOre/keenetic-sing-box-ui/main/scripts/install.sh | sh
#
# What it does:
#   1. installs the "OPKG" and "Netfilter kernel modules" firmware components
#      if missing (the router reboots once);
#   2. lists the disks (USB ext4 partitions, the internal storage) with free
#      space and an Entware already on them, offers the fitting one and
#      rescans when a disk is plugged in (r);
#   3. points opkg at that disk with the installer archive URL — the firmware
#      downloads it, unpacks it and runs it: Entware (when the disk has none),
#      iptables/ipset, keenetic-sing-box-ui (packaging/installer/setup.sh);
#   4. shows the progress from the router's system log.
#
# Options (or env vars):
#   --host HOST       ROUTER_HOST      default: this machine's default gateway
#   --user USER       ROUTER_USER      default: admin
#   --password PASS   ROUTER_PASSWORD  asked if empty
#   --disk DISK       ROUTER_DISK      opkg disk, e.g. storage:/ or <uuid>:/
#   --url URL         KSBUI_INSTALLER_URL  installer archive URL
#   --file PATH       serve a local archive (make installer) to the router
#   --relay           download the archive on this machine and serve it to the
#                     router (when the router can't reach GitHub by itself)
#   -y, --yes         don't ask for confirmation
set -eu

REPO=CoOre/keenetic-sing-box-ui
ASSET=keenetic-sing-box-ui-installer-aarch64.tar.gz
ASSET_DIST=dist/$ASSET
DEFAULT_URL="https://raw.githubusercontent.com/$REPO/installer/$ASSET"
RELEASE_URL="https://github.com/$REPO/releases/latest/download/$ASSET"

HOST=${ROUTER_HOST:-}
LOGIN=${ROUTER_USER:-}
PASS=${ROUTER_PASSWORD:-}
DISK=${ROUTER_DISK:-}
URL=${KSBUI_INSTALLER_URL:-}
FILE=""
RELAY=0
YES=0

while [ $# -gt 0 ]; do
	case "$1" in
	--host) HOST=$2; shift ;;
	--user) LOGIN=$2; shift ;;
	--password) PASS=$2; shift ;;
	--disk) DISK=$2; shift ;;
	--url) URL=$2; shift ;;
	--file) FILE=$2; shift ;;
	--relay) RELAY=1 ;;
	-y | --yes) YES=1 ;;
	-h | --help) sed -n '2,/^set -eu/p' "$0" | sed '$d; s/^# \{0,1\}//'; exit 0 ;;
	*) echo "неизвестный аргумент: $1" >&2; exit 2 ;;
	esac
	shift
done

say() { printf '%s\n' "$*"; }
die() { printf 'ОШИБКА: %s\n' "$*" >&2; exit 1; }

# Prompts read the terminal: under "curl | sh" stdin is the script itself.
ask() {
	printf '%s' "$1" >/dev/tty
	IFS= read -r REPLY </dev/tty || REPLY=""
}

command -v curl >/dev/null 2>&1 || die "нужен curl"

md5hex() {
	if command -v md5sum >/dev/null 2>&1; then printf %s "$1" | md5sum | cut -d' ' -f1
	elif command -v md5 >/dev/null 2>&1; then printf %s "$1" | md5 -q
	else printf %s "$1" | openssl dgst -md5 -r | cut -d' ' -f1; fi
}
sha256hex() {
	if command -v sha256sum >/dev/null 2>&1; then printf %s "$1" | sha256sum | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then printf %s "$1" | shasum -a 256 | cut -d' ' -f1
	else printf %s "$1" | openssl dgst -sha256 -r | cut -d' ' -f1; fi
}

gateway() {
	case "$(uname -s)" in
	Darwin) route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}' ;;
	*) ip route show default 2>/dev/null | awk '{for (i=1;i<NF;i++) if ($i=="via") {print $(i+1); exit}}' ;;
	esac
}

# ---------------------------------------------------------------------------
# Router API
# ---------------------------------------------------------------------------

TMP=$(mktemp -d)
SERVER_PID=""
WATCH_PID=""
cleanup() {
	[ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null
	[ -n "$WATCH_PID" ] && kill "$WATCH_PID" 2>/dev/null
	rm -rf "$TMP"
}
trap cleanup EXIT
trap 'exit 130' INT TERM
JAR=$TMP/cookies

http() { curl -sS --noproxy '*' -m "${TIMEOUT:-30}" -b "$JAR" -c "$JAR" "$@"; }

# Challenge-response login: sha256(challenge + md5(login:realm:password)).
login() {
	hdr=$(http -o /dev/null -D - "$BASE/auth" 2>/dev/null | tr -d '\r') || return 1
	case "$hdr" in
	"HTTP/1.1 200"*) return 0 ;;
	esac
	realm=$(printf '%s\n' "$hdr" | sed -n 's/^[Xx]-[Nn][Dd][Mm]-[Rr]ealm: //p')
	challenge=$(printf '%s\n' "$hdr" | sed -n 's/^[Xx]-[Nn][Dd][Mm]-[Cc]hallenge: //p')
	[ -n "$challenge" ] || return 1
	hash=$(sha256hex "$challenge$(md5hex "$LOGIN:$realm:$PASS")")
	code=$(http -o /dev/null -w '%{http_code}' -H 'Content-Type: application/json' \
		-d "{\"login\":\"$LOGIN\",\"password\":\"$hash\"}" "$BASE/auth") || return 1
	[ "$code" = 200 ]
}

rci_get() { http -f "$BASE/rci/$1"; }
rci_post() { http -f -H 'Content-Type: application/json' -d "$2" "$BASE/rci/$1"; }
cli() { rci_post "" "[{\"parse\":\"$1\"}]"; }

# json_str KEY: first "KEY": "value" of pretty-printed RCI JSON on stdin.
json_str() { sed -n "s/^ *\"$1\": \"\\([^\"]*\\)\".*/\\1/p" | head -n 1; }

uptime_s() { rci_get show/system 2>/dev/null | json_str uptime; }

# ---------------------------------------------------------------------------
# 1. Connect
# ---------------------------------------------------------------------------

if [ -z "$HOST" ]; then
	gw=$(gateway || true)
	ask "Адрес роутера [${gw:-192.168.1.1}]: "
	HOST=${REPLY:-${gw:-192.168.1.1}}
fi
if [ -z "$LOGIN" ]; then
	ask "Логин [admin]: "
	LOGIN=${REPLY:-admin}
fi
if [ -z "$PASS" ]; then
	stty -echo </dev/tty 2>/dev/null || true
	ask "Пароль $LOGIN@$HOST: "
	stty echo </dev/tty 2>/dev/null || true
	printf '\n' >/dev/tty
	PASS=$REPLY
fi
BASE="http://$HOST"

say "==> Подключаюсь к $HOST"
login || die "не удалось войти на http://$HOST (адрес, логин или пароль?)"

ver=$(rci_get show/version) || die "роутер не отвечает на RCI"
model=$(printf '%s\n' "$ver" | json_str description)
arch=$(printf '%s\n' "$ver" | json_str arch)
say "    $model, KeeneticOS $(printf '%s\n' "$ver" | json_str title), $arch"
[ "$arch" = aarch64 ] || die "архитектура $arch не поддерживается (нужен aarch64)"

# ---------------------------------------------------------------------------
# 2. Firmware components
# ---------------------------------------------------------------------------

comps=$(printf '%s\n' "$ver" | json_str components)
missing=""
for c in opkg opkg-kmod-netfilter; do
	case ",$comps," in *",$c,"*) ;; *) missing="$missing $c" ;; esac
done
if [ -n "$missing" ]; then
	say "==> Нужны компоненты прошивки:$missing"
	say "    (OPKG — для пакетов, «Модули ядра для Netfilter» — для режима TProxy)"
	if [ "$YES" != 1 ]; then
		ask "    Установить? Роутер перезагрузится. [Y/n] "
		case "$REPLY" in [nNнН]*) exit 1 ;; esac
	fi
	# The firmware installs only components from a catalogue it has fetched
	# from the update server ("commit without a component list" otherwise).
	i=0
	until rci_post components/list '{}' 2>/dev/null | grep -q '"component"'; do
		i=$((i + 1))
		[ "$i" -le 30 ] || die "роутер не получил список компонентов (нет интернета?)"
		sleep 2
	done
	for c in $missing; do
		cli "components install $c" | grep -q 'queued for installation' ||
			die "компонент $c недоступен для этой модели"
	done
	up0=$(uptime_s)
	up0=${up0:-999999999}
	cli "components commit" >/dev/null || die "не удалось запустить обновление компонентов"
	say "    Роутер скачивает компоненты и перезагрузится — это несколько минут…"
	deadline=$(($(date +%s) + 900))
	while :; do
		[ "$(date +%s)" -lt "$deadline" ] || die "роутер не вернулся за 15 минут"
		sleep 5
		if TIMEOUT=5 login 2>/dev/null; then
			up=$(TIMEOUT=5 uptime_s || true)
			[ -n "$up" ] && [ "$up" -lt "$up0" ] && break
		fi
	done
	ver=$(rci_get show/version)
	comps=$(printf '%s\n' "$ver" | json_str components)
	for c in $missing; do
		case ",$comps," in *",$c,"*) ;; *) die "компонент $c не установился" ;; esac
	done
	say "    Компоненты установлены"
fi

# ---------------------------------------------------------------------------
# 3. Disk
# ---------------------------------------------------------------------------

# Rough space needs, MB of files: the installer archive (the firmware
# downloads it to the disk and removes it afterwards), Entware with
# iptables/ipset/ip-full, the UI, sing-box (installed from the UI afterwards)
# and the second sing-box copy an update writes before it swaps the binaries.
# UBIFS (the internal storage) compresses them to about half: Entware, the UI
# and sing-box take ~42 MB there.
ARCHIVE_MB=10
ENTWARE_MB=15
UI_MB=10
SINGBOX_MB=80

human_mb() { awk -v m="$1" 'BEGIN { if (m >= 1024) printf "%.1f ГБ", m / 1024; else printf "%d МБ", m }'; }

# media: one line per partition from show/media:
# bus|uuid|label|fstype|state|total|free|opkg|vendor
media() {
	rci_get show/media | awk -F'"' '
		function trim(s) { gsub(/^[ \t]+|[ \t]+$/, "", s); return s }
		function flush() {
			if (uuid != "") print bus "|" uuid "|" label "|" fstype "|" state "|" total "|" free "|" opkg "|" vendor
			uuid = ""
		}
		$2 == "bus"          { flush(); bus = $4; vendor = "" }
		$2 == "manufacturer" { vendor = trim($4) }
		$2 == "product"      { p = trim($4); vendor = vendor (vendor == "" || p == "" ? "" : " ") p }
		$2 == "uuid"         { flush(); uuid = $4; label = fstype = state = ""; total = free = opkg = 0 }
		$2 == "label"        { label = trim($4) }
		$2 == "fstype"       { fstype = $4 }
		$2 == "state"        { state = $4 }
		$2 == "total"        { total = $4 }
		$2 == "free"         { free = $4 }
		$2 == "opkg" && NF == 3 { opkg = 1 }
		END { flush() }'
}

# media_sig: what changes when a disk is plugged in, mounted or removed. It
# reads the session cookie without writing the jar: it runs in the background.
media_sig() {
	curl -sS --noproxy '*' -m 10 -b "$JAR" -f "$BASE/rci/show/media" 2>/dev/null |
		awk -F'"' '$2 == "uuid" || $2 == "state" { printf "%s;", $4 }'
}

# scan_disks: writes $TMP/disks, one partition per line:
# disk|bus|title|fstype|state|free_mb|total_mb|opkg|entware|ui|singbox
# where opkg marks the current opkg disk and entware/ui/singbox what is
# already installed on it.
scan_disks() {
	media >"$TMP/media" || return 1
	: >"$TMP/disks"
	while IFS='|' read -r bus uuid label fstype state total free opkg vendor; do
		case "$bus" in
		mtd) disk=storage:/ title="Внутренняя память роутера" ;;
		*) disk="$uuid:/" title="USB «${label:-${vendor:-накопитель}}»" ;;
		esac
		[ "$disk" = "$current" ] && opkg=1
		entware=0 ui=0 sb=0
		if [ "$state" = MOUNTED ]; then
			bin=$(cli "ls ${disk}bin" 2>/dev/null </dev/null || true)
			case "$bin" in *'"opkg": {'*) entware=1 ;; esac
			case "$bin" in *'"keenetic-sing-box-ui": {'*) ui=1 ;; esac
			case "$bin" in *'"sing-box": {'*) sb=1 ;; esac
		fi
		echo "$disk|$bus|$title|$fstype|$state|$((total / 1048576))|$((free / 1048576))|$opkg|$entware|$ui|$sb"
	done <"$TMP/media" >"$TMP/disks"
}

# assess LINE: splits a $TMP/disks line into d_* and sets FIT to
#   none  — the partition can't take Entware (NOTE says why),
#   low   — less free space than the install needs,
#   tight — the install fits, a later sing-box update does not,
#   ok;
# NEED is the MB the install needs, NOTE a remark on the space and HAVE one
# on what is already there.
assess() {
	IFS='|' read -r d_disk d_bus d_title d_fs d_state d_total d_free d_opkg d_entware d_ui d_sb <<EOF
$1
EOF
	FIT=none NOTE="" HAVE="" NEED=0
	if [ "$d_state" != MOUNTED ]; then
		NOTE="не смонтирован ($d_state) — подождите и обновите список"
		return 0
	fi
	case "$d_bus|$d_fs" in
	mtd\|* | *\|ext[234]) ;;
	*) NOTE="не подходит: нужна файловая система ext4, а здесь ${d_fs:-неизвестная}"; return 0 ;;
	esac
	NEED=$UI_MB
	[ "$d_entware" = 1 ] || NEED=$((NEED + ENTWARE_MB))
	[ "$d_sb" = 1 ] || NEED=$((NEED + SINGBOX_MB))
	room=$((NEED + SINGBOX_MB))
	NEED=$((NEED + ARCHIVE_MB))
	if [ "$d_fs" = ubifs ]; then
		NEED=$((NEED / 2))
		room=$((room / 2))
	fi
	if [ "$d_free" -lt "$NEED" ]; then
		FIT=low NOTE="мало места: нужно около $(human_mb "$NEED")"
	elif [ "$d_total" -lt "$room" ]; then
		FIT=tight NOTE="места впритык: обновить sing-box из интерфейса здесь не получится: для новой версии не хватит места"
	elif [ "$d_free" -lt "$room" ]; then
		FIT=tight NOTE="места впритык: для обновления sing-box освободите ещё около $(human_mb $((room - d_free)))"
	else
		FIT=ok
	fi
	if [ "$d_ui" = 1 ]; then HAVE="уже есть Entware и keenetic-sing-box-ui — обновлю, настройки сохранятся"
	elif [ "$d_entware" = 1 ]; then HAVE="уже есть Entware — поставлю в него"
	fi
	if [ "$d_opkg" = 1 ]; then HAVE="${HAVE:-Entware не найден}; сейчас выбран для OPKG"; fi
	return 0
}

# default_disk: the index (line number in $TMP/disks) of the partition to
# offer: the current opkg disk, else one with Entware, else a USB disk with
# the most free space, else the internal storage — among those it fits on.
default_disk() {
	i=0 best="" best_score=-1
	while IFS= read -r line; do
		i=$((i + 1))
		assess "$line"
		case "$FIT" in none | low) continue ;; esac
		score=$((d_opkg * 8 + d_entware * 4 + $([ "$d_bus" = usb ] && echo 2 || echo 0)))
		[ "$FIT" = ok ] && score=$((score + 1))
		if [ "$score" -gt "$best_score" ] || { [ "$score" -eq "$best_score" ] && [ "$d_free" -gt "$best_free" ]; }; then
			best=$i best_score=$score best_free=$d_free
		fi
	done <"$TMP/disks"
	echo "$best"
}

show_disks() {
	say "==> Куда установить:"
	i=0
	while IFS= read -r line; do
		i=$((i + 1))
		assess "$line"
		where="$d_title"
		[ "$d_bus" = usb ] && where="$where, ${d_fs:-?}"
		if [ "$FIT" = none ]; then
			say "    -  $where"
			say "       $NOTE"
			continue
		fi
		say "    $i) $where — свободно $(human_mb "$d_free") из $(human_mb "$d_total")"
		[ -n "$HAVE" ] && say "       $HAVE"
		[ -n "$NOTE" ] && say "       $NOTE"
	done <"$TMP/disks"
	if [ -n "$current" ] && ! grep -q "^$current|" "$TMP/disks"; then
		say "    !  OPKG сейчас настроен на $current, но этого диска нет среди подключённых"
	fi
	[ -s "$TMP/disks" ] || say "       (носителей нет)"
	say "    r) обновить список — если подключили накопитель"
	return 0
}

# pick_disk: interactive menu; sets DISK and CHOSEN (its $TMP/disks line).
pick_disk() {
	while :; do
		scan_disks || die "роутер не отдал список носителей"
		def=$(default_disk)
		show_disks
		if [ -z "$def" ]; then
			say "    Нет носителя, где хватит места. Подключите USB-накопитель с ext4"
			say "    (не меньше $(human_mb $((ARCHIVE_MB + ENTWARE_MB + UI_MB + 2 * SINGBOX_MB)))) и введите r."
		fi
		# Tell about disks plugged in while the menu waits for an answer.
		sig=$(media_sig)
		(
			while sleep 3; do
				now=$(media_sig) || continue
				if [ -n "$now" ] && [ "$now" != "$sig" ]; then
					printf '\n    Носители изменились — введите r, чтобы обновить список: ' >/dev/tty
					sig=$now
				fi
			done
		) &
		WATCH_PID=$!
		ask "Выберите [${def:-r}]: "
		kill "$WATCH_PID" 2>/dev/null
		WATCH_PID=""
		REPLY=${REPLY:-${def:-r}}
		case "$REPLY" in
		r | R | к | К) continue ;;
		q | Q | й | Й) exit 1 ;;
		esac
		case "$REPLY" in
		*[!0-9]* | 0*) say "    Введите номер из списка или r"; continue ;;
		esac
		CHOSEN=$(sed -n "${REPLY}p" "$TMP/disks")
		[ -n "$CHOSEN" ] || { say "    Нет пункта $REPLY"; continue; }
		assess "$CHOSEN"
		case "$FIT" in
		none) say "    $d_title: $NOTE"; continue ;;
		low)
			ask "    Места может не хватить ($(human_mb "$d_free") из нужных $(human_mb "$NEED")). Всё равно сюда? [y/N] "
			case "$REPLY" in [yYдД]*) ;; *) continue ;; esac
			;;
		esac
		DISK=$d_disk
		return 0
	done
}

current=$(rci_get opkg 2>/dev/null | json_str disk || true)
CHOSEN=""
if [ -n "$DISK" ]; then
	scan_disks || die "роутер не отдал список носителей"
	CHOSEN=$(grep "^$DISK|" "$TMP/disks" || true)
	if [ -n "$CHOSEN" ]; then
		assess "$CHOSEN"
		[ "$FIT" != none ] || die "$DISK: $NOTE"
	else
		say "==> Диск $DISK не нашёлся среди носителей — ставлю как указано"
	fi
elif [ "$YES" = 1 ]; then
	scan_disks || die "роутер не отдал список носителей"
	def=$(default_disk)
	if [ -z "$def" ]; then
		show_disks
		die "нет носителя, где хватит места"
	fi
	CHOSEN=$(sed -n "${def}p" "$TMP/disks")
	DISK=${CHOSEN%%|*}
else
	pick_disk
fi
HAD_ENTWARE=0
if [ -n "$CHOSEN" ]; then
	assess "$CHOSEN"
	HAD_ENTWARE=$d_entware
	say "==> Диск: $d_title ($DISK), свободно $(human_mb "$d_free")"
	[ -n "$NOTE" ] && say "    $NOTE"
	[ -n "$HAVE" ] && say "    $HAVE"
	if [ "$d_bus" = mtd ]; then
		say "    USB-накопитель с ext4 бережнее к флеш-памяти роутера и просторнее"
	fi
fi
SWITCH=""
if [ -n "$current" ] && [ "$current" != "$DISK" ]; then
	SWITCH="OPKG переключится с $current на $DISK: Entware и пакеты на $current перестанут запускаться (файлы останутся)."
	grep "^$current|" "$TMP/disks" 2>/dev/null | cut -d'|' -f10 | grep -q 1 &&
		SWITCH="$SWITCH Настройки keenetic-sing-box-ui остались там — перенесите их резервной копией (Дополнительно → Резервные копии)."
	# Without -y the confirmation below repeats it.
	[ "$YES" = 1 ] && say "    ВНИМАНИЕ: $SWITCH"
fi

# ---------------------------------------------------------------------------
# 4. Installer archive
# ---------------------------------------------------------------------------

# serve_file PATH: serves the archive to the router from this machine.
serve_file() {
	command -v python3 >/dev/null 2>&1 || die "для --file/--relay нужен python3"
	[ -f "$1" ] || die "нет файла $1"
	case "$(uname -s)" in
	Darwin)
		ifc=$(route -n get "$HOST" 2>/dev/null | awk '/interface:/{print $2; exit}')
		myip=$(ipconfig getifaddr "$ifc" 2>/dev/null || true) ;;
	*) myip=$(ip route get "$HOST" 2>/dev/null | sed -n 's/.* src \([0-9.]*\).*/\1/p' | head -n 1) ;;
	esac
	[ -n "$myip" ] || die "не удалось определить адрес этой машины в сторону роутера"
	mkdir -p "$TMP/www"
	cp "$1" "$TMP/www/$ASSET"
	port=8765
	# The firmware wants application/octet-stream (it rejects application/gzip).
	python3 -I - "$TMP/www" "$myip" "$port" <<'EOF' &
import functools, http.server, sys
class H(http.server.SimpleHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def guess_type(self, path): return "application/octet-stream"
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer((sys.argv[2], int(sys.argv[3])),
    functools.partial(H, directory=sys.argv[1])).serve_forever()
EOF
	SERVER_PID=$!
	sleep 1
	kill -0 "$SERVER_PID" 2>/dev/null || die "не удалось запустить HTTP-сервер на $myip:$port"
	URL="http://$myip:$port/$ASSET"
}

if [ "$RELAY" = 1 ]; then
	say "==> Скачиваю установщик на этот компьютер"
	curl -fL --progress-bar -o "$TMP/$ASSET" "$RELEASE_URL" || die "не удалось скачать $RELEASE_URL"
	FILE=$TMP/$ASSET
fi
if [ -n "$FILE" ]; then
	serve_file "$FILE"
	say "    Раздаю роутеру: $URL"
fi
URL=${URL:-$DEFAULT_URL}

# The firmware wants a direct 200 (no redirects) and unpacks whatever comes,
# an error page too — so check the link here, before touching the router.
# A local archive is served on the LAN: no proxy for it.
[ -n "$FILE" ] && export NO_PROXY='*' no_proxy='*'
probe=$(curl -sS -m 20 -I -o /dev/null -w '%{http_code}|%{content_type}' "$URL" 2>/dev/null || true)
code=${probe%%|*}
case "$code" in
200)
	case "${probe#*|}" in
	application/gzip* | application/x-gzip*)
		say "    ВНИМАНИЕ: $URL отдаётся как ${probe#*|}, а прошивка такие архивы не принимает" ;;
	esac
	;;
000 | "") say "    Не удалось проверить $URL с этого компьютера — пробую как есть" ;;
3??) die "$URL перенаправляет на другой адрес, а прошивка редиректы не поддерживает — нужна прямая ссылка" ;;
404)
	[ "$URL" = "$DEFAULT_URL" ] &&
		die "установочный архив ещё не опубликован (ветка installer появляется с первым релизом). Соберите его сами: make installer && sh scripts/install.sh --file $ASSET_DIST"
	die "$URL не найден (HTTP 404)"
	;;
*) die "$URL отвечает HTTP $code" ;;
esac

if [ "$YES" != 1 ]; then
	say ""
	say "Будет установлено на $HOST (диск $DISK):"
	say "  Entware (если его нет), iptables, ipset, keenetic-sing-box-ui"
	say "  из $URL"
	[ -n "$SWITCH" ] && say "  $SWITCH"
	ask "Продолжить? [Y/n] "
	case "$REPLY" in [nNнН]*) exit 1 ;; esac
fi

# ---------------------------------------------------------------------------
# 5. Install: the firmware downloads, unpacks and runs the archive
# ---------------------------------------------------------------------------

# log_since ID: router log entries newer than ID as "id|ident|message".
log_since() {
	rci_post show/log '{"max-lines": 200}' | awk -F'"' -v since="$1" '
		$2 == "message" && NF >= 5 {
			msg = $4
			for (i = 5; i < NF; i++) msg = msg "\"" $i
			gsub(/\\"/, "\"", msg)
		}
		$2 == "ident" { ident = $4 }
		$2 == "id"    { id = $3; gsub(/[^0-9]/, "", id); if (id + 0 > since + 0) print id "|" ident "|" msg }'
}

# clean_downloads DISK: removes archives a failed install left in <disk>/install:
# the firmware unpacks every one of them again on the next "opkg disk".
clean_downloads() {
	cli "ls ${1}install" 2>/dev/null | sed -n 's/^ *"\([^"]*\.tar\.gz\)": {.*/\1/p' |
		while IFS= read -r f; do
			cli "erase ${1}install/$f" >/dev/null 2>&1 || true
		done
}

last=$(log_since 0 | tail -n 1 | cut -d'|' -f1)
last=${last:-0}

# The disk may have been unplugged while the questions waited.
if [ -n "$CHOSEN" ]; then
	scan_disks || die "роутер не отдал список носителей"
	grep "^$DISK|" "$TMP/disks" | cut -d'|' -f5 | grep -qx MOUNTED ||
		die "диск $DISK больше не подключён — запустите установку заново"
fi

say "==> Устанавливаю"
clean_downloads "$DISK"
[ -n "$current" ] && cli "no opkg disk" >/dev/null
cli "opkg disk $DISK $URL" >/dev/null || die "роутер отклонил opkg disk $DISK"
cli "system configuration save" >/dev/null || true

result=""
deadline=$(($(date +%s) + 1200))
while [ -z "$result" ]; do
	[ "$(date +%s)" -lt "$deadline" ] || die "установка не закончилась за 20 минут — см. системный журнал роутера"
	sleep 3
	lines=$(log_since "$last" 2>/dev/null) || { TIMEOUT=5 login 2>/dev/null || true; continue; }
	[ -n "$lines" ] || continue
	last=$(printf '%s\n' "$lines" | tail -n 1 | cut -d'|' -f1)
	printf '%s\n' "$lines" | while IFS='|' read -r _ ident msg; do
		case "$ident|$msg" in
		keenetic-sing-box-ui\|* | installer\|\[* | installer\|Critical* | npkg\|*failed*)
			say "    $msg" ;;
		ndm\|Opkg::Manager:\ download* | ndm\|Opkg::Manager:\ *failed*)
			say "    ${msg#Opkg::Manager: }" ;;
		esac
	done
	case "$lines" in
	*"|keenetic-sing-box-ui|Готово"*) result=ok ;;
	*"|keenetic-sing-box-ui|ОШИБКА"*) result=fail ;;
	*"|ndm|Opkg::Manager: system failed"*) result=fail ;;
	esac
done

if [ "$result" != ok ]; then
	# Bring the previous Entware back: the firmware starts it again as soon as
	# its disk is set (without a URL nothing is downloaded).
	clean_downloads "$DISK"
	if [ -n "$current" ]; then
		cli "no opkg disk" >/dev/null || true
		if cli "opkg disk $current" >/dev/null; then
			cli "system configuration save" >/dev/null || true
			say "    OPKG возвращён на $current — прежняя установка снова запущена"
		fi
	fi
	die "установка не удалась — подробности выше"
fi
say ""
say "Готово! Откройте http://$HOST:9091/ и задайте пароль при первом входе."
if [ "$HAD_ENTWARE" = 0 ]; then
	say "Entware открыл SSH для root с паролем «keenetic» — смените его (passwd) или отключите dropbear."
fi
