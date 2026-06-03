#!/usr/bin/env bash
set -euo pipefail

# install-router.sh — installer for keenetic-sing-box-ui on a Keenetic router
# that already has a working Entware install on one of its USB disks.
#
# Why an existing Entware is required:
#   - Keenetic stock SSH on port 22 is the KCommand CLI, not a shell. We can't
#     scp+ssh-and-run a regular install.
#   - KeeneticOS wipes the configured opkg disk on every boot unless it's
#     already a "blessed" Entware install. We can't bootstrap Entware from
#     scratch over the network without shell access. So we require one disk
#     that already holds Entware. If you don't have one yet, follow the
#     official path: web admin → Component manager → install OPKG, then
#     install Entware via web admin → Applications → OPKG on the chosen disk
#     (this involves Keenetic's own bootstrap that survives the boot-wipe).
#
# What this script does, given that prerequisite:
#   - Reuses the Entware disk as the opkg disk and writes our files into it
#     via SFTP (admin can write to subdirs of the opkg disk's CIFS share even
#     though it can't write to /opt/... through SFTP path translation).
#   - Removes the old sing-box (binary, init scripts, configs, cron entry).
#   - Replaces /opt/etc/initrc with one that:
#       * starts crond (preserved from the original),
#       * applies a pending binary update from staging if newer,
#       * (re)starts keenetic-sing-box-ui.
#   - KeeneticOS runs /opt/etc/initrc on every boot when the configured opkg
#     disk holds a valid Entware install, so auto-start just works after this.
#   - Triggers `opkg chroot` toggle so the service starts immediately,
#     without requiring a reboot.
#
# Inputs (env vars; CLI flags override; .env in repo root is auto-loaded):
#   ROUTER_HOST           default: auto-detect the machine's default gateway
#                         (the router on a typical LAN); confirmed/overridable
#                         at an interactive prompt
#   ROUTER_USER           prompted if empty (default admin)
#   ROUTER_PORT           default 22
#   ROUTER_PASSWORD       prompted if empty
#   ROUTER_OPKG_UUID      default auto   (auto-detect the disk that already
#                                         has /bin/busybox + /etc/opkg.conf)
#   ROUTER_LISTEN         default 0.0.0.0:9091
#   ROUTER_RELEASE        empty (build from source). Set to "latest" or a tag
#                         (e.g. v0.1.0) to install a prebuilt binary from the
#                         GitHub release instead of compiling — no Go/Node
#                         needed. Equivalent CLI: --from-release / --release-tag.
#   KSB_REPO              GitHub owner/repo for releases (default CoOre/keenetic-sing-box-ui)
#   ROUTER_BUILD          default 1 (ignored when ROUTER_RELEASE is set)
#   ROUTER_REBOOT         default 0      (1 = reboot to validate auto-start;
#                                         0 = just toggle chroot to start now)
#   ROUTER_VERIFY_TIMEOUT default 60
#
# Host prereqs: bash, sshpass, ssh, sftp, make, go (for build), curl.

# Load deploy config from .env in the repo root if present (see .env.example).
# Values already set in the environment take precedence over .env.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/../.env"
if [ -f "${ENV_FILE}" ]; then
	while IFS= read -r line || [ -n "${line}" ]; do
		case "${line}" in ''|'#'*) continue ;; esac
		key="${line%%=*}"
		[ "${key}" = "${line}" ] && continue   # skip lines without '='
		# Only set if not already provided via the environment.
		eval "current=\${${key}:-}"
		[ -n "${current}" ] || export "${line?}"
	done < "${ENV_FILE}"
fi

# ROUTER_HOST / ROUTER_USER are intentionally left blank here; they are
# resolved below (gateway auto-detect / interactive prompt) after arg parsing.
ROUTER_HOST="${ROUTER_HOST:-}"
ROUTER_USER="${ROUTER_USER:-}"
ROUTER_PORT="${ROUTER_PORT:-22}"
ROUTER_PASSWORD="${ROUTER_PASSWORD:-}"
ROUTER_OPKG_UUID="${ROUTER_OPKG_UUID:-auto}"
ROUTER_LISTEN="${ROUTER_LISTEN:-0.0.0.0:9091}"
ROUTER_BUILD="${ROUTER_BUILD:-1}"
ROUTER_REBOOT="${ROUTER_REBOOT:-0}"
ROUTER_VERIFY_TIMEOUT="${ROUTER_VERIFY_TIMEOUT:-60}"
ROUTER_RELEASE="${ROUTER_RELEASE:-}"          # empty=build; "latest" or a tag=install prebuilt
KSB_REPO="${KSB_REPO:-CoOre/keenetic-sing-box-ui}"

