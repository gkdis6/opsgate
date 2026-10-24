# Connecting clients

opsgate currently supports **stdio only**. The client launches the binary and exchanges MCP messages through stdin/stdout. Use absolute executable and configuration paths. Keep stdout free of banners or debugging output; diagnostics and the default audit destination use stderr. These examples assume the v0.1 providers have been completed; provider registration is currently stubbed in this checkout.

The process runs with the launching OS user's permissions and configured `stdio.scopes`. `stdio.user` is an audit label. Protect the config and use an unprivileged account. Enable only needed providers and allowlisted units/namespaces.

## Claude Code

```sh
claude mcp add opsgate -- /path/to/opsgate -config /path/opsgate.yaml
```

## Claude Desktop

Merge into `claude_desktop_config.json`, preserving other servers:

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "/path/to/opsgate",
      "args": ["-config", "/path/opsgate.yaml"]
    }
  }
}
```

Use Claude Desktop's developer settings to locate its configuration and restart the app after editing. On macOS and Windows, use SSH to inspect a Linux host rather than enabling the local host provider.

## Cursor

Merge into the project's `.cursor/mcp.json` or your user-level Cursor `mcp.json`:

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "/path/to/opsgate",
      "args": ["-config", "/path/opsgate.yaml"]
    }
  }
}
```

Only trust project MCP configurations you have reviewed: they can launch programs as your user.

## Over SSH

Install opsgate and its config on the Linux host. The stdio command is:

```sh
ssh host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

For Claude Code:

```sh
claude mcp add opsgate -- ssh -T host /usr/local/bin/opsgate -config /etc/opsgate.yaml
```

Desktop and Cursor can use this server entry:

```json
{
  "mcpServers": {
    "opsgate": {
      "command": "ssh",
      "args": ["-T", "host", "/usr/local/bin/opsgate", "-config", "/etc/opsgate.yaml"]
    }
  }
}
```

Set up host-key verification and noninteractive SSH authentication first. Do not disable host-key checking. Use a dedicated unprivileged remote account and no pseudo-terminal. Remote startup files must not print to stdout. Keep remote command paths fixed and trusted; never interpolate tool input into SSH commands. SSH supplies transport security; this is still a stdio MCP server, not an HTTP endpoint.

## Remote web clients

claude.ai and ChatGPT need the planned Streamable HTTP and OAuth mode; they cannot connect directly to this stdio process. See the [roadmap](roadmap.md). No HTTP URL or OAuth setup is available yet.
