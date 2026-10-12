/**
 * omarchy-web: two small web tools for local models (~300 prompt tokens, vs ~3.2k for pi-web-access).
 *
 * - web_search: the private SearXNG at 127.0.0.1:8888 (searxng.service, started on demand). No API keys,
 *   no third-party search account, nothing sent to a cloud model.
 * - web_fetch: one http(s) page or PDF as plain text, truncated; the full text is saved under /tmp/pi-web/.
 *
 * Source: contrib/omarchy/pi/extensions/ (installed by contrib/omarchy/pi/install.sh).
 */

import { Type } from "@earendil-works/pi-ai";
import { defineTool, type ExtensionAPI } from "@earendil-works/pi-coding-agent";
import { execFile } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import { promisify } from "node:util";

const run = promisify(execFile);
const SEARX = process.env.PI_SEARXNG_URL || "http://127.0.0.1:8888";
const UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0 Safari/537.36";
const OUT_DIR = "/tmp/pi-web";

async function searxUp(signal?: AbortSignal): Promise<boolean> {
	const ping = async () => {
		try {
			const r = await fetch(`${SEARX}/healthz`, { signal: AbortSignal.timeout(2000) });
			return r.ok;
		} catch {
			return false;
		}
	};
	if (await ping()) return true;
	await run("systemctl", ["--user", "start", "searxng.service"]).catch(() => undefined);
	for (let i = 0; i < 30 && !signal?.aborted; i++) {
		await new Promise((r) => setTimeout(r, 1000));
		if (await ping()) return true;
	}
	return false;
}