BIN_NAME="keenetic-sing-box-ui"

usage() {
	cat <<EOF
Usage: $0 [--host HOST] [--user USER] [--port PORT] [--opkg-uuid UUID]
          [--listen LISTEN] [--from-release] [--release-tag TAG]
          [--no-build] [--reboot] [-h|--help]

  --from-release      install the prebuilt aarch64 binary from the latest
                      GitHub release (no Go/Node toolchain required)
  --release-tag TAG   same, but pin a specific release tag (e.g. v0.1.0)

See the script header for full configuration documentation.
EOF
}

while [ "$#" -gt 0 ]; do
	case "$1" in
		--host)        ROUTER_HOST="${2:?missing}";       shift ;;
		--user)        ROUTER_USER="${2:?missing}";       shift ;;
		--port)        ROUTER_PORT="${2:?missing}";       shift ;;
		--opkg-uuid)   ROUTER_OPKG_UUID="${2:?missing}";  shift ;;
		--listen)      ROUTER_LISTEN="${2:?missing}";     shift ;;
		--from-release) ROUTER_RELEASE="latest" ;;
		--release-tag) ROUTER_RELEASE="${2:?missing}";    shift ;;
		--no-build)    ROUTER_BUILD=0 ;;
		--reboot)      ROUTER_REBOOT=1 ;;
		-h|--help)     usage; exit 0 ;;
		*) echo "unknown arg: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

require_cmd() { command -v "$1" >/dev/null 2>&1 || { echo "missing tool: $1" >&2; exit 1; }; }
require_cmd ssh; require_cmd sshpass; require_cmd sftp
if [ -n "${ROUTER_RELEASE}" ]; then
	require_cmd curl; require_cmd tar       # install-from-release needs no Go/Node
elif [ "${ROUTER_BUILD}" = "1" ]; then
	require_cmd make
fi

# detect_gateway prints the machine's default-gateway IP (the LAN router on a
# typical home network). Cross-platform: macOS (route), Linux (ip route / route).
detect_gateway() {
	case "$(uname -s)" in
		Darwin)
			route -n get default 2>/dev/null | awk '/gateway:/{print $2; exit}'
			;;
		Linux)
			if command -v ip >/dev/null 2>&1; then
				ip route show default 2>/dev/null \
					| awk '{for (i=1;i<=NF;i++) if ($i=="via") {print $(i+1); exit}}'
			else
				route -n 2>/dev/null | awk '$1=="0.0.0.0"{print $2; exit}'
			fi
			;;
		*) return 1 ;;
	esac
}

# Resolve login (prompt with default admin if not provided via env/.env/flag).
if [ -z "${ROUTER_USER}" ]; then
	if [ -t 0 ]; then
		printf "Router login [admin]: " >&2
		IFS= read -r ROUTER_USER
	fi
	ROUTER_USER="${ROUTER_USER:-admin}"
fi

# Resolve host: auto-detect the default gateway, confirm/override interactively.
if [ -z "${ROUTER_HOST}" ]; then
	detected="$(detect_gateway || true)"
	if [ -t 0 ]; then
		if [ -n "${detected}" ]; then
			printf "Router host [%s]: " "${detected}" >&2
		else
			printf "Router host: " >&2
		fi
		IFS= read -r ROUTER_HOST
		ROUTER_HOST="${ROUTER_HOST:-${detected}}"
	else
		ROUTER_HOST="${detected}"
	fi
	[ -n "${ROUTER_HOST}" ] || {
		echo "could not determine router host (set ROUTER_HOST or pass --host)" >&2
		exit 1
	}
fi

if [ -z "${ROUTER_PASSWORD}" ]; then
	if [ -t 0 ]; then
		printf "Password for %s@%s: " "${ROUTER_USER}" "${ROUTER_HOST}" >&2
		stty -echo; IFS= read -r ROUTER_PASSWORD; stty echo
		printf "\n" >&2
	else
		echo "ROUTER_PASSWORD required (no TTY for prompt)" >&2; exit 1
	fi
fi
export SSHPASS="${ROUTER_PASSWORD}"

