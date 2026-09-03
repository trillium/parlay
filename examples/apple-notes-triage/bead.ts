// bead.ts — runs a store's createCommand template to file a bead LIVE.
//
// `{title}` is substituted into the command string; the body (which always
// carries provenance — note id + ISO date, per discussion #244) is piped
// over stdin so it never has to survive shell-argument escaping. The
// resulting bead id is pulled out of stdout with a per-store regex (default
// matches the `task`-style "store-xxxxx" shape from `task create`, e.g.
// "Created issue: task-yf18z").

import type { StoreConfig } from './types.ts';

export const DEFAULT_BEAD_ID_PATTERN = '\\b[a-z][a-z0-9_]*-[a-z0-9]{4,8}\\b';

export interface BeadResult {
  ok: boolean;
  beadId: string | null;
  stdout: string;
  stderr: string;
}

function substituteTitle(template: string, title: string): string {
  return template.split('{title}').join(title);
}

export function extractBeadId(stdout: string, patternSource = DEFAULT_BEAD_ID_PATTERN): string | null {
  const re = new RegExp(patternSource, 'i');
  const m = re.exec(stdout);
  return m ? m[0] : null;
}

export async function createBead(config: StoreConfig, title: string, body: string): Promise<BeadResult> {
  const command = substituteTitle(config.createCommand, title);
  const proc = Bun.spawn(['bash', '-c', command], {
    stdin: 'pipe',
    stdout: 'pipe',
    stderr: 'pipe',
    // See notes-io.ts's runOsa for why this must be explicit.
    env: process.env,
  });
  proc.stdin.write(body);
  await proc.stdin.end();

  const [stdout, stderr, exitCode] = await Promise.all([
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
    proc.exited,
  ]);

  const ok = exitCode === 0;
  const beadId = ok ? extractBeadId(stdout, config.idPattern) : null;
  return { ok, beadId, stdout, stderr };
}
