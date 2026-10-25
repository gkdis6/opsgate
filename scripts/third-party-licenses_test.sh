#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' 0
trap 'exit 1' HUP INT TERM
mkdir -p "$scratch/scripts" "$scratch/cache/one/legal" "$scratch/cache/two"
cp scripts/third-party-licenses.sh "$scratch/scripts/"
cd "$scratch"
printf 'one license\n' > cache/one/LICENSE
printf 'one notice\n' > cache/one/legal/Notice.txt
printf 'package fixture\n' > cache/one/legal/license_test.go
printf 'two license\n' > cache/two/COPYING
printf 'example.com/one\tv1.2.3\t%s/cache/one\nexample.com/two\tv2.3.4\t%s/cache/two\n' "$PWD" "$PWD" > modules
cat > fake-go <<'GO'
#!/bin/sh
set -eu
test "$GOPROXY" = off
test "$GOSUMDB" = off
test "$1" = list
test "$2" = -mod=readonly
if test -f fail-list; then exit 1; fi
cat modules
GO
chmod +x fake-go
export GO="$PWD/fake-go"
sh scripts/third-party-licenses.sh
cmp cache/one/LICENSE dist/third_party/example.com/one/LICENSE
cmp cache/one/legal/Notice.txt dist/third_party/example.com/one/legal/Notice.txt
cmp cache/two/COPYING dist/third_party/example.com/two/COPYING
test "$(wc -l < dist/third_party/index.tsv | tr -d ' ')" = 4
test ! -e dist/third_party/example.com/one/legal/license_test.go
awk -F '\t' '$1 == "example.com/one" && $2 == "v1.2.3" && $3 == "example.com/one/legal/Notice.txt" { found = 1 } END { exit !found }' dist/third_party/index.tsv
cp dist/third_party/index.tsv expected-index

# Cache/list failures must preserve the last complete output.
touch fail-list
if sh scripts/third-party-licenses.sh; then exit 1; fi
cmp expected-index dist/third_party/index.tsv
rm fail-list
mv cache/two/COPYING cache/two/absent
if sh scripts/third-party-licenses.sh; then exit 1; fi
cmp expected-index dist/third_party/index.tsv
mv cache/two/absent cache/two/COPYING

# A later run removes notices for dependencies no longer linked.
printf 'example.com/one\tv1.2.3\t%s/cache/one\n' "$PWD" > modules
sh scripts/third-party-licenses.sh
test ! -e dist/third_party/example.com/two
echo 'License collector fixtures passed'