common_opts=(
	-o StrictHostKeyChecking=accept-new
	-o UserKnownHostsFile="${HOME}/.ssh/known_hosts"
	-o LogLevel=QUIET
)
ssh_target="${ROUTER_USER}@${ROUTER_HOST}"
cli() { sshpass -e ssh -p "${ROUTER_PORT}" "${common_opts[@]}" "${ssh_target}" "$@"; }
sftp_quiet() {
	# Runs SFTP commands from stdin; suppresses normal banner/output, returns
	# non-zero if any non-prefixed command fails.
	local log
	log="$(mktemp -t kn-sftp.XXXXXX)"
	if sshpass -e sftp -P "${ROUTER_PORT}" "${common_opts[@]}" "${ssh_target}" >"${log}" 2>&1 <<<"$1"; then
		rm -f "${log}"; return 0
	else
		cat "${log}" >&2; rm -f "${log}"; return 1
	fi
}
sftp_get_file() {
	# $1 = remote path, $2 = local path. Silent on success; returns non-zero if missing.
	sftp_quiet "get $1 $2" >/dev/null 2>&1
}

# ---------------------------------------------------------------------------
# 1. Obtain the aarch64 binary: from a GitHub release, by building, or reuse
#    a previously built one in dist/.
# ---------------------------------------------------------------------------

# download_release <tag|latest>: fetch + verify the prebuilt tarball from the
# GitHub release and extract the binary. Sets the global SRC_BIN. No toolchain.
download_release() {
	local tag="$1" tarball base expected actual
	mkdir -p dist
	if [ "${tag}" = "latest" ]; then
		echo "==> Resolve latest release of ${KSB_REPO}"
		tag="$(curl -fsSL "https://api.github.com/repos/${KSB_REPO}/releases/latest" \
			| awk -F'"' '/"tag_name":/{print $4; exit}')"
		[ -n "${tag}" ] || { echo "could not resolve latest release tag for ${KSB_REPO}" >&2; exit 1; }
	fi
	tarball="${BIN_NAME}_${tag}_aarch64.tar.gz"
	base="https://github.com/${KSB_REPO}/releases/download/${tag}"
	echo "==> Download ${tarball} (${tag})"
	curl -fsSL -o "dist/${tarball}" "${base}/${tarball}" \
		|| { echo "download failed: ${base}/${tarball}" >&2; exit 1; }
	# Verify sha256 against the release's sha256sums.txt when both are available.
	if command -v shasum >/dev/null 2>&1 \
		&& curl -fsSL -o "dist/sha256sums.txt" "${base}/sha256sums.txt" 2>/dev/null; then
		expected="$(awk -v f="${tarball}" '$2=="*"f || $2==f {print $1; exit}' dist/sha256sums.txt)"
		if [ -n "${expected}" ]; then
			actual="$(shasum -a 256 "dist/${tarball}" | awk '{print $1}')"
			[ "${expected}" = "${actual}" ] \
				|| { echo "sha256 mismatch for ${tarball}" >&2; exit 1; }
			echo "    sha256 ok"
		fi
	fi
	tar -xzf "dist/${tarball}" -C dist
	SRC_BIN="dist/opt/bin/${BIN_NAME}"
}

if [ -n "${ROUTER_RELEASE}" ]; then
	download_release "${ROUTER_RELEASE}"
elif [ "${ROUTER_BUILD}" = "1" ]; then
	echo "==> make build-arm64"
	make build-arm64
	SRC_BIN="dist/${BIN_NAME}-linux-arm64"
else
	SRC_BIN="dist/${BIN_NAME}-linux-arm64"
fi
[ -f "${SRC_BIN}" ] || { echo "binary not found at ${SRC_BIN}" >&2; exit 1; }

# ---------------------------------------------------------------------------
# 2. Probe router: arch + locate the Entware-bearing disk
# ---------------------------------------------------------------------------

echo "==> Probe router"
arch="$(cli 'show version' 2>&1 | awk -F': *' '/arch:/{print $2; exit}' | tr -d '\r\n')"
[ "${arch}" = "aarch64" ] || { echo "router arch '${arch}' but binary is aarch64" >&2; exit 1; }

media="$(cli 'show media' 2>&1 | sed $'s/\x1b\\[[0-9;]*[a-zA-Z]//g')"
partitions="$(printf '%s\n' "${media}" | awk '
	/partition:/ { in_part=1; u=""; l=""; s=""; next }
	in_part && /uuid:/  { u=$2 }
	in_part && /label:/ { l=$2 }
	in_part && /state:/ { s=$2 }
	in_part && /^[[:space:]]*$/ { if (u) print u "|" l "|" s; in_part=0 }
	END { if (in_part && u) print u "|" l "|" s }
