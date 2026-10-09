#!/usr/bin/env bash
set -euo pipefail

# changelog.sh — changelog из Conventional Commits между тегами v*.
#
#   scripts/changelog.sh notes <tag>   заметки к одному релизу (prev-tag..tag)
#                                      — тело GitHub Release в CI
#   scripts/changelog.sh full [next]   весь CHANGELOG.md; коммиты после
#                                      последнего тега идут под заголовком
#                                      [next] (или «Не выпущено»)
#
# Подпись коммита — первая строка, тело (списки «- …») переносится как есть.
# Коммиты chore(release) пропускаются.

repo_url() {
    if [ -n "${GITHUB_REPOSITORY:-}" ]; then
        echo "${GITHUB_SERVER_URL:-https://github.com}/$GITHUB_REPOSITORY"
        return
    fi
    # Ссылки только на веб-хостинг: локальный/ssh-путь без хоста не годится.
    git remote get-url origin 2>/dev/null |
        sed -E 's#^git@([^:]+):#https://\1/#; s#\.git$##' | grep '^https://' || true
}

REPO=$(repo_url)

# prev_tag <rev>: ближайший тег v* строго до rev (пусто, если это первый).
prev_tag() {
    git describe --tags --abbrev=0 --match 'v*' "$1^" 2>/dev/null || true
}

# section <range>: разделы по типам коммитов в диапазоне.
section() {
    git log --no-merges --format='%x1e%h%x1f%B' "$1" |
        awk -v repo="$REPO" '
        BEGIN {
            RS = "\036"; FS = "\037"
            n = split("breaking feat fix perf refactor other", order, " ")
            title["breaking"] = "⚠️ Несовместимые изменения"
            title["feat"]     = "Новое"
            title["fix"]      = "Исправления"
            title["perf"]     = "Производительность"
            title["refactor"] = "Рефакторинг"
            title["other"]    = "Прочее"
        }
        NF < 2 { next }
        {
            hash = $1
            nl = split($2, lines, "\n")
            subj = lines[1]
            if (subj ~ /^chore\(release\)/) next

            type = "other"; scope = ""; desc = subj; bang = 0
            if (match(subj, /^[a-z]+(\([^)]*\))?!?: /)) {
                head = substr(subj, 1, RLENGTH - 2)
                desc = substr(subj, RLENGTH + 1)
                if (head ~ /!$/) { bang = 1; sub(/!$/, "", head) }
                if (match(head, /\(.*\)/)) {
                    scope = substr(head, RSTART + 1, RLENGTH - 2)
                    head = substr(head, 1, RSTART - 1)
                }
                if (head in title) type = head
            }

            body = ""
            for (i = 2; i <= nl; i++) {
                l = lines[i]
                if (l ~ /^[ \t]*$/) continue
                if (l ~ /^(Co-[Aa]uthored-[Bb]y|Signed-off-by|BREAKING[ -]CHANGE):/) {
                    if (l ~ /^BREAKING/) bang = 1
                    continue
                }
                body = body "\n  " l
            }
            if (bang) type = "breaking"

            link = repo != "" ? " ([" hash "](" repo "/commit/" hash "))" : " (" hash ")"
            entry = "- " (scope != "" ? "**" scope ":** " : "") desc link body
            items[type] = items[type] entry "\n"
        }
        END {
            for (i = 1; i <= n; i++) {
                t = order[i]
                if (items[t] == "") continue
                printf "### %s\n\n%s\n", title[t], items[t]
            }
        }'
}

# compare <from> <to>: ссылка на полный дифф.
compare() {
    [ -n "$REPO" ] && [ -n "$1" ] || return 0
    echo "**Полный список изменений:** [$1...$2]($REPO/compare/$1...$2)"
    echo
}

cmd_notes() {
    local tag=${1:?usage: changelog.sh notes <tag>} prev
    prev=$(prev_tag "$tag")
    section "${prev:+$prev..}$tag"
    compare "$prev" "$tag"
}

cmd_full() {
    local next=${1:-} last tags tag prev date
    echo "# Changelog"
    echo
    echo "Генерируется из коммитов скриптом \`scripts/changelog.sh\` (\`make changelog\`)."
    echo

    last=$(git describe --tags --abbrev=0 --match 'v*' HEAD 2>/dev/null || true)
    if [ -n "$(git log --no-merges --format=%h "${last:+$last..}HEAD" -- | head -1)" ]; then
        if [ -n "$next" ]; then
            echo "## [$next] — $(date +%Y-%m-%d)"
        else
            echo "## Не выпущено"
        fi
        echo
        section "${last:+$last..}HEAD"
        [ -n "$next" ] && compare "$last" "$next"
    fi

    tags=$(git tag -l 'v*' --sort=-v:refname)
    for tag in $tags; do
        prev=$(prev_tag "$tag")
        date=$(git log -1 --format=%cs "$tag")
        echo "## [$tag] — $date"
        echo
        section "${prev:+$prev..}$tag"
        compare "$prev" "$tag"
    done
}

case "${1:-}" in
    notes) shift; cmd_notes "$@" ;;
    full)  shift; cmd_full "$@" ;;
    *)     echo "usage: $0 notes <tag> | full [next-tag]" >&2; exit 2 ;;
esac
