#!/bin/sh
# Collect notices for linked dependencies, using only the local module cache.
set -eu

cd "$(dirname "$0")/.."
export LC_ALL=C GOPROXY=off GOSUMDB=off
go_command=${GO:-go}
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' 0
trap 'exit 1' HUP INT TERM

# Keep go list outside a pipeline so a missing cache entry fails the release.
"$go_command" list -mod=readonly -deps -f '{{if and .Module (not .Module.Main)}}{{printf "%s\t%s\t%s" .Module.Path .Module.Version .Module.Dir}}{{end}}' ./cmd/opsgate > "$scratch/deps"
sort -u "$scratch/deps" > "$scratch/modules"
mkdir "$scratch/third_party"
printf 'module\tversion\tnotice\n' > "$scratch/third_party/index.tsv"
tab=$(printf '\t')
while IFS="$tab" read -r module version directory; do
    test -n "$module" || continue
    if test -z "$directory" || ! test -d "$directory"; then
        echo "missing cached module directory: $module" >&2
        exit 1
    fi
    case "$module" in
        /*|../*|*/../*|*/..|*\\*) echo "invalid module path: $module" >&2; exit 1 ;;
    esac
    # License-related Go source is not a notice; it also pollutes go test ./....
    find "$directory" -type f ! -name '*.go' \( \
        -name '[Ll][Ii][Cc][Ee][Nn][SsCc][Ee]*' -o \
        -name '[Cc][Oo][Pp][Yy][Ii][Nn][Gg]*' -o \
        -name '[Nn][Oo][Tt][Ii][Cc][Ee]*' \
    \) -print > "$scratch/notices"
    sort "$scratch/notices" > "$scratch/sorted-notices"
    if ! test -s "$scratch/sorted-notices"; then
        echo "no license notices found for $module" >&2
        exit 1
    fi
    while IFS= read -r notice; do
        relative=${notice#"$directory"/}
        destination="$scratch/third_party/$module/$relative"
        mkdir -p "$(dirname "$destination")"
        cp "$notice" "$destination"
        printf '%s\t%s\t%s\n' "$module" "$version" "$module/$relative" >> "$scratch/third_party/index.tsv"
    done < "$scratch/sorted-notices"
done < "$scratch/modules"

mkdir -p dist
# Publish only after every dependency has notices; remove stale dependencies.
rm -rf dist/third_party
cp -R "$scratch/third_party" dist/third_party
printf 'Third-party notices: dist/third_party/index.tsv\n'
