import assert from "node:assert/strict";
import fs, { existsSync } from "node:fs";
import { syncBuiltinESMExports } from "node:module";
import { spawnSync } from "node:child_process";
import {
	mkdir,
	readdir,
	readFile,
	rename,
	stat,
	symlink,
	writeFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";

import identityExtension from "./identity.mjs";

const ENV_KEYS = ["AGENTDECK_INSTANCE_ID", "AGENTDECK_OMP_DIR", "AGENTDECK_OMP_LAUNCH_ID"];

async function makeHarness(t, options = {}) {
	const base = await import("node:fs/promises").then(fs => fs.mkdtemp(path.join(os.tmpdir(), "agent-deck-omp-")));
	const managedRoot = path.join(base, "agent-deck");
	const ompDir = path.join(managedRoot, options.instanceId ?? "entry-a");
	await mkdir(ompDir, { recursive: true });

	const previousEnv = Object.fromEntries(ENV_KEYS.map(key => [key, process.env[key]]));
	process.env.AGENTDECK_INSTANCE_ID = options.instanceId ?? "entry-a";
	process.env.AGENTDECK_OMP_DIR = ompDir;
	process.env.AGENTDECK_OMP_LAUNCH_ID = options.launchId ?? "launch-123";
	await writeFile(path.join(ompDir, ".agent-deck-launch-generation"), `${options.authorizedLaunchId ?? options.launchId ?? "launch-123"}\n`);

	const handlers = new Map();
	const titleCalls = [];
	const notifications = [];
	const timers = [];
	const state = {
		file: options.sessionFile ?? path.join(ompDir, "root.jsonl"),
		id: options.sessionId ?? "session-a",
		name: options.sessionName ?? "provider-inherited-name",
		cwd: options.cwd ?? path.join(base, "project"),
	};
	const manager = {
		getSessionFile: () => state.file,
		getSessionId: () => state.id,
		getSessionName: () => state.name,
		getCwd: () => state.cwd,
	};
	const ctx = {
		mode: options.mode ?? "tui",
		hasUI: options.hasUI ?? true,
		sessionManager: manager,
		ui: {
			notify(message, type) {
				notifications.push({ message, type });
			},
		},
		setInterval(callback, milliseconds) {
			if (options.intervalError) throw options.intervalError;
			timers.push({ callback, milliseconds });
			return { callback, milliseconds };
		},
	};
	const pi = {
		on(event, callback) {
			assert.equal(handlers.has(event), false, `duplicate ${event} handler`);
			handlers.set(event, callback);
		},
		async setSessionName(title) {
			titleCalls.push(title);
			if (options.titleGate) await options.titleGate;
			if (options.titleError) throw options.titleError;
		},
	};
	identityExtension(pi);

	t.after(async () => {
		for (const key of ENV_KEYS) {
			if (previousEnv[key] === undefined) delete process.env[key];
			else process.env[key] = previousEnv[key];
		}
		await import("node:fs/promises").then(fs => fs.rm(base, { recursive: true, force: true }));
	});

	return {
		base,
		managedRoot,
		ompDir,
		state,
		ctx,
		handlers,
		titleCalls,
		notifications,
		timers,
		async emit(name, event = {}) {
			const handler = handlers.get(name);
			assert.ok(handler, `missing ${name} handler`);
			return handler({ type: name, ...event }, ctx);
		},
	};
}

async function writeTranscript(file, id, prefix = "") {
	await mkdir(path.dirname(file), { recursive: true });
	await writeFile(file, `${prefix}{"type":"session","id":${JSON.stringify(id)}}\n`);
}

async function readBinding(dir) {
	return readFile(path.join(dir, ".agent-deck-active-session.launch-123"), "utf8");
}

async function readStatus(dir) {
	return JSON.parse(await readFile(path.join(dir, ".agent-deck-omp-status.launch-123.json"), "utf8"));
}

function binding(file, id, state = "saved", generation = "launch-123") {
	return `1\n${file}\n${id}\n${state}\n${generation}\n`;
}

test("move then native new/fork/branch binds the new conversation in the moved directory", async t => {
	const h = await makeHarness(t);
	await h.emit("session_start");
	h.state.file = path.join(h.base, "moved", "first.jsonl");
	await writeTranscript(h.state.file, h.state.id);
	await h.timers[0].callback();
	for (const event of ["session_switch", "session_branch"]) {
		h.state.id += "-next";
		h.state.file = path.join(h.base, "moved", `${h.state.id}.jsonl`);
		await writeTranscript(h.state.file, h.state.id);
		await h.emit(event, { reason: "new" });
		assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));
	}
});

