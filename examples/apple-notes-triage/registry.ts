// registry.ts — loads the federated-store registry from local config.
//
// config.json (gitignored, personal) wins when present; config.example.json
// (committed, fake stores only) is the fallback so `--demo` and the test
// suite always have something to route against. See README.md for how to
// generate a real config.json.

import type { Registry } from './types.ts';

const DIR = new URL('.', import.meta.url).pathname;

export async function loadRegistry(path?: string): Promise<{ registry: Registry; source: string; live: boolean }> {
  const explicit = path ?? process.env.NOTES_TRIAGE_CONFIG;
  const candidates = explicit ? [explicit] : [`${DIR}config.json`, `${DIR}config.example.json`];

  for (const candidate of candidates) {
    const file = Bun.file(candidate);
    if (await file.exists()) {
      const registry = (await file.json()) as Registry;
      const live = candidate.endsWith('config.json') && !candidate.endsWith('config.example.json');
      return { registry, source: candidate, live };
    }
  }

  throw new Error(`no store registry found (looked at: ${candidates.join(', ')})`);
}

export function describeRegistry(registry: Registry): string {
  return Object.entries(registry)
    .map(([name, cfg]) => `- ${name}: ${cfg.purpose}`)
    .join('\n');
}
