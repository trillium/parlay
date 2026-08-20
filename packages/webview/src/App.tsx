import { useState, useEffect, useRef } from 'react'
import { useFleetStore } from './useFleetStore'
import type { Agent, Command, Message, ToolEvent } from './types'
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

// ── App ───────────────────────────────────────────────────────────────────────

type Tab = 'thread' | 'events'

export default function App() {
  const { agents, liveChannels, commands, messages, toolEvents, selectedChannel, selectChannel, connectedClients } = useFleetStore()
  const [tab, setTab] = useState<Tab>('thread')
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
            <button className={tab === 'events' ? 'tab active' : 'tab'} onClick={() => setTab('events')}>
              Tool Events {toolEvents.length > 0 && <span className="badge">{toolEvents.length}</span>}
            </button>
          </div>
          {tab === 'thread'
            ? <MessageThread messages={messages} />
            : <ToolLog events={toolEvents} filterChannel={selectedChannel} />
          }
        </main>
      </div>
    </div>
  )
}