test("move then new before the next timer captures the moved parent before switching", async t => {
	const h = await makeHarness(t);
	await h.emit("session_start");
	h.state.file = path.join(h.base, "quick-move", "parent.jsonl");
	await writeTranscript(h.state.file, h.state.id);
	assert.equal(await h.emit("session_before_switch", { reason: "new" }), undefined);
	h.state.id = "new-after-quick-move";
	h.state.file = path.join(h.base, "quick-move", "new.jsonl");
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_switch", { reason: "new" });
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));
});

test("rejecting a switch never acknowledges an identity whose publication failed", async t => {
	const h = await makeHarness(t);
	await writeFile(h.state.file, "invalid header\n");
	await h.emit("session_start");
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
	assert.deepEqual(await h.emit("session_before_switch", { reason: "resume" }), { cancel: true });
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
});

test("a missing saved transcript is an error, never a new lazy conversation", async t => {
	const h = await makeHarness(t);
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_start");
	const saved = await readBinding(h.ompDir);
	await rename(h.state.file, `${h.state.file}.preserved`);
	await h.timers[0].callback();
	assert.equal(await readBinding(h.ompDir), saved);
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
	assert.match((await readStatus(h.ompDir)).error, /missing|disappeared/i);
});

test("an older asynchronous title failure cannot hide a newer ownership conflict", async t => {
	let rejectTitle;
	const titleGate = new Promise((_resolve, reject) => { rejectTitle = reject; });
	const h = await makeHarness(t, { titleGate });
	await writeTranscript(h.state.file, h.state.id);
	await writeFile(path.join(h.ompDir, ".agent-deck-title.json"), JSON.stringify({ title: "Delayed title" }));
	const starting = h.emit("session_start");
	assert.equal(h.titleCalls.length, 1);
	const sibling = path.join(h.managedRoot, "sibling");
	await mkdir(sibling);
	await writeFile(path.join(sibling, ".agent-deck-active-session"), binding(path.join(sibling, "root.jsonl"), h.state.id));
	await h.emit("agent_end");
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
	rejectTitle(new Error("delayed title failure"));
	await starting;
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
	assert.match((await readStatus(h.ompDir)).error, /claimed|ownership/i);
});

test("a rejected switch cannot reacknowledge a conversation claimed by a sibling", async t => {
	const h = await makeHarness(t);
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_start");
	const sibling = path.join(h.managedRoot, "sibling");
	await mkdir(sibling);
	await writeFile(path.join(sibling, ".agent-deck-active-session"), binding(path.join(sibling, "root.jsonl"), h.state.id));
	await h.timers[0].callback();
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
	assert.deepEqual(await h.emit("session_before_switch", { reason: "resume" }), { cancel: true });
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
});

test("move then branch before the next timer captures the moved parent", async t => {
	const h = await makeHarness(t);
	await h.emit("session_start");
	h.state.file = path.join(h.base, "quick-branch", "parent.jsonl");
	await writeTranscript(h.state.file, h.state.id);
	assert.equal(await h.emit("session_before_branch", { entryId: "message-id" }), undefined);
	h.state.id = "branched-after-move";
	h.state.file = path.join(h.base, "quick-branch", "branch.jsonl");
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_branch");
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));
});