')"

disk_has_entware() {
	local uuid="$1" tmp1 tmp2 ok=1
	tmp1="$(mktemp -t kn-bb.XXXXXX)"
	tmp2="$(mktemp -t kn-cf.XXXXXX)"
	sftp_quiet "
-get /tmp/mnt/${uuid}/bin/busybox ${tmp1}
-get /tmp/mnt/${uuid}/etc/opkg.conf ${tmp2}
" >/dev/null 2>&1 || true
	[ -s "${tmp1}" ] && [ -s "${tmp2}" ] && ok=0
	rm -f "${tmp1}" "${tmp2}"
	return ${ok}
}

if [ "${ROUTER_OPKG_UUID}" = "auto" ]; then
	while IFS='|' read -r u l s; do
		[ "$s" != "MOUNTED" ] && continue
		if disk_has_entware "$u"; then ROUTER_OPKG_UUID="$u"; break; fi
	done <<<"${partitions}"
	if [ "${ROUTER_OPKG_UUID}" = "auto" ]; then
		cat >&2 <<EOF
ERROR: No disk with a working Entware install found on the router.
       We need a disk that already has /bin/busybox and /etc/opkg.conf. On a
       fresh router, install Entware via Keenetic's web admin first:
         Settings → Component manager → install OPKG
         then Applications → OPKG → select disk → Install.
       After that runs (and the router reboots), rerun this script.
EOF
		exit 1
	fi
fi
OPKG_MOUNT="/tmp/mnt/${ROUTER_OPKG_UUID}"
echo "    opkg disk: ${ROUTER_OPKG_UUID}"

# ---------------------------------------------------------------------------
# 3. (Removed) One-time cleanup of the user's previous sing-box install.
#
# This used to delete /opt/bin/sing-box, the old init scripts, the cron entry
# and logs over SFTP. That was a ONE-TIME migration and has already run. It
# MUST NOT live in a script that runs on every deploy, because /opt/bin/sing-box
# and /opt/etc/init.d/S99sing-box are now the paths WE install and manage —
# deleting them on each deploy wiped the freshly installed sing-box. The
# sing-box binary, config, and init script are now managed by the UI and must
# persist across deploys.
#
# If you ever need to purge an old sing-box again, do it as a deliberate manual
# step, not here.

# ---------------------------------------------------------------------------
# 4. Upload our binary atomically (write .new, rename)
# ---------------------------------------------------------------------------

echo "==> Upload binary to /opt/bin/${BIN_NAME}"
sftp_quiet "
put ${SRC_BIN} ${OPKG_MOUNT}/bin/${BIN_NAME}.new
chmod 755 ${OPKG_MOUNT}/bin/${BIN_NAME}.new
-rm ${OPKG_MOUNT}/bin/${BIN_NAME}
rename ${OPKG_MOUNT}/bin/${BIN_NAME}.new ${OPKG_MOUNT}/bin/${BIN_NAME}
" >/dev/null

# ---------------------------------------------------------------------------
# 5. Write /opt/etc/initrc that auto-starts the service
# ---------------------------------------------------------------------------

echo "==> Write /opt/etc/initrc"
initrc_tmp="$(mktemp -t kn-initrc.XXXXXX)"
cat > "${initrc_tmp}" <<INITRC
#!/opt/bin/sh
# Generated by install-router.sh. Runs as root every time KeeneticOS mounts
# /opt (boot or 'opkg chroot' toggle). Starts crond if needed, applies any
# pending binary update from the staging area, then (re)starts keenetic-sing-box-ui.
export PATH=/opt/sbin:/opt/bin:\$PATH
exec >>/opt/var/log/initrc.log 2>&1

BB=/opt/bin/busybox
BIN=/opt/bin/${BIN_NAME}
LOG=/opt/var/log/${BIN_NAME}.log
PIDFILE=/opt/var/run/${BIN_NAME}.pid
LISTEN='${ROUTER_LISTEN}'

\$BB mkdir -p /opt/var/log /opt/var/run

echo "=== initrc \$(\$BB date) chroot=\$1 ==="

# NOTE: do NOT rm -rf /opt/etc/sing-box or /opt/var/lib/sing-box here. initrc
# runs on every boot and every 'opkg chroot' toggle, so any cleanup placed
# here is destructive on every deploy. The sing-box config is managed by the
# UI (/opt/etc/sing-box/config.json) and must persist across deploys/reboots.

