import {
	closeSync,
	constants,
	fsyncSync,
	fstatSync,
	lstatSync,
	mkdirSync,
	openSync,
	readSync,
	readdirSync,
	realpathSync,
	renameSync,
	unlinkSync,
	writeFileSync,
} from "node:fs";
import path from "node:path";

const BINDING_NAME = ".agent-deck-active-session";
const FRESH_PENDING_NAME = ".agent-deck-fresh-pending";
const GENERATION_NAME = ".agent-deck-launch-generation";
const TITLE_NAME = ".agent-deck-title.json";
const SMALL_FILE_LIMIT = 16 * 1024;
const HEADER_LIMIT = 256 * 1024;
const HEADER_LINE_LIMIT = 32;
const SAFE_LAUNCH_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

let temporarySequence = 0;

function describeError(error) {
	return error instanceof Error ? error.message : String(error);
}

function isMissing(error) {
	return error && typeof error === "object" && error.code === "ENOENT";
}

function isTuiRootContext(ctx) {
	return ctx?.mode === "tui" && ctx?.hasUI === true;
}

function validateSingleLine(value, label, maximum = 4096) {
	if (typeof value !== "string" || value.length === 0 || value.length > maximum || /[\0\r\n]/.test(value)) {
		throw new Error(`invalid OMP ${label}`);
	}
	return value;
}

function readPrefix(fd, maximum) {
	const buffer = Buffer.allocUnsafe(maximum + 1);
	let total = 0;
	while (total < buffer.length) {
		const count = readSync(fd, buffer, total, buffer.length - total, total);
		if (count === 0) break;
		total += count;
	}
	return buffer.subarray(0, total);
}

function readSmallRegularFile(file, label) {
	let metadata;
	try {
		metadata = lstatSync(file);
	} catch (error) {
		if (isMissing(error)) return undefined;
		throw new Error(`cannot inspect ${label}: ${describeError(error)}`);
	}
	if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error(`${label} is not a regular file`);
	if (metadata.size > SMALL_FILE_LIMIT) throw new Error(`${label} exceeds ${SMALL_FILE_LIMIT} bytes`);
	let fd;
	try {
		fd = openSync(file, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
		const contents = readPrefix(fd, SMALL_FILE_LIMIT);
		if (contents.length > SMALL_FILE_LIMIT) throw new Error(`${label} exceeds ${SMALL_FILE_LIMIT} bytes`);
		return contents.toString("utf8");
	} catch (error) {
		if (error instanceof Error && error.message.includes("exceeds")) throw error;
		throw new Error(`cannot read ${label}: ${describeError(error)}`);
	} finally {
		if (fd !== undefined) closeSync(fd);
	}
}

function readHeaderSessionId(file) {
	let fd;
	try {
		if (!lstatSync(file).isFile()) throw new Error(`OMP transcript is not a regular file: ${file}`);
		fd = openSync(file, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0) | (constants.O_NONBLOCK ?? 0));
		if (!fstatSync(fd).isFile()) throw new Error(`OMP transcript is not a regular file: ${file}`);
		const lines = readPrefix(fd, HEADER_LIMIT - 1).toString("utf8").split("\n");
		for (let index = 0; index < Math.min(lines.length, HEADER_LINE_LIMIT); index += 1) {
			const line = lines[index];
			if (line.length === 0) continue;
			let entry;
			try {
				entry = JSON.parse(line);
			} catch (error) {
				throw new Error(`invalid OMP transcript header at ${file}: ${describeError(error)}`);
			}
			if (entry?.type === "session" && typeof entry.id === "string" && entry.id.length > 0) {
				return validateSingleLine(entry.id, "transcript session ID");
			}
		}
		throw new Error(`OMP transcript has no valid bounded session header: ${file}`);
	} catch (error) {
		if (isMissing(error)) throw error;
		if (error instanceof Error && error.message.startsWith("OMP transcript")) throw error;
		if (error instanceof Error && error.message.startsWith("invalid OMP transcript")) throw error;
		throw new Error(`cannot read OMP transcript header at ${file}: ${describeError(error)}`);
	} finally {
		if (fd !== undefined) closeSync(fd);
	}
}