test("old generation cannot delete a newer fresh boundary during marker removal", async t => {
	const h = await makeHarness(t);
	const common = path.join(h.ompDir, ".agent-deck-fresh-pending");
	const oldSpecific = `${common}.launch-123`;
	const newSpecific = `${common}.launch-new`;
	await writeFile(common, "launch-123\n");
	await writeFile(oldSpecific, "launch-123\n");
	const originalUnlink = fs.unlinkSync;
	let injected = false;
	fs.unlinkSync = file => {
		if (!injected && String(file).includes(".agent-deck-fresh-pending")) {
			injected = true;
			fs.writeFileSync(path.join(h.ompDir, ".agent-deck-launch-generation"), "launch-new\n");
			fs.writeFileSync(common, "launch-new\n");
			fs.writeFileSync(newSpecific, "launch-new\n");
		}
		return originalUnlink(file);
	};
	syncBuiltinESMExports();
	try { await h.emit("session_start"); }
	finally { fs.unlinkSync = originalUnlink; syncBuiltinESMExports(); }
	assert.equal(injected, true);
	assert.equal(await readFile(common, "utf8"), "launch-new\n");
	assert.equal(await readFile(newSpecific, "utf8"), "launch-new\n");
});

test("generation rotation immediately before rename cannot overwrite the selected newer binding", async t => {
	const h = await makeHarness(t);
	const newerFile = path.join(h.ompDir, "newer.jsonl");
	const newerBinding = binding(newerFile, "newer-id", "saved", "launch-new");
	const originalRename = fs.renameSync;
	let injected = false;
	fs.renameSync = (from, to) => {
		if (!injected && String(to).includes(".agent-deck-active-session")) {
			injected = true;
			fs.writeFileSync(path.join(h.ompDir, ".agent-deck-launch-generation"), "launch-new\n");
			fs.writeFileSync(path.join(h.ompDir, ".agent-deck-active-session.launch-new"), newerBinding);
		}
		return originalRename(from, to);
	};
	syncBuiltinESMExports();
	try { await h.emit("session_start"); }
	finally { fs.renameSync = originalRename; syncBuiltinESMExports(); }
	assert.equal(injected, true);
	assert.equal(await readFile(path.join(h.ompDir, ".agent-deck-active-session.launch-new"), "utf8"), newerBinding);
	assert.equal(existsSync(path.join(h.ompDir, ".agent-deck-active-session")), false, "a stale writer must never write the common binding");
});

test("resume rejects a FIFO without blocking the TUI", async t => {
	if (process.platform === "win32") return t.skip("POSIX FIFO test");
	const h = await makeHarness(t);
	const fifo = path.join(h.ompDir, "fifo.jsonl");
	assert.equal(spawnSync("mkfifo", [fifo]).status, 0);
	const code = `import extension from ${JSON.stringify(new URL("./identity.mjs", import.meta.url).href)};
const handlers = new Map();
extension({on: (e, cb) => handlers.set(e, cb)});
const ctx = {mode:'tui',hasUI:true,ui:{notify(){}},setInterval(){},sessionManager:{getSessionFile:()=>${JSON.stringify(h.state.file)},getSessionId:()=> 'session-a',getSessionName:()=>'',getCwd:()=>''}};
await handlers.get('session_start')({},ctx);
const result = await handlers.get('session_before_switch')({reason:'resume',targetSessionFile:${JSON.stringify(fifo)}},ctx);
if (!result?.cancel) process.exit(2);`;
	const child = spawnSync(process.execPath, ["--input-type=module", "-e", code], { timeout: 2000, encoding: "utf8" });
	assert.equal(child.status, 0, `FIFO blocked or was allowed: ${child.error ?? child.stderr}`);
});