const decode = (s: string) =>
	s
		.replace(/&nbsp;/g, " ")
		.replace(/&amp;/g, "&")
		.replace(/&lt;/g, "<")
		.replace(/&gt;/g, ">")
		.replace(/&quot;/g, '"')
		.replace(/&#39;|&apos;/g, "'")
		.replace(/&#(\d+);/g, (_, n) => String.fromCodePoint(Number(n)))
		.replace(/&#x([0-9a-f]+);/gi, (_, n) => String.fromCodePoint(parseInt(n, 16)));

// HTML -> readable text: drop scripts, styles and page chrome, keep headings, list items and paragraphs.
export function htmlToText(html: string): { title: string; text: string } {
	const title = decode((html.match(/<title[^>]*>([\s\S]*?)<\/title>/i)?.[1] || "").trim());
	let body = html.match(/<main[\s\S]*?<\/main>/i)?.[0] || html.match(/<article[\s\S]*?<\/article>/i)?.[0] || html;
	body = body
		.replace(/<(script|style|noscript|svg|nav|footer|header|aside|form|iframe)[\s\S]*?<\/\1>/gi, " ")
		.replace(/<!--[\s\S]*?-->/g, " ")
		.replace(/<h([1-6])[^>]*>/gi, (_, n) => "\n\n" + "#".repeat(Number(n)) + " ")
		.replace(/<li[^>]*>/gi, "\n- ")
		.replace(/<(br|\/p|\/div|\/h[1-6]|\/tr|\/li|\/pre)[^>]*>/gi, "\n")
		.replace(/<[^>]+>/g, " ");
	const text = decode(body)
		.replace(/[ \t\f\v\r]+/g, " ")
		.replace(/ *\n */g, "\n")
		.replace(/\n{3,}/g, "\n\n")
		.trim();
	return { title, text };
}

function blockedHost(u: URL): string | undefined {
	if (u.protocol !== "http:" && u.protocol !== "https:") return "only http(s) URLs";
	const h = u.hostname.toLowerCase();
	if (h === "metadata.google.internal" || h.startsWith("169.254.")) return "cloud metadata address";
	return undefined;
}

const webSearch = defineTool({
	name: "web_search",
	label: "Web search",
	description:
		"Search the web (private SearXNG). Returns titles, URLs and snippets; read a result with web_fetch. category: general (default), it, science or news.",
	parameters: Type.Object({
		query: Type.String({ description: "Search terms" }),
		max_results: Type.Optional(Type.Number({ description: "1-10, default 5" })),
		category: Type.Optional(Type.String({ description: "general | it | science | news" })),
	}),
	annotations: { readOnlyHint: true, openWorldHint: true },
	async execute(_id, params, signal) {
		if (!(await searxUp(signal))) throw new Error("SearXNG is not running (systemctl --user start searxng)");
		const n = Math.min(10, Math.max(1, Math.round(params.max_results ?? 5)));
		const cat = ["general", "it", "science", "news"].includes(params.category ?? "") ? params.category : "general";
		const url = `${SEARX}/search?format=json&categories=${cat}&q=${encodeURIComponent(params.query)}`;
		const r = await fetch(url, { signal: signal ?? AbortSignal.timeout(20000) });
		if (!r.ok) throw new Error(`SearXNG answered ${r.status}`);
		const data = (await r.json()) as { results?: { title?: string; url?: string; content?: string; publishedDate?: string }[] };
		const seen = new Set<string>();
		const results = (data.results || []).filter((x) => x.url && !seen.has(x.url) && seen.add(x.url)).slice(0, n);
		if (results.length === 0) return { content: [{ type: "text", text: `No results for "${params.query}".` }], details: { count: 0 } };
		const text = results
			.map((x, i) => {
				const snippet = (x.content || "").replace(/\s+/g, " ").trim().slice(0, 220);
				const date = x.publishedDate ? ` (${x.publishedDate.slice(0, 10)})` : "";
				return `${i + 1}. ${x.title || x.url}${date}\n   ${x.url}${snippet ? `\n   ${snippet}` : ""}`;
			})
			.join("\n");
		return { content: [{ type: "text", text }], details: { count: results.length, query: params.query } };
	},
});

const webFetch = defineTool({
	name: "web_fetch",
	label: "Fetch page",
	description:
		"Read one web page or PDF (http/https) as plain text. Long pages are truncated; the full text is saved to a file you can read.",
	parameters: Type.Object({
		url: Type.String({ description: "http(s) URL" }),
		max_chars: Type.Optional(Type.Number({ description: "default 6000, max 20000" })),
	}),
	annotations: { readOnlyHint: true, openWorldHint: true },
	async execute(_id, params, signal) {
		let u: URL;
		try {
			u = new URL(params.url);
		} catch {
			throw new Error(`not a URL: ${params.url}`);
		}
		const why = blockedHost(u);
		if (why) throw new Error(`refused: ${why}`);
		const max = Math.min(20000, Math.max(500, Math.round(params.max_chars ?? 6000)));
		const r = await fetch(u, {
			headers: { "user-agent": UA, accept: "text/html,application/xhtml+xml,application/pdf,text/plain;q=0.9,*/*;q=0.5" },
			redirect: "follow",
			signal: signal ?? AbortSignal.timeout(25000),
		});
		if (!r.ok) throw new Error(`${r.status} ${r.statusText} for ${u}`);
		const type = r.headers.get("content-type") || "";
		mkdirSync(OUT_DIR, { recursive: true });
		const file = `${OUT_DIR}/${createHash("sha1").update(u.href).digest("hex").slice(0, 12)}`;
		let title = "";
		let text: string;
		if (type.includes("pdf") || u.pathname.toLowerCase().endsWith(".pdf")) {
			writeFileSync(`${file}.pdf`, Buffer.from(await r.arrayBuffer()));
			const { stdout } = await run("pdftotext", ["-layout", "-enc", "UTF-8", `${file}.pdf`, "-"], { maxBuffer: 64 << 20 }).catch(() => ({
				stdout: "",
			}));
			text = stdout.trim() || "(PDF without extractable text)";
		} else {
			const raw = await r.text();
			if (type.includes("html") || /^\s*<(!doctype|html)/i.test(raw)) ({ title, text } = htmlToText(raw));
			else text = raw;
		}
		writeFileSync(`${file}.txt`, text);
		const cut = text.length > max;
		const head = `${title ? `# ${title}\n` : ""}${r.url}\n\n`;
		const tail = cut ? `\n\n[truncated: ${max} of ${text.length} chars; full text in ${file}.txt]` : "";
		return { content: [{ type: "text", text: head + text.slice(0, max) + tail }], details: { url: r.url, chars: text.length, file: `${file}.txt` } };
	},
});

export default function (pi: ExtensionAPI) {
	pi.registerTool(webSearch);
	pi.registerTool(webFetch);
}
