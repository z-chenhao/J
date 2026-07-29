# J-Space macOS deployment

The current host already exposes `127.0.0.1:8088` through a userspace
Tailscale Funnel. J-Space reuses that TLS endpoint and adds no new public
listener:

```text
Tailscale Funnel :443
  -> 127.0.0.1:8088  jspace-gateway
       /v1/models                  -> 127.0.0.1:8000
       /v1/chat/completions        -> 127.0.0.1:8000
       /jspace/* GET/HEAD only     -> 127.0.0.1:8090
```

`jspace-server` and `jspace-gateway` bind only to loopback. The gateway
preserves the existing model paths and model-key injection. J-Space uses a
different viewer token at `~/.j/jspace/access-token`.

## Install or update

From `J-agent/research/jspace`:

```bash
make check
mkdir -p ~/.local/share/jspace-workbench ~/.local/state/usej
install -m 0755 bin/jspace-server ~/.local/share/jspace-workbench/jspace-server
install -m 0755 bin/jspace-gateway ~/.local/share/j-public-gateway/j-public-gateway
sed "s|__HOME__|$HOME|g" \
  deploy/dev.usej.jspace-server.plist.template \
  > ~/Library/LaunchAgents/dev.usej.jspace-server.plist
plutil -lint ~/Library/LaunchAgents/dev.usej.jspace-server.plist
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/dev.usej.jspace-server.plist
launchctl kickstart -k "gui/$(id -u)/dev.usej.model-gateway"
```

For an update, use `launchctl bootout` on the J-Space service before
`bootstrap`. Keep a copy of the previous gateway binary until both the model
routes and J-Space routes pass verification.

Do not place the viewer token in a plist, command argument, URL, repository,
shell history, or log. Read it directly from its mode-0600 file when pasting it
into the page.

## Verify

Local checks:

```bash
curl --noproxy '*' -I http://127.0.0.1:8090/jspace/
curl --noproxy '*' -i http://127.0.0.1:8088/jspace/api/status
```

Expected: the page returns `200`; the API without a viewer token returns
`401`. Perform the authenticated check without printing the token:

```bash
curl --noproxy '*' -fsS \
  -H "Authorization: Bearer $(< ~/.j/jspace/access-token)" \
  http://127.0.0.1:8088/jspace/api/status
```

Public checks:

```bash
curl -I https://usej-model.tailb0426d.ts.net/jspace/
curl -i https://usej-model.tailb0426d.ts.net/jspace/api/status
curl -i -X POST https://usej-model.tailb0426d.ts.net/jspace/api/runs
```

Expected status codes are `200`, `401`, and `404`. Also re-check
`GET /v1/models` and one short authenticated model request so a viewer
deployment never silently regresses the existing public model service.

## Rollback

Restore the saved gateway binary, restart `dev.usej.model-gateway`, and boot
out `dev.usej.jspace-server`. Artifacts and the lens remain in `~/.j/jspace`
and can be inspected locally; rollback does not delete research state.
