/**
 * sigoREST-Provider für pi
 *
 * Registriert den lokalen sigoREST-Server als pi-Provider. Nutzt den
 * Anthropic-Endpoint /v1/messages, da dieser - anders als
 * /v1/chat/completions - KEINE Memory-/System-Prompt-/Session-Injektion
 * macht (pi verwaltet seinen eigenen Kontext).
 *
 * Modelle werden beim Start dynamisch von GET /api/models geladen
 * (Shortcode als Modell-ID, echte Token-Limits und Preise).
 *
 * Fallback: sigoREST selbst routet zwischen Kanälen/Providern; fällt der
 * komplette Server aus, bleibt der eingebaute ZAI-Provider (/model zai/...).
 *
 * Konfiguration: SIGOREST_URL (Default http://localhost:9080).
 */
import type { ExtensionAPI, ProviderConfig } from "@earendil-works/pi-coding-agent";

const BASE_URL = process.env.SIGOREST_URL ?? "http://localhost:9080";

/** Modell-Eintrag aus GET /api/models (sigoREST README). */
interface SigoModelInfo {
	id: string;
	shortcode: string;
	provider: string;
	upstream_id?: string;
	max_input_tokens: number;
	max_output_tokens: number;
	input_cost: number; // $/1M Tokens
	output_cost: number; // $/1M Tokens
}

/**
 * Shortcodes von Modellen mit Reasoning-/Thinking-Fähigkeit, gemappt auf
 * das maxTokens-Limit aus pi's Modellkatalog (gleiche Quelle wie das
 * Matching). sigoRESTs /api/models meldet für viele Modelle nur einen
 * konservativen Default (z.B. 8192) - pi klemmt aber das Thinking-Budget
 * auf maxTokens-1024. Mit den Katalogwerten kann pi das volle Budget
 * senden und sigoREST mappt es korrekt auf reasoning_effort.
 *
 * Abgeleitet durch exaktes Matching der sigoREST-Modell-IDs (upstream_id/id)
 * gegen pi's Modellkatalog (nur Modelle mit reasoning: true übernommen).
 * Neue sigoREST-Modelle müssen hier manuell nachgetragen werden; alle
 * nicht gelisteten Shortcodes laufen ohne Thinking.
 */
