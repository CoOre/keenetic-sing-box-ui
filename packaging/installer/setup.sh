#!/opt/bin/sh
# Second stage of the keenetic-sing-box-ui installer archive, started in the
# background by /opt/etc/init.d/S00keenetic-sing-box-ui-setup right after
# KeeneticOS unpacked the archive into /opt.
#
# Progress goes to the router's system log (tag keenetic-sing-box-ui), where
# the web admin and scripts/install.sh show it, and to $LOG.

export PATH=/opt/sbin:/opt/bin:/opt/usr/sbin:/opt/usr/bin:/usr/sbin:/usr/bin:/sbin:/bin
TZ=$(cat /var/TZ 2>/dev/null)
export TZ="${TZ:-UTC}"

NAME=keenetic-sing-box-ui
DIR=/opt/share/$NAME/install
HOOK=/opt/etc/init.d/S00$NAME-setup
LOG=/opt/var/log/$NAME-install.log
LOCK=/tmp/$NAME-setup.lock # tmpfs: a stale lock must not outlive a reboot
PKGS="iptables ipset ip-full"

mkdir -p /opt/var/log /opt/var/run

log() {
	echo "$(date '+%F %T') $*" >>"$LOG"
	logger -t "$NAME" "$*"
}

fail() {
	log "ОШИБКА: $*"
	exit 1
}

# rc.unslung may run "start" again (e.g. an opkg disk toggle) while we work.
mkdir "$LOCK" 2>/dev/null || exit 0
trap 'rmdir "$LOCK"' EXIT

log "Установка $(cat "$DIR/version" 2>/dev/null)"

# On a disk without Entware NDM runs the archive's init scripts itself, and
# Entware's doinstall (also from the archive) installs Entware, deleting
# itself when done. On a disk with Entware NDM runs rc.unslung instead, which
# ignores doinstall: nobody picks it up, and it is a leftover to remove.
if [ -e /opt/etc/init.d/doinstall ]; then
	idle=0
	seen=0
	while [ -e /opt/etc/init.d/doinstall ] && [ "$idle" -lt 15 ]; do
		if pgrep -f /opt/bin/install >/dev/null 2>&1; then
			[ "$seen" = 1 ] || log "Устанавливается Entware…"
			seen=1
			idle=0
		else
			idle=$((idle + 1))
		fi
		sleep 2
	done
	if [ "$seen" = 0 ] && [ -e /opt/etc/init.d/doinstall ] &&
		[ -x /opt/etc/init.d/rc.unslung ] && [ -x /opt/bin/opkg ]; then
		rm -f /opt/etc/init.d/doinstall /opt/bin/install
	else
		# The installer stops at the first package that fails to download, and
		# bin.entware.net now and then stalls a connection until the TCP
		# timeout. Rerunning it is safe: opkg skips what is already installed.
		for i in 1 2 3; do
			[ -e /opt/etc/init.d/doinstall ] || break
			log "Установка Entware прервалась — повторяю ($i/3)"
			(
				unset LD_BIND_NOW LD_LIBRARY_PATH LD_PRELOAD
				exec /opt/bin/sh /opt/bin/install
			) </dev/null >/dev/null 2>&1
		done
		[ -e /opt/etc/init.d/doinstall ] &&
			fail "Entware не установился — причина в системном журнале (installer)"
		log "Entware установлен"
	fi
fi

log "Ставлю пакеты: $PKGS"
ok=0
for i in 1 2 3 4 5; do
	if opkg update >>"$LOG" 2>&1 && opkg install $PKGS >>"$LOG" 2>&1; then
		ok=1
		break
	fi
	sleep 10
done
[ "$ok" = 1 ] || log "ВНИМАНИЕ: не удалось поставить $PKGS — без них прозрачный режим не заработает. Повторите вручную: opkg install $PKGS"

if [ -f "$DIR/$NAME" ]; then
	[ -x /opt/etc/init.d/S99$NAME ] && /opt/etc/init.d/S99$NAME stop >>"$LOG" 2>&1
	chmod 755 "$DIR/$NAME" "$DIR/S99$NAME" || fail "нет файлов в $DIR"
	# rename(2): safe even if the old binary is still running.
	mv -f "$DIR/$NAME" /opt/bin/$NAME || fail "не удалось установить /opt/bin/$NAME"
	mv -f "$DIR/S99$NAME" /opt/etc/init.d/S99$NAME || fail "не удалось установить init-скрипт"
fi
[ -x /opt/bin/$NAME ] || fail "нет /opt/bin/$NAME"

/opt/etc/init.d/S99$NAME restart >>"$LOG" 2>&1
up=0
for i in $(seq 1 30); do
	if wget -q -O /dev/null http://127.0.0.1:9091/ 2>/dev/null; then
		up=1
		break
	fi
	sleep 1
done
[ "$up" = 1 ] || fail "интерфейс не запустился — см. /opt/var/log/$NAME.log"

rm -f "$HOOK"
rm -rf "$DIR"
rmdir "${DIR%/*}" 2>/dev/null

lan=$(wget -q -O - http://127.0.0.1:79/rci/show/interface/Bridge0 2>/dev/null |
	sed -n 's/^ *"address": "\([0-9.]*\)".*/\1/p' | head -n 1)
log "Готово: откройте http://${lan:-<адрес роутера>}:9091/"
