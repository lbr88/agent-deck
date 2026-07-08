# Agent Deck Hub

Agent Deck Hub connects multiple `agent-deck` instances through one encrypted relay. Each joined `agent-deck` owns its local sessions and keeps an outbound `wss://` connection to the hub. Joined nodes can publish their own sessions immediately, but each existing node must approve a new node before that new node can see, attach to, prompt, stop, restart, rename, or create sessions on the existing node.

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
agent-deck hub status
agent-deck hub nodes
agent-deck hub nodes promote node_...
agent-deck hub nodes demote node_...
agent-deck hub nodes rename node_... desktop
agent-deck hub nodes revoke node_...
agent-deck hub invites
agent-deck hub invites revoke inv_...
agent-deck hub trust pending
agent-deck hub trust allow node_...
agent-deck hub trust deny node_...
```

The invite command prints the exact command to run on the joining machine:

```bash
agent-deck hub join wss://hub.example:8421 --token invite_...
```

Use `agent-deck hub invite --admin <node-name>` when the new node should also be able to administer the hub. Non-admin nodes can connect, publish sessions, and use the relay, but cannot create/revoke invites or manage registered nodes.

An invite grants hub membership, not automatic access to every other node. When a new node joins, each already joined node receives a TUI confirmation asking whether to allow that new node to access that node's local sessions. The owner node can also use the CLI fallback:

```bash
agent-deck hub trust pending
agent-deck hub trust allow node_...
agent-deck hub trust deny node_...
```

If you are on the hub host and want to manage the local hub database instead of the configured joined hub, use:

```bash
agent-deck hub invite --local laptop
agent-deck hub invite --data /data laptop
agent-deck hub nodes --local
agent-deck hub nodes --data /data
agent-deck hub invites --local
agent-deck hub invites --data /data
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

## Agent CLI Context

Agents launched inside `agent-deck` can be told about the hub without hardcoding hub details into prompts or repository docs:

```bash
agent-deck agent-context --format plain
agent-deck agent-context --format hook-json
```

When this node has not joined a hub, the command prints nothing and exits successfully. When the hub is configured, it emits short guidance that tells the agent to use:

```bash
agent-deck hub nodes
agent-deck hub shell <node-name-or-id> --cwd <path> --title <title>
```

The output intentionally omits node credentials, invite values, TLS fingerprints, and token file paths. `--format plain` is for hook systems that add stdout to model context. `--format hook-json` emits hook JSON using `hookSpecificOutput.additionalContext`; `codex-json` remains accepted as a compatibility alias.

Install integrations through the normal hook installers:

```bash
agent-deck hooks install
agent-deck codex-hooks install
agent-deck gemini-hooks install
agent-deck cursor-hooks install
agent-deck hermes-hooks install
agent-deck kiro-hooks install
agent-deck opencode-hooks install
```

These installers add Agent Deck's existing status hooks where supported and also install hub context hooks by default. The context hook command is silent when the hub is not configured, so it is safe to install before a node joins a hub.

Current model-visible context support:

- Claude: `SessionStart`.
- Codex: `SessionStart`.
- Gemini: `SessionStart`.
- Cursor: `sessionStart`.
- Hermes: `on_session_start`.
- Kiro: `agentSpawn` on a generated global `agent-deck` custom agent. Agent Deck launches Kiro with that agent when Kiro hooks are installed and no other Kiro agent is configured.
- OpenCode: no prompt-context hook is installed; `opencode-hooks install` removes the legacy Agent Deck context plugin if present.
- OpenCode: global plugin using the system prompt transform hook.

Codex may require reviewing and trusting the installed command hooks through its `/hooks` UI before they run. Tools without a verified model-visible hook contract are not wired to `agent-context`.

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

The compose file uses the published hub image and the default self-signed certificate. If you run behind a reverse proxy, it must pass WebSocket upgrades to `/ws/node` and HTTPS requests to `/api/join`, `/api/status`, `/api/invites`, `/api/invites/revoke`, `/api/nodes`, `/api/nodes/promote`, `/api/nodes/demote`, `/api/nodes/rename`, `/api/nodes/revoke`, `/api/trust/pending`, `/api/trust/allow`, and `/api/trust/deny`.

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
- Admin nodes can create/revoke invites and manage registered nodes. Bootstrap admin invite creation only happens while the hub has no registered nodes.
- Demote and revoke refuse to remove the last admin node.
- Revoking a node removes its stored credential and latest snapshot, and the hub closes any active websocket for that node.
- Invite tokens grant hub membership. Access to another node's sessions requires an explicit allow decision from that owner node.
- Admin status controls hub administration only. Admin nodes do not bypass per-owner trust gates.
- Until an owner node allows a requester, the hub withholds that owner's snapshots and rejects attach/action relay attempts from the requester.
- There is no offline queue. Actions require the owner node to be connected.
- No SSH connectivity between nodes is required.