test("TUI start publishes exact pending identity, acknowledges it, applies explicit title, and owns one timer", async t => {
	const h = await makeHarness(t);
	await writeFile(path.join(h.ompDir, ".agent-deck-title.json"), JSON.stringify({ title: "Explicit deck title" }));
	await writeFile(path.join(h.ompDir, ".agent-deck-fresh-pending.launch-123"), "launch-123\n");
	const historical = path.join(h.ompDir, "historical.jsonl");
	await writeTranscript(historical, "historical-id");

	await h.emit("session_start");

	assert.equal(await readBinding(h.ompDir), binding(h.state.file, "session-a", "pending"));
	const status = await readStatus(h.ompDir);
	assert.deepEqual(
		{
			instance_id: status.instance_id,
			launch_id: status.launch_id,
			pid: status.pid,
			session_id: status.session_id,
			session_file: status.session_file,
			identity_ready: status.identity_ready,
			error: status.error,
		},
		{
			instance_id: "entry-a",
			launch_id: "launch-123",
			pid: process.pid,
			session_id: "session-a",
			session_file: h.state.file,
			identity_ready: true,
			error: null,
		},
	);
	assert.equal(new Date(status.updated_at).toISOString(), status.updated_at);
	assert.deepEqual(h.titleCalls, ["Explicit deck title"]);
	assert.deepEqual(h.timers.map(timer => timer.milliseconds), [1000]);
	assert.equal(await readFile(path.join(h.ompDir, ".agent-deck-root-launch-123.pid"), "utf8"), `${process.pid}\n`);
	assert.equal(existsSync(path.join(h.ompDir, ".agent-deck-fresh-pending.launch-123")), false);
	assert.equal(existsSync(historical), true);
	assert.deepEqual(
		[...h.handlers.keys()].sort(),
		["agent_end", "session_before_branch", "session_before_switch", "session_branch", "session_shutdown", "session_start", "session_switch"],
	);
	const mode = (await stat(path.join(h.ompDir, ".agent-deck-active-session.launch-123"))).mode & 0o777;
	assert.equal(mode, 0o600);
	assert.equal((await readdir(h.ompDir)).some(name => name.includes(".tmp-")), false);
});

test("print-mode inherited extension never claims root state or creates a timer", async t => {
	const h = await makeHarness(t, { mode: "print", hasUI: false });

	for (const event of ["session_start", "session_switch", "session_branch", "agent_end", "session_shutdown"]) {
		await h.emit(event);
	}
	assert.equal(await h.emit("session_before_switch", { reason: "resume", targetSessionFile: "/elsewhere/root.jsonl" }), undefined);

	assert.equal(existsSync(path.join(h.ompDir, ".agent-deck-active-session.launch-123")), false);
	assert.equal(existsSync(path.join(h.ompDir, ".agent-deck-omp-status.launch-123.json")), false);
	assert.equal(existsSync(path.join(h.ompDir, ".agent-deck-root-launch-123.pid")), false);
	assert.equal(h.timers.length, 0);
	assert.equal(h.titleCalls.length, 0);
});

test("agent end and timer promote only a materialized file and follow new, branch, shutdown, and move identities", async t => {
	const h = await makeHarness(t);
	await h.emit("session_start");
	assert.match(await readBinding(h.ompDir), /\npending\n/);

	await writeTranscript(h.state.file, h.state.id);
	await h.emit("agent_end");
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));

	const oldFile = h.state.file;
	h.state.file = path.join(h.ompDir, "new-root.jsonl");
	h.state.id = "session-new";
	await h.emit("session_switch", { reason: "new", previousSessionFile: oldFile });
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id, "pending"));
	await writeTranscript(h.state.file, h.state.id);
	await h.timers[0].callback();
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));

	h.state.file = path.join(h.ompDir, "fork-root.jsonl");
	h.state.id = "session-fork";
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_switch", { reason: "fork", previousSessionFile: oldFile });
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));

	h.state.file = path.join(h.ompDir, "branch-root.jsonl");
	h.state.id = "session-branch";
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_branch");
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));

	h.state.file = path.join(h.ompDir, "shutdown-root.jsonl");
	h.state.id = "session-shutdown";
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_shutdown");
	assert.equal(await readBinding(h.ompDir), binding(h.state.file, h.state.id));

	const moved = path.join(h.base, "moved", "shutdown-root.jsonl");
	await mkdir(path.dirname(moved), { recursive: true });
	await rename(h.state.file, moved);
	h.state.file = moved;
	await h.timers[0].callback();
	assert.equal(await readBinding(h.ompDir), binding(moved, h.state.id));

	const frozen = await readBinding(h.ompDir);
	h.ctx.mode = "print";
	h.state.file = path.join(h.ompDir, "child.jsonl");
	h.state.id = "child-id";
	await h.timers[0].callback();
	assert.equal(await readBinding(h.ompDir), frozen);
});

