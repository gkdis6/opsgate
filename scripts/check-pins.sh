#!/bin/sh
# Check literal tool versions without installing tools or fetching metadata.
set -eu

cd "$(dirname "$0")/.."
export LC_ALL=C

awk '
function fail(message) {
    printf "%s:%d: %s\n", FILENAME, first, message > "/dev/stderr"
    failed = 1
}
function check(line,    code, ref, n, words, i, item, url, rest, command, j, parts, digest, path) {
    code = line
    sub(/[[:space:]]*#.*/, "", code)
    if (code ~ /^[[:space:]]*(-[[:space:]]*)?uses[[:space:]]*:/) {
        ref = code
        sub(/^[[:space:]]*(-[[:space:]]*)?uses[[:space:]]*:[[:space:]]*/, "", ref)
        gsub(/["\047]/, "", ref)
        sub(/[[:space:]]+$/, "", ref)
        if (ref !~ /@/) ref = ""
        sub(/^.*@/, "", ref)
        if ((length(ref) != 40 || ref ~ /[^[:xdigit:]]/) && line !~ /# TODO pin([[:space:]]|$)/)
            fail("uses: must have a 40-hex SHA or a # TODO pin comment")
    }
    # Accept go and the Makefile GO variable; inspect every package argument.
    command = code
    while (match(command, /(^|[^[:alnum:]_])(go|\$\(GO\)|\$\{GO\})[[:space:]]+install[[:space:]]+/)) {
        command = substr(command, RSTART + RLENGTH)
        rest = command
        sub(/[;&|].*$/, "", rest)
        n = split(rest, words, /[[:space:]]+/)
        for (i = 1; i <= n; i++) {
            item = words[i]
            if (item ~ /^(&&|\|\||;|\|)/) break
            if (item ~ /^([0-9]*[<>])/) break
            gsub(/["\047;]/, "", item)
            if (item == "" || item ~ /^-/) continue
            if (item !~ /@v[0-9]+\.[0-9]+\.[0-9]+([+.-][[:alnum:].-]+)?$/)
                fail("go install requires an explicit module version: " item)
        }
    }
    if (code ~ /(^|[^[:alnum:]_])(curl|wget)([[:space:]]|$)/) {
        rest = code
        n = 0
        while (match(rest, /https?:\/\/[^[:space:]"\047<>|]+/)) {
            url = substr(rest, RSTART, RLENGTH)
            rest = substr(rest, RSTART + RLENGTH)
            n++
            path = url
            sub(/^https?:\/\/[^/]+\/?/, "", path)
            digest = 0
            j = split(path, parts, /[^[:alnum:]]/)
            for (i = 1; i <= j; i++)
                if ((length(parts[i]) == 40 || length(parts[i]) == 64) && parts[i] !~ /[^[:xdigit:]]/) digest = 1
            if (path ~ /(^|[/@_-])(latest|main|master|HEAD)([/?.#_-]|$)/ ||
                (path !~ /(^|[^[:alnum:]])(v|go)?[0-9]+\.[0-9]+(\.[0-9]+)?([^[:alnum:]]|$)/ &&
                 !digest))
                fail("tool download URL requires a literal version or digest: " url)
        }
        if (n == 0) fail("tool download URL must carry a literal version or digest")
    }
}
{
    if (FNR == 1 && pending != "") {
        check(pending)
        pending = ""
    }
    if (pending == "" && $0 ~ /^[[:space:]]*#/) next
    if (pending == "") first = FNR
    line = pending $0
    if (line ~ /\\[[:space:]]*$/) {
        sub(/\\[[:space:]]*$/, " ", line)
        pending = line
        next
    }
    pending = ""
    check(line)
}
END {
    if (pending != "") check(pending)
    exit failed
}
' .github/workflows/*.yml Makefile
