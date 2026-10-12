/**
 * omarchy-guard: safety and stability for Pi on this machine. Built from Pi's official examples
 * (permission-gate, protected-paths, notify) plus guards for what local models got wrong here.
 *
 * - Dangerous bash (sudo, rm -r, force push, reset --hard, dd/mkfs, curl | sh, pacman -R, poweroff...): asks first;
 *   blocked when there is no UI (pi -p).
 * - Secrets and private notes (vault Privado/, ~/.ssh, ~/.gnupg, restic password, Pi auth): never read or written.
 *   .env files: not written.
 * - Writes outside the project folder (and outside the knowledge base ~/.agents/kb and /tmp/pi-*): asks first;
 *   blocked without UI. (Qwen once wrote a requested module to /tmp/rev instead of the current folder.)
 * - Loops: the same tool call with the same arguments 3 times in a row is blocked with a hint to change approach.
 * - A desktop notification when a run that took more than 30 s finishes.
 *
 * Source: contrib/omarchy/pi/extensions/ (installed by contrib/omarchy/pi/install.sh).
 */

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { execFile } from "node:child_process";
import { homedir } from "node:os";
import { isAbsolute, relative, resolve } from "node:path";

const HOME = homedir();

export const DANGEROUS: [RegExp, string][] = [
	[/\b(sudo|doas|pkexec|run0)\b/, "runs as root"],
	[/\brm\s+(-[a-zA-Z]*[rR][a-zA-Z]*|--recursive)\b/, "recursive delete"],
	[/\bgit\s+push\b.*(\s-f\b|--force)/, "force push"],
	[/\bgit\s+reset\s+--hard\b/, "discards uncommitted work"],
	[/\bgit\s+clean\s+-[a-zA-Z]*f/, "deletes untracked files"],
	[/\bgit\s+(checkout|restore)\s+(--\s+)?\.(\s|$)/, "discards uncommitted work"],
	[/\b(dd\s+if=|mkfs(\.\w+)?\b|wipefs\b|fdisk\b|parted\b|sgdisk\b)/, "writes disks"],
	[/>\s*\/dev\/(sd|nvme|vd|mmcblk)/, "writes a disk device"],
	[/\b(chmod|chown)\s+(-R\s+)?\S*\s*(777|\/\s*$)/, "broad permission change"],
	[/\b(curl|wget)\b[^|]*\|\s*(sudo\s+)?(ba|z|fi)?sh\b/, "pipes a download into a shell"],
	[/\b(pacman|yay|paru)\s+-R|\bomarchy\s+pkg\s+(drop|remove)\b/, "uninstalls packages"],
	[/\bsystemctl\s+(--user\s+)?(stop|disable|mask|kill)\b/, "stops a service"],
	[/\b(shutdown|reboot|poweroff|halt)\b|\bsystemctl\s+(suspend|hibernate|reboot|poweroff)\b/, "power action"],
	[/\bkill\s+-9\s+-1\b|:\(\)\s*\{/, "kills everything"],
	[/\bdocker\s+(system|volume|image)\s+prune\b|\bdocker\s+rm\s+-f\b/, "deletes Docker data"],
	[/\b(drop\s+(database|table)|truncate\s+table)\b/i, "deletes database data"],
];

// never read or written, by any tool
export const SECRET = [
	/\/Obsidian Vault\/Privado(\/|$)/,
	/(^|\/)\.ssh\//,
	/(^|\/)\.gnupg\//,
	/restic-password/,
	/omarchy-backup\/\.env/,
	/\.pi\/agent\/auth\.json/,
	/\.config\/omarchy-google\/client\.json/,
];

export function dangerous(cmd: string): string | undefined {
	for (const [re, why] of DANGEROUS) if (re.test(cmd)) return why;
	return undefined;
}

export function secret(text: string): boolean {
	const t = text.replace(/^~(?=\/)/, HOME).replace(/\$HOME|\$\{HOME\}/g, HOME);
	return SECRET.some((re) => re.test(t));
}

// write allowed without asking: inside the project, the knowledge base, or Pi's own temp folders
export function insideAllowed(path: string, cwd: string): boolean {
	const abs = resolve(cwd, path.replace(/^~(?=\/|$)/, HOME));
	const inside = (base: string) => {
		const rel = relative(base, abs);
		return rel === "" || (!rel.startsWith("..") && !isAbsolute(rel));
	};
	return (
		(cwd !== HOME && cwd !== "/" && inside(cwd)) ||
		inside(resolve(HOME, ".agents/kb")) ||
		inside(resolve(HOME, "Documents/Obsidian Vault/Agentes")) ||
		/^\/tmp\/pi-/.test(abs)
	);
}

const HARMLESS = /^\/(dev\/(null|stdout|stderr|tty|fd\/)|proc\/self\/)/;
const unquote = (t: string) => t.replace(/^['"]|['"]$/g, "");

// paths a bash command writes to: redirect targets plus the arguments of write commands, per segment
export function bashWriteTargets(cmd: string): string[] {
	const out: string[] = [];
	for (const m of cmd.matchAll(/(?:^|[^<>&0-9])[12&]?>>?\|?\s*([^\s;|&<>]+)/g)) out.push(unquote(m[1]));
	for (const seg of cmd.split(/;|&&|\|\||\||\n/)) {
		const words = seg.trim().split(/\s+/).map(unquote).filter(Boolean);
		while (words.length && (/^\w+=/.test(words[0]) || ["sudo", "doas", "env", "command", "nohup", "time"].includes(words[0]))) words.shift();
		const [verb, ...rest] = words;
		const args = rest.filter((w) => !w.startsWith("-") && !/^[<>]/.test(w));
		if (!verb) continue;
		if (["mkdir", "touch", "rm", "rmdir", "ln", "chmod", "chown", "truncate", "tee", "shred", "unlink"].includes(verb)) out.push(...args);
		else if (["cp", "mv", "install", "rsync", "scp"].includes(verb) && args.length) out.push(args[args.length - 1]);
		else if (verb === "sed" && rest.some((w) => /^-[a-zA-Z]*i/.test(w)) && args.length) out.push(args[args.length - 1]);
	}
	return out.filter((t) => /^[~\/.]|\//.test(t) || !t.includes("$")).filter((t) => !HARMLESS.test(t));
}

export function bashWritesOutside(cmd: string, cwd: string): string | undefined {
	return bashWriteTargets(cmd).find((t) => !insideAllowed(t, cwd));
}

export default function (pi: ExtensionAPI) {
	let recent: string[] = [];
	let started = 0;

	pi.on("before_agent_start", async () => {
		recent = [];
		started = Date.now();
		return undefined;
	});

	pi.on("tool_call", async (event, ctx) => {
		const input = (event.input || {}) as Record<string, unknown>;
		const cwd = ctx.cwd || process.cwd();
		const ask = async (question: string, reason: string) => {
			if (!ctx.hasUI) return { block: true, reason: `${reason} (blocked: no one to confirm in this mode)` };
			const choice = await ctx.ui.select(question, ["No", "Yes, allow it"]);
			return choice === "Yes, allow it" ? undefined : { block: true, reason: `${reason}: the user said no` };
		};

		// loops: identical call 3 times in a row
		const key = event.toolName + " " + JSON.stringify(input);
		recent.push(key);
		if (recent.length > 3) recent.shift();
		if (recent.length === 3 && recent.every((k) => k === key)) {
			recent = [];
			return {
				block: true,
				reason: "You made this exact call 3 times in a row with the same result. Do not repeat it: change approach, use what you already have, or answer the user.",
			};
		}

		const pathArg = typeof input.path === "string" ? input.path : typeof input.file_path === "string" ? input.file_path : undefined;
		if (pathArg && secret(resolve(cwd, pathArg.replace(/^~(?=\/|$)/, HOME)))) {
			return { block: true, reason: `${pathArg} holds secrets or private notes: never read or write it` };
		}

		if (event.toolName === "bash" && typeof input.command === "string") {
			const cmd = input.command;
			if (secret(cmd)) return { block: true, reason: "the command touches secrets or private notes (vault Privado/, ~/.ssh, keys): not allowed" };
			const why = dangerous(cmd);
			if (why) return ask(`⚠️  Pi wants to run a command that ${why}:\n\n  ${cmd}\n\nin ${cwd}\n\nAllow it?`, `Dangerous command (${why}). Do not work around this with another command`);
			const out = bashWritesOutside(cmd, cwd);
			if (out) return ask(`📁  Pi wants a command to write OUTSIDE the project folder (${out}):\n\n  ${cmd}\n\nproject: ${cwd}\n\nAllow it?`,
				`The command writes outside the project folder ${cwd} (${out}). Write inside it unless the user asked for another path; do not work around this with another tool`);
		}

		if ((event.toolName === "write" || event.toolName === "edit") && pathArg) {
			if (/(^|\/)\.env(\.|$)/.test(pathArg)) return { block: true, reason: `${pathArg} is an environment/secret file: not written` };
			if (!insideAllowed(pathArg, cwd)) {
				return ask(
					`📁  Pi wants to write OUTSIDE the project folder:\n\n  ${resolve(cwd, pathArg)}\n\nproject: ${cwd}\n\nAllow it?`,
					`Write outside the project folder ${cwd}; write inside it unless the user asked for another path, and do not work around this with bash`,
				);
			}
		}
		return undefined;
	});

	pi.on("agent_settled", async (_event, ctx) => {
		if (!ctx.hasUI || !started || Date.now() - started < 30000) return;
		execFile("notify-send", ["-a", "Pi", "-i", "dialog-information", "Pi", "Terminó: esperando tu respuesta"], () => undefined);
	});
}