test("switch guard rejects other rows, nested tasks, foreign external IDs, and ownership conflicts", async t => {
	const h = await makeHarness(t);
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_start");

	const siblingTarget = path.join(h.managedRoot, "entry-b", "other.jsonl");
	await writeTranscript(siblingTarget, "other-id");
	assert.deepEqual(
		await h.emit("session_before_switch", { reason: "resume", targetSessionFile: siblingTarget }),
		{ cancel: true },
	);

	const nestedTarget = path.join(h.ompDir, "root", "task.jsonl");
	await writeTranscript(nestedTarget, "task-id");
	assert.deepEqual(
		await h.emit("session_before_switch", { reason: "resume", targetSessionFile: nestedTarget }),
		{ cancel: true },
	);

	const foreign = path.join(h.base, "external", "foreign.jsonl");
	await writeTranscript(foreign, "foreign-id");
	assert.deepEqual(
		await h.emit("session_before_switch", { reason: "resume", targetSessionFile: foreign }),
		{ cancel: true },
	);

	const relocated = path.join(h.base, "external", "same-id.jsonl");
	await writeTranscript(relocated, h.state.id, '{"type":"title","title":"ignored"}\n');
	assert.equal(
		await h.emit("session_before_switch", { reason: "resume", targetSessionFile: relocated }),
		undefined,
	);

	const siblingDir = path.join(h.managedRoot, "entry-c");
	await mkdir(siblingDir, { recursive: true });
	await writeFile(
		path.join(siblingDir, ".agent-deck-active-session"),
		binding(path.join(h.base, "somewhere-else.jsonl"), h.state.id),
	);
	assert.deepEqual(
		await h.emit("session_before_switch", { reason: "resume", targetSessionFile: relocated }),
		{ cancel: true },
	);
	assert.ok(h.notifications.length >= 4);
	assert.ok(h.notifications.every(note => note.type === "error" || note.type === "warning"));
});

test("switch guard fails closed on malformed ownership state and catches all inspection errors", async t => {
	const h = await makeHarness(t);
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_start");
	const external = path.join(h.base, "external.jsonl");
	await writeTranscript(external, h.state.id);
	const siblingDir = path.join(h.managedRoot, "broken-row");
	await mkdir(siblingDir, { recursive: true });
	await writeFile(path.join(siblingDir, ".agent-deck-active-session"), "not a binding\n");

	await assert.doesNotReject(async () => {
		assert.deepEqual(
			await h.emit("session_before_switch", { reason: "resume", targetSessionFile: external }),
			{ cancel: true },
		);
	});
	assert.deepEqual(await h.emit("session_before_switch", { reason: "resume" }), { cancel: true });
	assert.equal(await h.emit("session_before_switch", { reason: "new" }), undefined);
	assert.equal(await h.emit("session_before_switch", { reason: "fork" }), undefined);
	assert.match((await readStatus(h.ompDir)).error, /ownership|switch/i);
});

test("external move is rejected when another row claims its path, without replacing the last good binding", async t => {
	const h = await makeHarness(t);
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_start");
	const lastGood = await readBinding(h.ompDir);
	const moved = path.join(h.base, "moved.jsonl");
	await rename(h.state.file, moved);
	h.state.file = moved;
	const siblingDir = path.join(h.managedRoot, "entry-b");
	await mkdir(siblingDir, { recursive: true });
	await writeFile(path.join(siblingDir, ".agent-deck-active-session"), binding(moved, "different-id"));

	await h.timers[0].callback();

	assert.equal(await readBinding(h.ompDir), lastGood);
	const status = await readStatus(h.ompDir);
	assert.equal(status.identity_ready, false);
	assert.match(status.error, /claimed|owner|ownership/i);
});