function parseBinding(contents, source) {
	if (contents === undefined) return undefined;
	if (contents.includes("\0") || contents.includes("\r")) throw new Error(`invalid OMP ownership binding in ${source}`);
	const lines = contents.endsWith("\n") ? contents.slice(0, -1).split("\n") : contents.split("\n");
	if (lines.length !== 5 || lines[0] !== "1") throw new Error(`invalid OMP ownership binding in ${source}`);
	const file = validateSingleLine(lines[1], "owned session path");
	const id = validateSingleLine(lines[2], "owned session ID");
	if (!path.isAbsolute(file) || path.extname(file) !== ".jsonl") {
		throw new Error(`invalid OMP ownership path in ${source}`);
	}
	if (lines[3] !== "pending" && lines[3] !== "saved") throw new Error(`invalid OMP ownership state in ${source}`);
	validateSingleLine(lines[4], "ownership generation");
	return { file: path.resolve(file), id, state: lines[3], generation: lines[4] };
}

function atomicWrite(file, contents, commitGuard) {
	mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
	const temporary = path.join(
		path.dirname(file),
		`.${path.basename(file)}.tmp-${process.pid}-${++temporarySequence}`,
	);
	let fd;
	try {
		fd = openSync(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
		writeFileSync(fd, contents, "utf8");
		fsyncSync(fd);
		closeSync(fd);
		fd = undefined;
		commitGuard?.();
		renameSync(temporary, file);
		const directoryFd = openSync(path.dirname(file), constants.O_RDONLY);
		try {
			fsyncSync(directoryFd);
		} finally {
			closeSync(directoryFd);
		}
	} catch (error) {
		if (fd !== undefined) {
			try {
				closeSync(fd);
			} catch {}
		}
		try {
			unlinkSync(temporary);
		} catch {}
		throw error;
	}
}

function pathIsInside(root, candidate) {
	const relative = path.relative(root, candidate);
	return relative !== "" && relative !== ".." && !relative.startsWith(`..${path.sep}`) && !path.isAbsolute(relative);
}

function loadConfiguration() {
	const instanceId = validateSingleLine(process.env.AGENTDECK_INSTANCE_ID, "Agent Deck instance ID", 1024);
	const ompDirValue = validateSingleLine(process.env.AGENTDECK_OMP_DIR, "Agent Deck directory", 8192);
	const launchId = validateSingleLine(process.env.AGENTDECK_OMP_LAUNCH_ID, "launch generation", 128);
	if (!path.isAbsolute(ompDirValue)) throw new Error("AGENTDECK_OMP_DIR must be absolute");
	if (!SAFE_LAUNCH_ID.test(launchId)) throw new Error("AGENTDECK_OMP_LAUNCH_ID is not a safe token");
	const ompDir = path.resolve(ompDirValue);
	let realOmpDir;
	let realManagedRoot;
	try {
		realOmpDir = realpathSync.native(ompDir);
		realManagedRoot = realpathSync.native(path.dirname(ompDir));
	} catch (error) {
		throw new Error(`cannot resolve AGENTDECK_OMP_DIR: ${describeError(error)}`);
	}
	if (realOmpDir !== path.join(realManagedRoot, path.basename(ompDir))) {
		throw new Error("AGENTDECK_OMP_DIR must not be a symlink to another managed row");
	}
	return {
		instanceId,
		launchId,
		ompDir,
		managedRoot: path.dirname(ompDir),
		realOmpDir,
		realManagedRoot,
		bindingFile: path.join(ompDir, `${BINDING_NAME}.${launchId}`),
		sourceBindingFile: path.join(ompDir, `.agent-deck-source-binding.${launchId}`),
		freshPendingFile: path.join(ompDir, `${FRESH_PENDING_NAME}.${launchId}`),
		generationFile: path.join(ompDir, GENERATION_NAME),
		ownerFile: path.join(ompDir, `.agent-deck-root-${launchId}.pid`),
		statusFile: path.join(ompDir, `.agent-deck-omp-status.${launchId}.json`),
		titleFile: path.join(ompDir, TITLE_NAME),
	};
}

function readAuthorizedGeneration(config) {
	const contents = readSmallRegularFile(config.generationFile, "OMP launch-generation file");
	if (contents === undefined) throw new Error("OMP launch-generation file is missing");
	const generation = contents.endsWith("\n") ? contents.slice(0, -1) : contents;
	return validateSingleLine(generation, "authorized launch generation", 128);
}

function assertAuthorized(config) {
	if (readAuthorizedGeneration(config) !== config.launchId) throw new Error("OMP launch generation is stale");
	const owner = readSmallRegularFile(config.ownerFile, "OMP root owner file");
	if (owner === undefined || owner !== `${process.pid}\n`) throw new Error("OMP root process ownership was lost");
}

function claimRoot(config) {
	if (readAuthorizedGeneration(config) !== config.launchId) throw new Error("OMP launch generation is stale");
	let fd;
	try {
		fd = openSync(config.ownerFile, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
		writeFileSync(fd, `${process.pid}\n`, "utf8");
		fsyncSync(fd);
		closeSync(fd);
		fd = undefined;
	} catch (error) {
		if (fd !== undefined) {
			try {
				closeSync(fd);
			} catch {}
			try {
				unlinkSync(config.ownerFile);
			} catch {}
		}
		if (error?.code !== "EEXIST") throw new Error(`cannot claim OMP root process: ${describeError(error)}`);
		const owner = readSmallRegularFile(config.ownerFile, "OMP root owner file");
		if (owner !== `${process.pid}\n`) throw new Error("another OMP root process owns this launch generation");
	}
}

function readOwnBinding(config) {
	const current = readSmallRegularFile(config.bindingFile, "OMP active-session binding");
	if (current !== undefined) return parseBinding(current, config.bindingFile);
	return parseBinding(readSmallRegularFile(config.sourceBindingFile, "OMP launch-source binding"), config.sourceBindingFile);
}

function assertNoSiblingClaim(config, file, id) {
	let entries;
	try {
		entries = readdirSync(config.managedRoot, { withFileTypes: true });
	} catch (error) {
		throw new Error(`cannot inspect OMP ownership rows: ${describeError(error)}`);
	}
	for (const entry of entries) {
		if (!entry.isDirectory()) continue;
		const siblingDir = path.join(config.managedRoot, entry.name);
		if (path.resolve(siblingDir) === config.ompDir) continue;
		const generation = readSmallRegularFile(path.join(siblingDir, GENERATION_NAME), "OMP sibling generation")?.trim();
		if (generation !== undefined && !SAFE_LAUNCH_ID.test(generation)) throw new Error("invalid OMP sibling generation");
		let siblingBindingFile = path.join(siblingDir, generation ? `${BINDING_NAME}.${generation}` : BINDING_NAME);
		if (generation && readSmallRegularFile(siblingBindingFile, "OMP sibling binding") === undefined) {
			siblingBindingFile = path.join(siblingDir, `.agent-deck-source-binding.${generation}`);
		}
		const sibling = parseBinding(
			readSmallRegularFile(siblingBindingFile, `OMP ownership binding for ${entry.name}`),
			siblingBindingFile,
		);
		if (!sibling) continue;
		if (sibling.file === path.resolve(file) || sibling.id === id) {
			throw new Error(`OMP session ${id} at ${file} is claimed by another Agent Deck entry`);
		}
	}
}

function locationCategory(root, row, file) {
	const resolved = path.resolve(file);
	if (!pathIsInside(root, resolved)) return "external";
	return path.dirname(resolved) === row ? "owned" : "other";
}

function canonicalCandidate(file) {
	try {
		return realpathSync.native(file);
	} catch (error) {
		if (!isMissing(error)) throw error;
		try {
			return path.join(realpathSync.native(path.dirname(file)), path.basename(file));
		} catch (parentError) {
			if (isMissing(parentError)) return undefined;
			throw parentError;
		}
	}
}

function classifyManagedLocation(config, file) {
	const lexical = locationCategory(config.managedRoot, config.ompDir, file);
	if (lexical === "other") {
		throw new Error(`OMP session belongs to another Agent Deck entry or nested task: ${file}`);
	}
	let canonical;
	try {
		canonical = canonicalCandidate(file);
	} catch (error) {
		throw new Error(`cannot resolve OMP session path ${file}: ${describeError(error)}`);
	}
	if (canonical !== undefined) {
		const physical = locationCategory(config.realManagedRoot, config.realOmpDir, canonical);
		if (physical === "other" || physical !== lexical) {
			throw new Error(`OMP session path crosses a symlink into another Agent Deck entry or nested task: ${file}`);
		}
	}
	return lexical;
}

function assertPublishableIdentity(config, file, id) {
	const location = classifyManagedLocation(config, file);
	if (location === "external") {
		const previous = readOwnBinding(config);
		// /move also relocates OMP's session directory. Subsequent native
		// new/fork/branch IDs belong there; explicit foreign resume is still
		// rejected by beforeSwitch. Manager truth, not an arbitrary file scan,
		// is the authority for these newly-created identities.
		const previousDirectory = previous && canonicalCandidate(path.dirname(previous.file));
		const sameMovedDirectory = previousDirectory !== undefined && previous && classifyManagedLocation(config, previous.file) === "external" &&
			previousDirectory === canonicalCandidate(path.dirname(file));
		if (!previous || (previous.id !== id && !sameMovedDirectory)) {
			throw new Error(`OMP external relocation must preserve the active session ID ${id}`);
		}
	}
	assertNoSiblingClaim(config, file, id);
}

function currentIdentity(ctx) {
	const manager = ctx.sessionManager;
	const fileValue = validateSingleLine(manager.getSessionFile(), "session path", 8192);
	const id = validateSingleLine(manager.getSessionId(), "session ID");
	if (!path.isAbsolute(fileValue) || path.extname(fileValue) !== ".jsonl") {
		throw new Error(`invalid OMP session path: ${fileValue}`);
	}
	// These are intentionally queried only from the provider manager. The name
	// is not imported: Agent Deck's explicit title-intent file is authoritative.
	manager.getSessionName();
	manager.getCwd();
	return { file: fileValue, id };
}

function materializationState(config, identity) {
	let metadata;
	try {
		metadata = lstatSync(identity.file);
	} catch (error) {
		if (isMissing(error)) {
			const previous = readOwnBinding(config);
			if (previous?.id === identity.id && previous.state === "saved") {
				throw new Error(`OMP saved transcript is missing: ${identity.file}; previous binding and history are preserved`);
			}
			return "pending";
		}
		throw new Error(`cannot inspect OMP session file: ${describeError(error)}`);
	}
	if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error(`OMP session is not a regular transcript: ${identity.file}`);
	const headerId = readHeaderSessionId(identity.file);
	if (headerId !== identity.id) {
		throw new Error(`OMP session ID mismatch at ${identity.file}: expected ${identity.id}, found ${headerId}`);
	}
	return "saved";
}

function statusPayload(config, identity, identityReady, error) {
	return {
		instance_id: config.instanceId,
		launch_id: config.launchId,
		pid: process.pid,
		session_id: identity?.id ?? "",
		session_file: identity?.file ?? "",
		identity_ready: identityReady,
		error: error ?? null,
		updated_at: new Date().toISOString(),
	};
}

function publishStatus(config, identity, identityReady, error) {
	const desired = statusPayload(config, identity, identityReady, error);
	try {
		const existingContents = readSmallRegularFile(config.statusFile, "OMP status file");
		if (existingContents !== undefined) {
			const existing = JSON.parse(existingContents);
			const unchanged = Object.entries(desired).every(
				([key, value]) => key === "updated_at" || existing?.[key] === value,
			);
			if (unchanged) return;
		}
	} catch {
		// A malformed status record is replaced by the validated current state.
	}
	atomicWrite(config.statusFile, `${JSON.stringify(desired)}\n`, () => assertAuthorized(config));
}

function clearCurrentFreshMarker(config) {
	const marker = readSmallRegularFile(config.freshPendingFile, "OMP fresh-pending marker");
	if (marker === undefined) return;
	const generation = marker.endsWith("\n") ? marker.slice(0, -1) : marker;
	if (generation === config.launchId) {
		assertAuthorized(config);
		unlinkSync(config.freshPendingFile);
	}
}

function publishIdentity(config, ctx) {
	const identity = currentIdentity(ctx);
	assertPublishableIdentity(config, identity.file, identity.id);
	const state = materializationState(config, identity);
	const contents = `1\n${identity.file}\n${identity.id}\n${state}\n${config.launchId}\n`;
	try {
		let unchanged = false;
		try {
			unchanged = readSmallRegularFile(config.bindingFile, "OMP active-session binding") === contents;
		} catch {
			// A malformed local binding is safely replaced by manager truth.
		}
		if (!unchanged) atomicWrite(config.bindingFile, contents, () => assertAuthorized(config));
	} catch (error) {
		throw new Error(`cannot publish OMP active session binding: ${describeError(error)}`);
	}
	clearCurrentFreshMarker(config);
	publishStatus(config, identity, true, null);
	return identity;
}

function readTitleIntent(config) {
	const contents = readSmallRegularFile(config.titleFile, "OMP title-intent file");
	if (contents === undefined) return undefined;
	let parsed;
	try {
		parsed = JSON.parse(contents);
	} catch (error) {
		throw new Error(`invalid OMP title-intent file: ${describeError(error)}`);
	}
	if (!parsed || typeof parsed !== "object" || typeof parsed.title !== "string" || parsed.title.length === 0) {
		throw new Error("invalid OMP title-intent file: title must be a non-empty string");
	}
	if (parsed.title.includes("\0")) throw new Error("invalid OMP title-intent file: title contains NUL");
	return { contents, title: parsed.title };
}

export default function identityExtension(pi) {
	let config;
	let rootClaimed = false;
	let timerRegistered = false;
	let appliedTitleContents;
	let lastNotification;

	function notify(ctx, message, type = "error") {
		if (message === lastNotification) return;
		lastNotification = message;
		try {
			ctx.ui.notify(message, type);
		} catch {}
	}

	function durableError(ctx, error, identity) {
		let message = `Agent Deck OMP identity: ${describeError(error)}`;
		if (config && rootClaimed) {
			try {
				assertAuthorized(config);
				// Awaitable title hooks may finish after a later switch already
				// published another identity. Never let the older completion replace
				// that newer status record.
				if (identity) {
					const live = currentIdentity(ctx);
					if (live.file !== identity.file || live.id !== identity.id) {
						notify(ctx, message);
						return;
					}
					try {
						assertPublishableIdentity(config, identity.file, identity.id);
						materializationState(config, identity);
					} catch (currentError) {
						message += `; current identity unavailable: ${describeError(currentError)}`;
						identity = undefined;
					}
				}
				// A title-only failure occurs after publishIdentity returned. Preserve
				// that valid identity ACK while surfacing the independent warning.
				publishStatus(config, identity, identity !== undefined, message);
			} catch (statusError) {
				if (!/stale|ownership was lost/.test(describeError(statusError))) {
					notify(ctx, `${message}; status could not be persisted: ${describeError(statusError)}`);
					return;
				}
			}
		}
		notify(ctx, message);
	}

	async function reconcile(ctx, forceTitle = false) {
		if (!isTuiRootContext(ctx) || !rootClaimed) return;
		let identity;
		try {
			assertAuthorized(config);
			identity = publishIdentity(config, ctx);
			const titleIntent = readTitleIntent(config);
			if (titleIntent && (forceTitle || titleIntent.contents !== appliedTitleContents)) {
				await pi.setSessionName(titleIntent.title);
				appliedTitleContents = titleIntent.contents;
			}
			if (!titleIntent) appliedTitleContents = undefined;
			lastNotification = undefined;
			return identity;
		} catch (error) {
			durableError(ctx, error, identity);
			return identity;
		}
	}

	async function start(_event, ctx) {
		if (!isTuiRootContext(ctx)) return;
		if (!rootClaimed) {
			try {
				config = loadConfiguration();
				claimRoot(config);
				rootClaimed = true;
			} catch (error) {
				notify(ctx, `Agent Deck OMP identity: ${describeError(error)}`);
				return;
			}
		}
		const identity = await reconcile(ctx, true);
		if (!timerRegistered) {
			try {
				ctx.setInterval(async () => {
					if (!isTuiRootContext(ctx)) return;
					await reconcile(ctx);
				}, 1000);
				timerRegistered = true;
			} catch (error) {
				durableError(ctx, error, identity);
			}
		}
	}

	async function lifecycle(_event, ctx) {
		if (!isTuiRootContext(ctx)) return;
		await reconcile(ctx);
	}

	async function switched(_event, ctx) {
		if (!isTuiRootContext(ctx)) return;
		await reconcile(ctx, true);
	}

	async function beforeSwitch(event, ctx) {
		if (!isTuiRootContext(ctx) || !rootClaimed) return undefined;
		try {
			assertAuthorized(config);
			if (event.type === "session_before_branch" || event.reason === "new" || event.reason === "fork") {
				// /move emits no event. Capture its manager-owned path before a
				// following transition can replace the UUID ahead of our timer.
				const current = currentIdentity(ctx);
				const previous = readOwnBinding(config);
				if (previous?.file !== current.file || previous.id !== current.id) publishIdentity(config, ctx);
				return undefined;
			}
			if (event.reason !== "resume") throw new Error(`unsupported session switch reason: ${event.reason}`);
			const targetValue = validateSingleLine(event.targetSessionFile, "switch target", 8192);
			if (!path.isAbsolute(targetValue) || path.extname(targetValue) !== ".jsonl") {
				throw new Error(`invalid OMP switch target: ${targetValue}`);
			}
			const target = path.resolve(targetValue);
			const targetId = readHeaderSessionId(target);
			const location = classifyManagedLocation(config, target);
			if (location === "external" && targetId !== validateSingleLine(ctx.sessionManager.getSessionId(), "session ID")) {
				throw new Error("OMP external resume target is not a relocation of the active session ID");
			}
			assertNoSiblingClaim(config, target, targetId);
			return undefined;
		} catch (error) {
			let identity;
			try {
				const current = currentIdentity(ctx);
				const published = readOwnBinding(config);
				if (published?.generation === config.launchId && published.file === current.file && published.id === current.id) {
					assertPublishableIdentity(config, current.file, current.id);
					materializationState(config, current);
					identity = current;
				}
			} catch {}
			durableError(ctx, new Error(`session switch rejected: ${describeError(error)}`), identity);
			return { cancel: true };
		}
	}

	pi.on("session_start", start);
	pi.on("session_before_switch", beforeSwitch);
	pi.on("session_before_branch", beforeSwitch);
	pi.on("session_switch", switched);
	pi.on("session_branch", lifecycle);
	pi.on("agent_end", lifecycle);
	pi.on("session_shutdown", lifecycle);
}
