import { useState, useEffect, useRef } from 'react'
import { useFleetStore } from './useFleetStore'
import type { Agent, Command, EvalHit, Message, ToolEvent } from './types'
import './App.css'

// ── utils ────────────────────────────────────────────────────────────────────

function relTime(ts: string): string {
  const diff = Date.now() - new Date(ts).getTime()
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`
  if (diff < 3_600_000) return `${Math.floor(diff / 60_000)}m ago`
  return `${Math.floor(diff / 3_600_000)}h ago`
}

function fmtDuration(ms: number): string {
  const s = Math.floor(ms / 1000)
  if (s < 60) return `${s}s`
  if (s < 3600) return `${Math.floor(s / 60)}m ${s % 60}s`
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
}

const TOOL_ICONS: Record<string, string> = {
  Bash: '⬢', Read: '📄', Edit: '✏️', Write: '💾', Agent: '🤖',
  WebFetch: '🌐', WebSearch: '🔍', Artifact: '🎨', Workflow: '⚙️',
}

// ── AgentList ────────────────────────────────────────────────────────────────

function AgentList({ agents, liveChannels, selected, onSelect }: {
  agents: Agent[]
  liveChannels: Set<string>
  selected: string | null
  onSelect: (id: string | null) => void
}) {
  return (
    <aside className="agent-list">
      <div className="panel-header">Agents <span className="badge">{agents.length}</span></div>
      <div
        className={`agent-item ${selected === null ? 'active' : ''}`}
        onClick={() => onSelect(null)}
      >
        <span className="agent-dot" style={{ background: '#6b7280' }} />
        <span className="agent-name">Fleet (all)</span>
      </div>
      {agents.map(a => {
        const live = liveChannels.has(a.id)
        return (
          <div
            key={a.id}
            className={`agent-item ${selected === a.id ? 'active' : ''}`}
            onClick={() => onSelect(a.id)}
          >
            <span className="agent-dot" style={{ background: live ? a.color : '#374151' }} />
            <span className="agent-name" title={a.id}>{a.name || a.id}</span>
            {live && <span className="live-pip" />}
          </div>
        )
      })}
    </aside>
  )
}

// ── CommandBar ───────────────────────────────────────────────────────────────

function CommandBar({ commands }: { commands: Command[] }) {
  const running = commands.filter(c => c.state === 'running')
  if (!running.length) return null
  return (
    <div className="command-bar">
      {running.map(c => (
        <span key={c.id} className="cmd-pill" title={`pid ${c.pid} · ${fmtDuration(c.durationMs)}`}>
          <span className="cmd-verb">{c.verb}</span>
          {c.agent && <span className="cmd-agent"> {c.agent}</span>}
          <span className="cmd-dur"> {fmtDuration(c.durationMs)}</span>
        </span>
      ))}
    </div>
  )
}

// ── MessageThread ─────────────────────────────────────────────────────────────

function MessageThread({ messages }: { messages: Message[] }) {
  const bottomRef = useRef<HTMLDivElement>(null)
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [messages.length])

  return (
    <div className="thread">
      {messages.map(m => (
        <div key={m.id} className={`msg msg-${m.type === 'system_update' ? 'system' : m.role}`}>
          <div className="msg-meta">
            <span className="msg-role">{m.from || m.source || m.role}</span>
            {m.channel && <span className="msg-channel"> #{m.channel}</span>}
            <span className="msg-ts">{relTime(m.ts)}</span>
          </div>
          <pre className="msg-text">{m.text}</pre>
        </div>
      ))}
      <div ref={bottomRef} />
    </div>
  )
}

// ── ToolLog ───────────────────────────────────────────────────────────────────

function ToolLog({ events, filterChannel }: { events: ToolEvent[]; filterChannel: string | null }) {
  const visible = filterChannel ? events.filter(e => !e.channel || e.channel === filterChannel) : events
  const bottomRef = useRef<HTMLDivElement>(null)
  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: 'smooth' }) }, [visible.length])

  return (
    <div className="tool-log">
      {visible.map((e, i) => (
        <div key={i} className="tl-entry">
          <span className="tl-icon">{TOOL_ICONS[e.tool] ?? '○'}</span>
          <span className="tl-tool">{e.tool}</span>
          <span className="tl-desc">{e.desc ?? ''}</span>
          {e.channel && <span className="tl-ch"> #{e.channel}</span>}
          <span className="tl-ts">{relTime(e.ts)}</span>
          {(e.cmd || e.out || e.err) && (
            <div className="tl-body">
              {e.cmd && <div className="tl-cmd">{e.cmd}</div>}
              {e.out && <div className="tl-out">{e.out}</div>}
              {e.err && <div className="tl-err">{e.err}</div>}
            </div>
          )}
        </div>
      ))}
      <div ref={bottomRef} />
    </div>
  )
}

// ── EvalLog ───────────────────────────────────────────────────────────────────

const ACTION_COLOR: Record<string, string> = {
  submitNow:          'var(--green)',
  setText:            'var(--accent)',
  replaceRange:       'var(--accent)',
  clear:              'var(--yellow)',
  stopSpeech:         'var(--yellow)',
  flagSpeech:         'var(--red)',
  switchTab:          'var(--text)',
  openChannelPicker:  'var(--text)',
  armTimer:           'var(--text2)',
  cancelTimer:        'var(--text2)',
  showHint:           'var(--text2)',
  clearHint:          'var(--text2)',
  noop:               'var(--border)',
}

function EvalLog({ hits, filter }: { hits: EvalHit[]; filter: string }) {
  const [selected, setSelected] = useState<EvalHit | null>(null)
  const q = filter.toLowerCase()
  const visible = q
    ? hits.filter(h =>
        h.actions.some(a => a.verb.toLowerCase().includes(q)) ||
        h.streamId.toLowerCase().includes(q)
      )
    : hits

  return (
    <div className="eval-wrap">
      <div className="eval-list">
        {visible.length === 0 && (
          <div style={{ padding: '24px 16px', color: 'var(--text2)' }}>
            {filter ? 'No matches for filter.' : 'Waiting for eval hits… speak a command.'}
          </div>
        )}
        {visible.map((h, i) => (
          <div
            key={i}
            className={`eval-row ${selected === h ? 'eval-row-selected' : ''}`}
            onClick={() => setSelected(selected === h ? null : h)}
          >
            <span className="eval-ts">{relTime(h.ts)}</span>
            <span className="eval-stream">{h.streamId.replace(/^eval-/, '').replace(/-main$/, '')}</span>
            <span className="eval-actions">
              {h.actions.map((a, j) => (
                <span key={j} className="eval-verb" style={{ color: ACTION_COLOR[a.verb] ?? 'var(--text)' }}>
                  {a.verb}
                  {a.args?.channel ? ` → ${a.args.channel}` : ''}
                  {a.args?.reason ? ` (${a.args.reason})` : ''}
                </span>
              ))}
            </span>
            {h.timing?.engineEvalNs != null && (
              <span className="eval-timing">{(h.timing.engineEvalNs / 1e6).toFixed(2)}ms</span>
            )}
            {h.serverOwned && <span className="eval-server-tag">server</span>}
          </div>
        ))}
      </div>
      {selected && (
        <div className="eval-drawer">
          <div className="eval-drawer-header">
            <span>Event detail</span>
            <button className="eval-drawer-close" onClick={() => setSelected(null)}>×</button>
          </div>
          <pre className="eval-drawer-body">{JSON.stringify(selected, null, 2)}</pre>
        </div>
      )}
    </div>
  )
}

// ── CommandTable ──────────────────────────────────────────────────────────────

const STATE_COLOR: Record<string, string> = {
  running: 'var(--green)',
  done: 'var(--text2)',
  failed: 'var(--red)',
}

function CommandTable({ commands, filterAgent }: { commands: Command[]; filterAgent: string | null }) {
  const visible = filterAgent ? commands.filter(c => !c.agent || c.agent === filterAgent) : commands
  const sorted = [...visible].sort((a, b) => {
    const order = { running: 0, failed: 1, done: 2 }
    return (order[a.state as keyof typeof order] ?? 3) - (order[b.state as keyof typeof order] ?? 3)
  })

  if (!sorted.length) return <div style={{ padding: '24px 16px', color: 'var(--text2)' }}>No commands recorded.</div>

  return (
    <div className="cmd-table-wrap">
      <table className="cmd-table">
        <thead>
          <tr>
            <th>State</th>
            <th>Verb</th>
            <th>Agent</th>
            <th>Flags</th>
            <th>PID</th>
            <th>Duration</th>
            <th>Started</th>
            <th>Outcome</th>
          </tr>
        </thead>
        <tbody>
          {sorted.map(c => (
            <tr key={c.id}>
              <td><span className="state-dot" style={{ background: STATE_COLOR[c.state] ?? 'var(--text2)' }} />{c.state}</td>
              <td className="td-verb">{c.verb}</td>
              <td className="td-agent">{c.agent ?? '—'}</td>
              <td className="td-flags">{(c.flags ?? []).join(' ')}</td>
              <td className="td-pid">{c.pid ?? '—'}</td>
              <td>{fmtDuration(c.durationMs)}</td>
              <td>{relTime(c.startedAt)}</td>
              <td>{c.outcome ?? '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── App ───────────────────────────────────────────────────────────────────────

type Tab = 'thread' | 'eval' | 'commands' | 'events'

export default function App() {
  const { agents, liveChannels, commands, messages, toolEvents, evalHits, selectedChannel, selectChannel, connectedClients } = useFleetStore()
  const [tab, setTab] = useState<Tab>('eval')
  const [evalFilter, setEvalFilter] = useState('')
  const liveCount = [...liveChannels].filter(ch => agents.some(a => a.id === ch)).length
  const runningCount = commands.filter(c => c.state === 'running').length

  return (
    <div className="layout">
      <header className="top-bar">
        <span className="logo">⬡ Parlay</span>
        <CommandBar commands={commands} />
        <div className="top-stats">
          <span title="live agent channels">{liveCount} live</span>
          <span title="running commands">{runningCount} cmds</span>
          <span title="SSE clients">{connectedClients} clients</span>
        </div>
      </header>

      <div className="body">
        <AgentList
          agents={agents}
          liveChannels={liveChannels}
          selected={selectedChannel}
          onSelect={selectChannel}
        />

        <main className="main-panel">
          <div className="tab-bar">
            <button className={tab === 'thread' ? 'tab active' : 'tab'} onClick={() => setTab('thread')}>
              Thread
            </button>
            <button className={tab === 'eval' ? 'tab active' : 'tab'} onClick={() => setTab('eval')}>
              Eval {evalHits.length > 0 && <span className="badge">{evalHits.length}</span>}
            </button>
            <button className={tab === 'commands' ? 'tab active' : 'tab'} onClick={() => setTab('commands')}>
              Commands <span className="badge">{runningCount}</span>
            </button>
            <button className={tab === 'events' ? 'tab active' : 'tab'} onClick={() => setTab('events')}>
              Tool Events {toolEvents.length > 0 && <span className="badge">{toolEvents.length}</span>}
            </button>
          </div>
          {tab === 'eval' && (
            <>
              <div className="eval-filter-bar">
                <input
                  className="eval-filter"
                  placeholder="filter by verb or stream…"
                  value={evalFilter}
                  onChange={e => setEvalFilter(e.target.value)}
                />
              </div>
              <EvalLog hits={evalHits} filter={evalFilter} />
            </>
          )}
          {tab === 'thread' && <MessageThread messages={messages} />}
          {tab === 'commands' && <CommandTable commands={commands} filterAgent={selectedChannel} />}
          {tab === 'events' && <ToolLog events={toolEvents} filterChannel={selectedChannel} />}
        </main>
      </div>
    </div>
  )
}
