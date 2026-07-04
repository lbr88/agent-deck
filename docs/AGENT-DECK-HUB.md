# Agent Deck Hub

Agent Deck Hub connects multiple trusted `agent-deck` instances through one encrypted relay. Each joined `agent-deck` owns its local sessions and keeps an outbound `wss://` connection to the hub. Any connected TUI can see, attach to, prompt, stop, restart, rename, and create sessions on other connected nodes.

The hub is not a browser UI and it is not a multi-user auth system. It is a coordination service for nodes you trust and intentionally join.

## Start A Hub

The hub always uses TLS. If you do not provide a certificate, `hub serve` creates and reuses a self-signed certificate in the hub data directory.

```bash
agent-deck hub serve \
  --listen :8421 \
  --bootstrap-admin laptop \
  --data ~/.local/share/agent-deck-hub
```

The data directory stores `hub.db`, node credentials, invite state, latest session snapshots, the hub URL used by invites, and the default self-signed cert/key. By default the hub URL is derived from `--listen`. If that is not the URL clients should use, set it once when starting the hub:

```bash
agent-deck hub serve \
  --listen :8421 \
  --url wss://hub.example:8421 \
  --bootstrap-admin laptop \
  --data ~/.local/share/agent-deck-hub
```

`AGENT_DECK_HUB_URL` is also accepted as a fallback for container setups. The `--url` flag takes precedence, and local runs can omit both if the derived `--listen` URL is correct.

`--bootstrap-admin <node-name>` is the first-run path. It creates and prints a single-use admin invite only when the hub has no registered nodes. After the first admin node joins, later restarts skip bootstrap invite creation.

To use your own certificate, pass both files:

```bash
agent-deck hub serve --tls-cert cert.pem --tls-key key.pem
```

## Join The First Node

Start the hub with `--bootstrap-admin <node-name>`. The hub prints the exact command to run on the first client:

```bash
agent-deck hub join wss://hub.example:8421 --token invite_...
```

That first joined node becomes a hub admin. Admin nodes can manage the hub from their own machine:

```bash
agent-deck hub invite desktop
agent-deck hub nodes
agent-deck hub nodes promote node_...
```

The invite command prints the exact command to run on the joining machine:

```bash
agent-deck hub join wss://hub.example:8421 --token invite_...
```

Use `agent-deck hub invite --admin <node-name>` when the new node should also be able to administer the hub. Non-admin nodes can connect, publish sessions, and use the relay, but cannot create invites or manage registered nodes.

If you are on the hub host and want to manage the local hub database instead of the configured joined hub, use:

```bash
agent-deck hub invite --local laptop
agent-deck hub invite --data /data laptop
agent-deck hub nodes --local
agent-deck hub nodes --data /data
```

If you need to recover admin access from the hub host, promote an already joined node:

```bash
agent-deck hub nodes promote --local node_...
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

The deployment files in `deploy/hub/` run only the hub server. The default compose file pulls the published GHCR image:

```bash
docker compose -f deploy/hub/docker-compose.yml pull
docker compose -f deploy/hub/docker-compose.yml up -d
```

If the package is private, log in to GHCR on the Docker host first:

```bash
echo "$GHCR_TOKEN" | docker login ghcr.io -u <github-user> --password-stdin
```

For local development, build the image directly from the repository root:

```bash
docker build -f deploy/hub/Dockerfile -t agent-deck-hub:local .
```

The compose file uses `ghcr.io/lbr88/agent-deck-hub:latest` and the default self-signed certificate. If you run behind a reverse proxy, it must pass WebSocket upgrades to `/ws/node` and HTTPS requests to `/api/join`, `/api/invites`, `/api/nodes`, and `/api/nodes/promote`.

```yaml
command:
  - --listen=:8421
  - --url=wss://hub.example:8421
  - --bootstrap-admin=laptop
  - --data=/data
```

## Security Model

- All node traffic uses TLS WebSockets.
- The hub creates a self-signed certificate by default. You can override it with `--tls-cert` and `--tls-key`.
- Join uses a single-use invite token over HTTPS and pins the accepted certificate fingerprint.
- Joined nodes receive long-lived node credentials stored locally with file mode `0600`.
- Admin nodes can create more invites and manage registered nodes. Bootstrap admin invite creation only happens while the hub has no registered nodes.
- Joined nodes are trusted. A node that can connect can see session metadata and relay actions to other connected nodes. A non-admin node cannot create invites.
- There is no offline queue. Actions require the owner node to be connected.
- No SSH connectivity between nodes is required.
