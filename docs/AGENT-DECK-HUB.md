# Agent Deck Hub

Agent Deck Hub connects multiple trusted `agent-deck` instances through one encrypted relay. Each joined `agent-deck` owns its local sessions and keeps an outbound `wss://` connection to the hub. Any connected TUI can see, attach to, prompt, stop, restart, rename, and create sessions on other connected nodes.

The hub is not a browser UI and it is not a multi-user auth system. It is a coordination service for nodes you trust and intentionally join.

## Start A Hub

The hub requires TLS. Plaintext hub URLs are refused.

```bash
agent-deck hub serve \
  --listen :8421 \
  --data ~/.local/share/agent-deck-hub \
  --tls-cert cert.pem \
  --tls-key key.pem
```

The data directory stores `hub.db`, including node credentials, invite state, and latest session snapshots.

## Join A Node

On the hub host, create a single-use invite token:

```bash
agent-deck hub invite laptop
```

On the joining machine, exchange that invite for a node credential:

```bash
agent-deck hub join wss://hub.example:8421 --token <token>
```

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

Provide TLS files at `deploy/hub/certs/tls.crt` and `deploy/hub/certs/tls.key`, or adjust the compose volumes and command flags. If you run behind a reverse proxy, it must pass WebSocket upgrades to `/ws/node` and HTTPS requests to `/api/join`.

## Security Model

- All node traffic uses TLS WebSockets.
- Join uses a single-use invite token over HTTPS.
- Joined nodes receive long-lived node credentials stored locally with file mode `0600`.
- Joined nodes are trusted. A node that can connect can see session metadata and relay actions to other connected nodes.
- There is no offline queue. Actions require the owner node to be connected.
- No SSH connectivity between nodes is required.
