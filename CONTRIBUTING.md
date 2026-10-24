# Contributing

Use Go 1.26 or newer, Git and make. Linux is needed for real host inspection; unit tests must run without root, systemd, a cluster or network access. Clone the repository and work on a focused branch.

For the managed development environment, set these before Go commands:

```sh
export PATH=$HOME/.local/go/bin:$PATH GOTOOLCHAIN=local GOFLAGS=-mod=mod GOCACHE=$PWD/.gocache
make build
make test
make vet
make lint
make vuln
```

Install golangci-lint v2 and govulncheck separately if needed. `make lint` reports a skip when the linter is absent; CI requires linting. Vulnerability scanning may need network access. `make build VERSION=dev` writes `bin/opsgate`; `make clean` removes build output and the local Go cache. Do not commit generated binaries or caches.

Keep Go files small, use doc comments on exported identifiers, and prefer the standard library. Format with gofmt. Run `go test -race ./...` and `go vet ./...` before submitting. Use table-driven tests for meaningful cases, especially input validation and policy refusals, with fake dependencies rather than live services.

Every new MCP tool must register through `tool.Add`, use `tool.Caller` for identity, return plain text and return `tool.Denied` for policy refusals. The wrapper provides scope checks, deadlines, redaction, truncation and auditing. Never bypass it by returning hand-built MCP results from a provider.

Tools must be read-only unless a reviewed write gate exists. Future writes require explicit allowlists, `ops:write` and human confirmation through MCP elicitation that the model cannot bypass. These gates are planned, not implemented; setting a write annotation or scope alone is insufficient.

Validate every client input strictly with appropriate regexes, enums and ranges. Clamp requested limits. Test malformed inputs, boundary values, allowlist misses and namespace deny precedence. Do not read Secrets or introduce shell/exec tools. When an internal external command is necessary, use `exec.CommandContext` with a fixed executable and argv, never a shell; honor cancellation. Treat logs and events as attacker-controlled data, never instructions. Redaction is a safety net, not permission to collect secrets.

Document new settings, defaults and client-visible behavior in both READMEs and the example config. Keep unknown-field rejection intact. Pull requests should explain the concrete behavior change and validation performed. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## Documentation regression checks

v0.1 providers, audit persistence and redaction are implemented and pre-release; third-party security review is still pending. Keep future work explicitly marked as not implemented. For documentation-only changes, run the following offline check from the repository root as well as the Go checks above. It checks both README tool tables against provider inputs, kind aliases against the resolver, audit examples against `audit.Event`, and the security/status statements that previously became stale. Review translated prose for semantic equivalence too.

```sh
python3 - <<'PY'
import json
import re
from pathlib import Path

readmes = [Path(name).read_text() for name in ('README.md', 'README.ko.md')]
docs = {name: Path(name).read_text() for name in (
    'README.md', 'README.ko.md', 'SECURITY.md', 'docs/roadmap.md', 'docs/clients.md')}
for name, text in docs.items():
    assert not re.search(r'\bstub(?:s|bed)?\b|TODO|스텁|아직 없습니다', text), name
    assert not re.search(r'manifest[^.\n]*(?:absent|missing)', text, re.I), name
assert 'planned, not implemented' in docs['docs/roadmap.md']
assert Path('deploy/kubernetes/rbac-readonly.yaml').is_file()
for text in readmes:
    assert 'pre-release' in text or '사전 릴리스' in text
    assert 'third parties' in text or '제3자' in text

# Each registered tool needs a row containing every input field.
sources = {str(p): p.read_text() for p in Path('internal/provider').rglob('*.go')
           if not p.name.endswith('_test.go')}
inputs = {
    'host_overview': None, 'host_services': None,
    'host_service_status': ('host/host.go', 'unitInput'),
    'host_logs': ('host/host.go', 'logsInput'),
    'host_top_processes': ('host/host.go', 'topInput'),
    'k8s_overview': None, 'k8s_list': ('kube/objects.go', 'listInput'),
    'k8s_get': ('kube/objects.go', 'getInput'),
    'k8s_logs': ('kube/logs.go', 'logsInput'),
    'k8s_events': ('kube/events.go', 'eventsInput'),
}
registered = set(re.findall(r'Name: "((?:host|k8s)_\w+)"', '\n'.join(sources.values())))
assert registered == set(inputs)
for text in readmes:
    for tool, source in inputs.items():
        row = next(line for line in text.splitlines() if line.startswith(f'| `{tool}` |'))
        assert len(row.split('|')) == 5, tool
        if source:
            filename, typename = source
            body = re.search(r'type ' + typename + r' struct \{(.*?)\n\}',
                             sources['internal/provider/' + filename], re.S).group(1)
            for field in re.findall(r'json:"([^",]+)', body):
                assert re.search(r'`' + field + r'(?:`|=)', row), (tool, field)
    for case in re.findall(r'case ([^:\n]+):', sources['internal/provider/kube/resources.go']):
        for alias in re.findall(r'"([a-z]+)"', case):
            assert re.search(r'\b' + alias + r'\b', text), alias
    for token in ('lines=100', 'tail=200', 'limit=15', 'limit=100', 'limit=50',
                  'sort="cpu"', 'since="1h"', 'warnings_only=true', 'previous=false',
                  '4096', '256', '128', '168h', '1 MiB', '500', '65536', '30s'):
        assert token in text, token

# A real JSONL example must use Event fields and agree across translations.
event = Path('internal/audit/audit.go').read_text().split('type Event struct {', 1)[1].split('\n}', 1)[0]
fields = re.findall(r'json:"([^",]+)(?:,omitempty)?"', event)
required = re.findall(r'json:"([^",]+)"', event)
examples = []
for text in readmes:
    line = next(line for line in text.splitlines() if line.startswith('{"time":'))
    obj = json.loads(line)
    assert set(required) <= set(obj) <= set(fields)
    assert obj['outcome'] in ('ok', 'error', 'denied')
    assert isinstance(obj['duration_ms'], int) and isinstance(obj['output_bytes'], int)
    assert all('`' + field + '`' in text for field in fields)
    examples.append(obj)
assert examples[0] == examples[1]

# Security details must remain visible in both languages, not just source code.
for text in readmes:
    for token in ('exec', 'auth-provider', 'PersistentVolumes', 'Nodes', 'Namespace',
                  'binaryData', 'show_configmap_data=true', 'valueFrom', 'init',
                  'ephemeral', 'database_url', 'managedFields', 'PEM', 'JWT',
                  'Proxy-Authorization', 'Telegram', 'set-cookie', 'db_password'):
        assert token in text, token
    assert 'best effort' in text or '최선형' in text
print('Documentation regression checks passed')
PY
```
