/**
 * One log row, and the strip of "this is OFF right now" targets above the table.
 *
 * Split from LogPanel.tsx (filters + table shell) so each file is one idea: this
 * one is what a ROW renders and what a row's off switch aims at.
 */
import type { ActionRecord, OffTarget } from './log'

export function OffStrip({ targets, onRestore }: { targets: OffTarget[]; onRestore: (t: OffTarget) => void }) {
  if (targets.length === 0) {
    return <p className="muted">nothing is off — every connection and action is live</p>
  }
  return (
    <p className="note warn">
      OFF right now:{' '}
      {targets.map((t) => (
        <button key={`${t.kind}:${t.id}`} className="chip" onClick={() => void onRestore(t)}>
          {t.kind} {t.id} — turn back on
        </button>
      ))}
    </p>
  )
}

export function Row({
  rec,
  targets,
  onTurnOff,
}: {
  rec: ActionRecord
  targets: OffTarget[]
  onTurnOff: (t: { kind: string; id: string }) => void
}) {
  const connectionOff = rec.device ? targets.some((t) => t.kind === 'connection' && t.id === rec.device) : false
  const actionOff = rec.inputAction ? targets.some((t) => t.kind === 'action' && t.id === rec.inputAction) : false
  return (
    <tr className={`outcome-${rec.outcome}`}>
      <td title={rec.at}>{rec.at.slice(11, 19)}</td>
      <td>{rec.source}</td>
      <td>
        <span className={`pill ${rec.outcome}`}>{rec.outcome}</span>
      </td>
      <td>{rec.reason ?? '—'}</td>
      <td>
        <code>{rec.inputAction || '—'}</code> → {rec.outputActions.length ? rec.outputActions.join(', ') : '—'}
      </td>
      <td>
        {rec.device ? (
          <button className="chip" disabled={connectionOff} onClick={() => onTurnOff({ kind: 'connection', id: rec.device! })}>
            {connectionOff ? `${rec.device} (off)` : `turn ${rec.device} off`}
          </button>
        ) : (
          '—'
        )}
      </td>
      <td>
        {rec.inputAction ? (
          <button className="chip" disabled={actionOff} onClick={() => onTurnOff({ kind: 'action', id: rec.inputAction! })}>
            {actionOff ? `${rec.inputAction} (off)` : `turn ${rec.inputAction} off`}
          </button>
        ) : (
          '—'
        )}
      </td>
    </tr>
  )
}

/** effectOf states what a flip does, so the page never leaves a reader guessing
 *  whether "off" hid a row or revoked a capability. */
export function effectOf(kind: string): string {
  return kind === 'connection'
    ? "that device's evaluations are refused before the engine is called, and its pending fires too"
    : "that command's emissions are suppressed; every other command keeps working"
}
