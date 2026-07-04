# Agent Deck Hub

Agent Deck Hub connects multiple trusted `agent-deck` instances through one encrypted relay. Each joined `agent-deck` owns its local sessions and keeps an outbound `wss://` connection to the hub. Any connected TUI can see, attach to, prompt, stop, restart, rename, and create sessions on other connected nodes.

The hub is not a browser UI and it is not a multi-user auth system. It is a coordination service for nodes you trust and intentionally join.

## Start A Hub

The hub always uses TLS. If you do not provide a certificate, `hub serve` creates and reuses a self-signed certificate in the hub data directory.

```bash
agent-deck hub serve \
  --listen :8421 \
  --data ~/.local/share/agent-deck-hub
```

The data directory stores `hub.db`, node credentials, invite state, latest session snapshots, the hub URL used by invites, and the default self-signed cert/key. By default the hub URL is derived from `--listen`. If that is not the URL clients should use, set it once when starting the hub:

```bash
agent-deck hub serve \
  --listen :8421 \
  --url wss://hub.example:8421 \
  --data ~/.local/share/agent-deck-hub
```

`AGENT_DECK_HUB_URL` is also accepted as a fallback for container setups. The `--url` flag takes precedence, and local runs can omit both if the derived `--listen` URL is correct.

To use your own certificate, pass both files:

```bash
agent-deck hub serve --tls-cert cert.pem --tls-key key.pem
```

## Join A Node

On the hub host, create a single-use invite:

```bash
agent-deck hub invite laptop
```

The invite command prints the exact command to run on the joining machine:

```bash
agent-deck hub join wss://hub.example:8421 --token invite_...
```

On first join with the default self-signed certificate, `agent-deck` shows the hub certificate fingerprint and asks whether to trust it. The accepted fingerprint is stored in `config.toml`, like an SSH known-host key. Future connects reject the hub if that certificate changes.

`hub join` writes the node token to the configured token file and enables hub auto-connect in `config.toml`. After that, starting the TUI connects to the hub automatically. For non-TUI service mode, run:

```bash
agent-deck hub connect
```

## TUI Layout

When hub is configured, groups are shown with their owner:

```text
local / my-sessions
server1 / my-sessions
macbook / experiments
```

`local` is shown only when hub is configured. Remote hub sessions are projected into the normal list rather than under `remotes/`.

Common TUI actions route to the owner node:

- `Enter`: attach through the hub relay.
- prompt hotkey: send a one-line prompt without attaching.
- `D`: stop the remote session process.
- `R`: restart the remote session.
- `r`: rename the remote session.
- `n` or `N` on a hub group/session: create a session on that node.

## Docker Deployment

The deployment files in `deploy/hub/` run only the hub server. Build and start from the repository root:

```bash
docker compose -f deploy/hub/docker-compose.yml up --build -d
```

The compose file uses the default self-signed certificate. If you run behind a reverse proxy, it must pass WebSocket upgrades to `/ws/node` and HTTPS requests to `/api/join`.

```yaml
command:
  - --listen=:8421
  - --url=wss://hub.example:8421
  - --data=/data
```

## Security Model

- All node traffic uses TLS WebSockets.
- The hub creates a self-signed certificate by default. You can override it with `--tls-cert` and `--tls-key`.
- Join uses a single-use invite token over HTTPS and pins the accepted certificate fingerprint.
- Joined nodes receive long-lived node credentials stored locally with file mode `0600`.
- Joined nodes are trusted. A node that can connect can see session metadata and relay actions to other connected nodes.
- There is no offline queue. Actions require the owner node to be connected.
- No SSH connectivity between nodes is required.