const THINKING_MODELS: Record<string, number> = {
	"che-a33": 32768, "che-cl-f025": 128000, "che-cl-f025.1": 128000, "che-cl45-h": 64000,
	"che-cl45-o": 64000, "che-cl45-s": 64000, "che-cl46-o": 128000, "che-cl46-s": 64000,
	"che-cl47-o": 128000, "che-cl48-o": 128000, "che-cl48-ofst": 128000, "che-cl5-o": 128000,
	"che-cl5-ofst": 128000, "che-cl5-s": 128000, "che-cl55-o": 128000, "che-ds4-f": 384000,
	"che-ds4-f073": 384000, "che-ds4-p": 384000, "che-ds4-p081": 384000, "che-ds41-f": 384000,
	"che-g24": 32768, "che-g52": 65000, "che-gem25-f": 65536, "che-gem3-fpv": 65536,
	"che-gem31-flt": 65536, "che-gem31-p": 65536, "che-gem31-ppv": 65536, "che-gem35-f": 65536,
	"che-gem36-f": 65536, "che-gem37-f": 65536, "che-glm45": 98304, "che-glm45-a": 98304,
	"che-glm46": 131072, "che-glm47": 131072, "che-glm5": 131072, "che-glm51": 131072,
	"che-glm52": 164000, "che-glm53": 262144, "che-glm53-f": 400000, "che-gpt-o83120": 131072,
	"che-gpt5-m": 128000, "che-gpt5-n": 128000, "che-gpt52-cx": 128000, "che-gpt54": 128000,
	"che-gpt54-m": 128000, "che-gpt54-n": 128000, "che-gpt55": 128000, "che-gpt56-l88": 128000,
	"che-gpt56-s01": 128000, "che-gpt56-t21": 128000, "che-gpt6-a84": 128000, "che-gpt6-l88": 128000,
	"che-grok45": 500000, "che-kimik3": 131072, "che-m46": 131072, "che-m46-2": 131072,
	"che-m83": 131072, "che-mmxm25": 131072, "che-mmxm27": 131072, "che-qwen3535b-a3b": 65536,
	"che-qwen3627b": 262140, "che-qwen3635b-a3b": 65536, "che-qwen3827b": 32768, "lon-longcat2": 131072,
	"mam-cl-f025": 128000, "mam-cl-f025.1": 128000, "mam-cl4-s": 64000, "mam-cl45-h": 64000,
	"mam-cl45-o": 64000, "mam-cl45-s": 64000, "mam-cl46-o": 128000, "mam-cl46-s": 64000,
	"mam-cl47-o": 128000, "mam-cl48-o": 128000, "mam-cl5-o": 128000, "mam-cl5-ofst": 128000,
	"mam-cl5-s": 128000, "mam-cl55-o": 128000, "mam-cl55-ofst": 128000, "mam-ds31-term": 65536,
	"mam-ds32": 81920, "mam-ds4-f": 384000, "mam-ds4-f073": 384000, "mam-ds4-p": 384000,
	"mam-ds41-f": 384000, "mam-dsr10528": 163840, "mam-gem25-f": 65536, "mam-gem25-flt": 65536,
	"mam-gem25-p": 65536, "mam-gem3-fpv": 65536, "mam-gem31-fltimg": 65536, "mam-gem31-fltpv": 65536,
	"mam-gem31-ppv": 65536, "mam-gem31-ppvct": 65536, "mam-gem35-f": 65536, "mam-gem35-flt": 65536,
	"mam-gem36-f": 65536, "mam-gem37-f": 65536, "mam-gem38-f": 65536, "mam-gpt51": 128000,
	"mam-gpt51-cx": 128000, "mam-gpt51-cxm": 128000, "mam-gpt51-cxm01": 128000, "mam-gpt52": 128000,
	"mam-gpt52-cx": 128000, "mam-gpt53-cx": 128000, "mam-gpt54": 128000, "mam-gpt54-m": 128000,
	"mam-gpt54-n": 128000, "mam-gpt55": 128000, "mam-gpt56-l88": 128000, "mam-gpt56-s01": 128000,
	"mam-gpt56-t21": 128000, "mam-gpt6-a84": 128000, "mam-gpt6-l88": 128000, "mam-gpt6-s01": 128000,
	"mam-grok43": 900000, "mam-grok45": 500000, "mam-grok46": 500000, "mam-grok47": 500000,
	"mam-kimik25": 262144, "mam-kimik3-fst": 131072, "mam-mist-m3735": 262144, "mam-mist2603-s": 256000,
	"mam-mmxm27": 131072, "mam-mmxm27-hs": 131072, "mam-mmxm3": 250000, "mam-qwen3535b-a3b": 65536,
	"mam-qwen35397b-a17b": 32768, "mam-qwen359b": 65536, "mam-qwen37-m01": 131072, "mam-qwen37-pl": 65536,
	"mam-qwen38-f": 131072, "mam-qwen3827b": 131072, "moo-kimik26": 131000, "moo-kimik27-cod": 131072,
	"moo-kimik27-codhs": 262144, "moo-kimik3": 131072, "zai-glm45": 98304, "zai-glm45a": 98304,
	"zai-glm46": 131072, "zai-glm47": 131072, "zai-glm5": 131072, "zai-glm51": 131072,
	"zai-glm52": 164000, "zai-glm53": 262144, "zai-glm53-f": 400000, "zai-glm53-f32": 131072,
	"zai-glm5t": 131072,
};
export default async function (pi: ExtensionAPI) {
	let models: ProviderConfig["models"] = [];
	let warning: string | null = null;

	try {
		const res = await fetch(`${BASE_URL}/api/models`, {
			headers: { accept: "application/json" },
			signal: AbortSignal.timeout(5000),
		});
		if (!res.ok) {
			throw new Error(`HTTP ${res.status}`);
		}
		const infos: SigoModelInfo[] = await res.json();
		// Embedding-Modelle sind keine Chat-Modelle und würden in /v1/messages fehlschlagen.
		models = infos
			.filter((m) => !/embedding/i.test(m.id) && !/embedding/i.test(m.upstream_id ?? ""))
			.map((m) => {
				// Katalog-maxTokens für Thinking-Modelle: sigoREST meldet oft
				// nur einen konservativen Default (z.B. 8192), der pi's
				// Thinking-Budget auf maxTokens-1024 klemmen würde.
				let modelMaxTokens = THINKING_MODELS[m.shortcode] ?? m.max_output_tokens;
				// ZAI's API deckelt max_tokens auf 131072 (Fehlercode 1210) -
				// pi-Katalogwerte können höher liegen (z.B. GLM-5.3: 262144).
				if (m.provider === "zai") {
					modelMaxTokens = Math.min(modelMaxTokens, 131072);
				}
				return {
				type: "chat" as const,
				// Shortcode als ID: stabil (id_registry.db), keine Slashes
				// (sigoREST-Voll-IDs wie "ci-google/gemini-..." würden pi's
				// provider/model-Parsing stören).
				id: m.shortcode,
				name: m.upstream_id ?? m.id,
				api: "anthropic-messages" as const,
				reasoning: THINKING_MODELS[m.shortcode] !== undefined,
				input: ["text", "image"] as ("text" | "image")[],
				contextWindow: m.max_input_tokens,
				maxTokens: modelMaxTokens,
				cost: {
					input: m.input_cost,
					output: m.output_cost,
					// sigoREST liefert keine Cache-Preise; 0 = Cache-Tokens
					// werden in der Kostenanzeige nicht berücksichtigt.
					cacheRead: 0,
					cacheWrite: 0,
				},
				};
			});
	} catch (err) {
		warning = `sigoREST nicht erreichbar (${BASE_URL}): ${
			err instanceof Error ? err.message : String(err)
		} - Provider ohne Modelle registriert.`;
	}

	pi.registerProvider("sigorest", {
		name: "sigoREST",
		baseUrl: BASE_URL,
		api: "anthropic-messages",
		// sigoREST authentifiziert per IP-Zugriffskontrolle; der API-Key ist
		// ein Platzhalter. pi verlangt aber einen konfigurierten Key:
		// export SIGOREST_API_KEY=sigo
		apiKey: "$SIGOREST_API_KEY",
		models,
	});

	if (warning) {
		console.warn(warning);
	}
}