test("switch and move cannot use an external symlink ancestor to enter a sibling row", async t => {
	const h = await makeHarness(t);
	await writeTranscript(h.state.file, h.state.id);
	await h.emit("session_start");
	const lastGood = await readBinding(h.ompDir);
	const siblingTarget = path.join(h.managedRoot, "entry-b", "same-id.jsonl");
	await writeTranscript(siblingTarget, h.state.id);
	const externalAlias = path.join(h.base, "external-alias");
	await symlink(path.dirname(siblingTarget), externalAlias, "dir");
	const disguisedTarget = path.join(externalAlias, path.basename(siblingTarget));

	assert.deepEqual(
		await h.emit("session_before_switch", { reason: "resume", targetSessionFile: disguisedTarget }),
		{ cancel: true },
	);
	h.state.file = disguisedTarget;
	await h.timers[0].callback();

	assert.equal(await readBinding(h.ompDir), lastGood);
	assert.equal((await readStatus(h.ompDir)).identity_ready, false);
});

test("title intent is reapplied on switch and changes, while provider names are never copied inbound", async t => {
	const h = await makeHarness(t);
	const titlePath = path.join(h.ompDir, ".agent-deck-title.json");
	await writeFile(titlePath, JSON.stringify({ title: "Deck one" }));
	await h.emit("session_start");
	assert.deepEqual(h.titleCalls, ["Deck one"]);

	await h.emit("session_switch", { reason: "resume", previousSessionFile: h.state.file });
	assert.deepEqual(h.titleCalls, ["Deck one", "Deck one"]);
	await writeFile(titlePath, JSON.stringify({ title: "Deck two" }));
	await h.timers[0].callback();
	assert.deepEqual(h.titleCalls, ["Deck one", "Deck one", "Deck two"]);

	await import("node:fs/promises").then(fs => fs.unlink(titlePath));
	h.state.name = "provider-renamed-me";
	await h.timers[0].callback();
	assert.deepEqual(h.titleCalls, ["Deck one", "Deck one", "Deck two"]);
});

test("malformed title and title API failures are durable, visible, and never escape an event", async t => {
	const h = await makeHarness(t, { titleError: new Error("provider title write failed") });
	const titlePath = path.join(h.ompDir, ".agent-deck-title.json");
	await writeFile(titlePath, JSON.stringify({ title: "Deck title" }));
	await assert.doesNotReject(() => h.emit("session_start"));
	assert.equal((await readStatus(h.ompDir)).identity_ready, true);
	assert.match((await readStatus(h.ompDir)).error, /provider title write failed/);
	assert.match(h.notifications.at(-1).message, /provider title write failed/);

	const h2 = await makeHarness(t, { instanceId: "entry-b" });
	await writeFile(path.join(h2.ompDir, ".agent-deck-title.json"), "{broken");
	await assert.doesNotReject(() => h2.emit("session_start"));
	assert.equal((await readStatus(h2.ompDir)).identity_ready, true);
	assert.match((await readStatus(h2.ompDir)).error, /title/i);
});

test("binding write failures preserve fresh intent and produce durable visible error status", async t => {
	const h = await makeHarness(t);
	await mkdir(path.join(h.ompDir, ".agent-deck-active-session.launch-123"));
	await writeFile(path.join(h.ompDir, ".agent-deck-fresh-pending.launch-123"), "launch-123\n");

	await assert.doesNotReject(() => h.emit("session_start"));

	const status = await readStatus(h.ompDir);
	assert.equal(status.identity_ready, false);
	assert.match(status.error, /active session|binding|directory|rename/i);
	assert.ok(h.notifications.some(note => note.type === "error"));
	assert.equal(await readFile(path.join(h.ompDir, ".agent-deck-fresh-pending.launch-123"), "utf8"), "launch-123\n");
});

