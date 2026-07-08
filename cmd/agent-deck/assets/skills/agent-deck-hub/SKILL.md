---
name: agent-deck-hub
description: Use when an agent needs to inspect trusted Agent Deck Hub nodes, open an interactive shell on a hub-connected node, or explain the Agent Deck Hub CLI workflow.
---

# Agent Deck Hub

## Overview

Agent Deck Hub connects multiple running `agent-deck` nodes over TLS. A node can see and attach to sessions from trusted peers. Use the CLI, not SSH, when the user wants work routed through the hub.

## Check the hub

Run:

```sh
agent-deck hub status
```

If the hub is not configured, do not invent a hub URL or token. Ask the user to join the node first.

To see visible nodes:

```sh
agent-deck hub nodes
```

If a node is missing, it may be offline or not trusted yet.

## Inspect and control remote sessions

List visible hub sessions:

```sh
agent-deck hub sessions
agent-deck hub sessions <node-name-or-id>
agent-deck hub sessions --json
```

Create a normal Agent Deck session on a hub node, including nodes that currently have zero sessions:

```sh
agent-deck hub sessions create <node-name-or-id> --title "task" --tool codex --cwd /path/to/project --group default
```

Common native actions route to the owning node:

```sh
agent-deck hub sessions attach <node> <session-id-or-title>
agent-deck hub sessions send <node> <session> "message"
agent-deck hub sessions close <node> <session>
agent-deck hub sessions restart <node> <session>
agent-deck hub sessions restart-fresh <node> <session>
agent-deck hub sessions fork <node> <session>
agent-deck hub sessions rename <node> <session> "new title"
agent-deck hub sessions move <node> <session> group/path
agent-deck hub sessions archive <node> <session>
agent-deck hub sessions unarchive <node> <session>
agent-deck hub sessions delete <node> <session>
agent-deck hub sessions remove <node> <session>
agent-deck hub sessions toggle-yolo <node> <session>
agent-deck hub sessions preview <node> <session>
```

Prefer node names from `agent-deck hub nodes`; use node ids when names are ambiguous. Prefer session ids when titles are ambiguous.

## Open a remote shell

Open and attach to a shell session on a trusted node:

```sh
agent-deck hub shell <node-name-or-id>
```

Useful options:

```sh
agent-deck hub shell <node> --cwd /path/to/project
agent-deck hub shell <node> --title "maintenance shell"
agent-deck hub shell <node> --group ops
```

Create the shell without attaching, for scripting:

```sh
agent-deck hub shell <node> --no-attach --json
```

The command creates a normal `tool=shell` Agent Deck session on the target node. It is visible in that node's session list and can be attached later from Agent Deck.

## Trust and access

Hub access is trust-gated by nodes. If a target node is visible but attach or create fails with a trust error, ask the user to approve trust from the owning node:

```sh
agent-deck hub trust pending
agent-deck hub trust allow <node-name-or-id>
```

Do not use hub shell access for destructive commands unless the user has explicitly asked for that action.