# crond
if ! \$BB pgrep -f /opt/sbin/crond >/dev/null 2>&1; then
	if [ -x /opt/sbin/crond ]; then
		/opt/sbin/crond -c /opt/etc/crontabs -L /opt/var/log/crond.log -b && echo "crond started"
	fi
fi

# Stop any previous instance (pid file first, then a broad fallback).
if [ -f "\$PIDFILE" ]; then
	oldpid=\$(\$BB cat "\$PIDFILE" 2>/dev/null)
	if [ -n "\$oldpid" ] && [ -d "/proc/\$oldpid" ]; then
		\$BB kill "\$oldpid" 2>/dev/null
		\$BB sleep 1
		[ -d "/proc/\$oldpid" ] && \$BB kill -9 "\$oldpid" 2>/dev/null
	fi
	\$BB rm -f "\$PIDFILE"
fi
for pid in \$(\$BB pgrep -f /opt/bin/${BIN_NAME} 2>/dev/null); do
	\$BB kill "\$pid" 2>/dev/null
done

# Start.
if [ -x "\$BIN" ]; then
	"\$BIN" --listen "\$LISTEN" >>\$LOG 2>&1 &
	echo \$! >\$PIDFILE
	echo "${BIN_NAME} pid \$!"
else
	echo "ERROR: \$BIN missing or not executable"
fi
INITRC

sftp_quiet "
put ${initrc_tmp} ${OPKG_MOUNT}/etc/initrc.new
chmod 755 ${OPKG_MOUNT}/etc/initrc.new
-rm ${OPKG_MOUNT}/etc/initrc
rename ${OPKG_MOUNT}/etc/initrc.new ${OPKG_MOUNT}/etc/initrc
" >/dev/null
rm -f "${initrc_tmp}"

# ---------------------------------------------------------------------------
# 6. Make sure Keenetic is pointed at this disk with default initrc
# ---------------------------------------------------------------------------

echo "==> Configure Keenetic opkg state"
cli "opkg disk ${ROUTER_OPKG_UUID}:/" >/dev/null
cli "no opkg initrc" >/dev/null
cli "system configuration save" >/dev/null

# ---------------------------------------------------------------------------
# 7. Start the service: chroot toggle (or reboot to validate boot-time start)
# ---------------------------------------------------------------------------

if [ "${ROUTER_REBOOT}" = "1" ]; then
	echo "==> system reboot (validates that initrc runs at boot)"
	cli "system reboot" || true
	until ! (echo > "/dev/tcp/${ROUTER_HOST}/${ROUTER_PORT}") 2>/dev/null; do sleep 2; done
	until (echo > "/dev/tcp/${ROUTER_HOST}/${ROUTER_PORT}") 2>/dev/null; do sleep 3; done
else
	echo "==> Toggle opkg chroot to start the service"
	cli "no opkg chroot" >/dev/null
	sleep 2
	cli "opkg chroot" >/dev/null
fi

# ---------------------------------------------------------------------------
# 8. Verify
# ---------------------------------------------------------------------------

listen_port="${ROUTER_LISTEN##*:}"
echo "==> Wait for http://${ROUTER_HOST}:${listen_port}/ (up to ${ROUTER_VERIFY_TIMEOUT}s)"
deadline=$(( $(date +%s) + ROUTER_VERIFY_TIMEOUT ))
ok=0
while [ "$(date +%s)" -lt "${deadline}" ]; do
	if (echo > "/dev/tcp/${ROUTER_HOST}/${listen_port}") 2>/dev/null; then
		ok=1; break
	fi
	sleep 1
done

if [ "${ok}" = "1" ]; then
	code=$(curl -sS -m 5 -o /dev/null -w '%{http_code}' "http://${ROUTER_HOST}:${listen_port}/" || true)
	echo "==> Done. UI on http://${ROUTER_HOST}:${listen_port}/ (HTTP ${code})"
else
	cat >&2 <<EOF
WARN: port ${listen_port} did not come up within ${ROUTER_VERIFY_TIMEOUT}s.
      Diagnostics:
        sftp ${ROUTER_USER}@${ROUTER_HOST}:/tmp/mnt/${ROUTER_OPKG_UUID}/var/log/initrc.log
        sftp ${ROUTER_USER}@${ROUTER_HOST}:/tmp/mnt/${ROUTER_OPKG_UUID}/var/log/${BIN_NAME}.log
EOF
	exit 1
fi