test("fresh marker from another generation is never removed", async t => {
	const h = await makeHarness(t);
	await writeFile(path.join(h.ompDir, ".agent-deck-fresh-pending"), "older-launch\n");
	await h.emit("session_start");
	assert.equal(await readFile(path.join(h.ompDir, ".agent-deck-fresh-pending"), "utf8"), "older-launch\n");
});

test("launch generation and root pid fences prevent stale callbacks from publishing", async t => {
	const h = await makeHarness(t);
	await h.emit("session_start");
	const originalBinding = await readBinding(h.ompDir);
	const originalStatus = await readFile(path.join(h.ompDir, ".agent-deck-omp-status.launch-123.json"), "utf8");
	await writeFile(path.join(h.ompDir, ".agent-deck-launch-generation"), "newer-launch\n");
	h.state.file = path.join(h.ompDir, "stale.jsonl");
	h.state.id = "stale-id";
	await h.timers[0].callback();
	await h.emit("agent_end");
	assert.equal(await readBinding(h.ompDir), originalBinding);
	assert.equal(await readFile(path.join(h.ompDir, ".agent-deck-omp-status.launch-123.json"), "utf8"), originalStatus);

	const h2 = await makeHarness(t, { instanceId: "entry-b" });
	await writeFile(path.join(h2.ompDir, ".agent-deck-root-launch-123.pid"), `${process.pid + 1}\n`);
	await h2.emit("session_start");
	assert.equal(existsSync(path.join(h2.ompDir, ".agent-deck-active-session.launch-123")), false);
	assert.equal(existsSync(path.join(h2.ompDir, ".agent-deck-omp-status.launch-123.json")), false);
	assert.equal(h2.timers.length, 0);
});

test("root identity reads are bounded and malformed headers fail without loading later history", async t => {
	const h = await makeHarness(t);
	const malformedPrefix = Array.from({ length: 33 }, (_, index) => JSON.stringify({ type: "title", title: `line-${index}` })).join("\n") + "\n";
	await writeTranscript(h.state.file, h.state.id, malformedPrefix);
	await assert.doesNotReject(() => h.emit("session_start"));
	assert.equal(existsSync(path.join(h.ompDir, ".agent-deck-active-session.launch-123")), false);
	const status = await readStatus(h.ompDir);
	assert.equal(status.identity_ready, false);
	assert.match(status.error, /bounded|header|session/i);
});

test("manager and publication errors are caught instead of relying on swallowed hook exceptions", async t => {
	const h = await makeHarness(t);
	h.ctx.sessionManager.getSessionFile = () => {
		throw new Error("manager unavailable");
	};

	await assert.doesNotReject(() => h.emit("session_start"));

	const status = await readStatus(h.ompDir);
	assert.equal(status.identity_ready, false);
	assert.match(status.error, /manager unavailable/);
	assert.match(h.notifications.at(-1).message, /manager unavailable/);
});

test("timer registration failures retain the published identity ACK and become a visible warning", async t => {
	const h = await makeHarness(t, { intervalError: new Error("timer service unavailable") });

	await assert.doesNotReject(() => h.emit("session_start"));

	const status = await readStatus(h.ompDir);
	assert.equal(status.identity_ready, true);
	assert.match(status.error, /timer service unavailable/);
	assert.match(h.notifications.at(-1).message, /timer service unavailable/);
});

test("same pid can reuse the generation owner on extension reload", async t => {
	const h = await makeHarness(t);
	await h.emit("session_start");
	const first = await readBinding(h.ompDir);

	const h2 = await makeHarness(t, {
		instanceId: "entry-a",
		launchId: "launch-123",
		sessionFile: path.join(h.ompDir, "reloaded.jsonl"),
		sessionId: "reloaded-id",
	});
	// Exercise a reload against the original directory while preserving the
	// same process/generation owner established by the first extension.
	process.env.AGENTDECK_OMP_DIR = h.ompDir;
	await h2.emit("session_start");
	assert.notEqual(await readBinding(h.ompDir), first);
	assert.equal(await readFile(path.join(h.ompDir, ".agent-deck-root-launch-123.pid"), "utf8"), `${process.pid}\n`);
});
