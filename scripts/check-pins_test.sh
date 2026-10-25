#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' 0
trap 'exit 1' HUP INT TERM
mkdir -p "$scratch/scripts" "$scratch/.github/workflows"
cp scripts/check-pins.sh "$scratch/scripts/"
cd "$scratch"
: > Makefile

# Each row is expected status, case name, and a workflow/Makefile fragment.
while IFS='|' read -r want name fragment; do
    printf '%b\n' "$fragment" > .github/workflows/check.yml
    status=0
    sh scripts/check-pins.sh > result 2>&1 || status=1
    if test "$status" != "$want"; then
        echo "pin check failed: $name (want $want, got $status)" >&2
        cat result >&2
        exit 1
    fi
    # Exercise both scanned inputs.
    cp .github/workflows/check.yml Makefile
    : > .github/workflows/check.yml
    status=0
    sh scripts/check-pins.sh > result 2>&1 || status=1
    if test "$status" != "$want"; then
        echo "Makefile pin check failed: $name" >&2
        cat result >&2
        exit 1
    fi
    : > Makefile
done <<'CASES'
0|SHA action|- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683
0|TODO action|- uses: actions/setup-go@v6 # TODO pin to SHA
0|quoted action|- uses: "actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683"
1|bare action|- uses: actions/checkout@v4
1|short SHA|- uses: actions/checkout@11bd719
1|SHA suffix|- uses: actions/checkout@11bd71901bbe5b1630ceea73d27597364c9af683-extra
1|missing at|- uses: 11bd71901bbe5b1630ceea73d27597364c9af683
0|pinned install|go install example.com/tool@v1.2.3
0|redirected install|go install example.com/tool@v1.2.3 >/dev/null
0|pseudo version|go install example.com/tool@v0.0.0-20261025120000-0123456789ab
0|Make GO variable|$(GO) install example.com/tool@v1.2.3
0|sequential pinned commands|go install example.com/one@v1.2.3; go install example.com/two@v1.2.3
1|latest install|go install example.com/tool@latest
1|bare install|go install example.com/tool
1|multiple packages|go install example.com/one@v1.2.3 example.com/two@latest
1|continued install|go install \\\n  example.com/tool@latest
1|second command|go install example.com/one@v1.2.3 && go install example.com/two@latest
0|version download|curl -fL https://example.com/tool/v1.2.3/tool.tar.gz
0|Go download|curl -fL https://go.dev/dl/go1.26.0.linux-amd64.tar.gz
0|commit download|wget https://example.com/11bd71901bbe5b1630ceea73d27597364c9af683/install.sh
1|bare download|curl https://example.com/install.sh
1|numeric host is not a version|curl https://127.0.0.1/install.sh
1|latest download|wget https://example.com/releases/latest/download/tool.tar.gz
1|variable download|curl "$TOOL_URL"
1|mutable branch|curl https://example.com/main/v1.2.3/install.sh
0|comment|# go install example.com/tool@latest
1|comment continuation cannot hide action|# comment \\\n- uses: actions/checkout@v4
CASES
echo 'Pin checker fixtures passed'
