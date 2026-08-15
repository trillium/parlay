// CSS: the live-command view (split out of css-features.ts, same as css-debug.ts).
//
// #pa-cmdlog is a floating card OVER the thread rather than a thread
// replacement like the tool log, so the two views never fight over who owns
// #pa-thread's display. The bottom offset clears the input area.
export const CSS_COMMANDS = `
  #pa-cmdlog {
    display: none; position: absolute; left: 8px; right: 8px; bottom: 92px; z-index: 6;
    max-height: 52%; overflow-y: auto;
    padding: 8px 10px 10px; border-radius: 8px;
    background: var(--pa-surf2); border: 1px solid var(--pa-border);
    box-shadow: 0 6px 24px rgba(0,0,0,.45);
    font-family: var(--pa-mono); font-size: 10.5px;
  }
  #pa-cmdlog.visible { display: block; }
  #pa-cmdlog::-webkit-scrollbar { width: 3px; }
  #pa-cmdlog::-webkit-scrollbar-thumb { background: var(--pa-border); border-radius: 2px; }
  .pa-cmd-head { display: flex; align-items: baseline; gap: 8px; padding-bottom: 6px; margin-bottom: 6px; border-bottom: 1px solid var(--pa-border); }
  .pa-cmd-title { color: var(--pa-muted); font-size: 9px; font-weight: 700; letter-spacing: .1em; }
  .pa-cmd-count { color: var(--pa-body); opacity: .7; font-size: 9.5px; margin-left: auto; }
  .pa-cmd-row { display: flex; align-items: baseline; gap: 7px; padding: 3px 2px; border-radius: 3px; }
  .pa-cmd-row.done { opacity: .55; }
  .pa-cmd-dot { width: 6px; height: 6px; border-radius: 50%; flex-shrink: 0; background: var(--pa-muted); }
  .pa-cmd-dot.running  { background: var(--pa-green); box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-green) 22%, transparent); }
  .pa-cmd-dot.failed   { background: var(--pa-red); }
  .pa-cmd-dot.expired  { background: transparent; box-shadow: inset 0 0 0 1px var(--pa-muted); }
  .pa-cmd-verb { color: var(--pa-green); font-weight: 700; font-size: 10px; flex-shrink: 0; }
  .pa-cmd-who  { color: var(--pa-body); opacity: .8; font-size: 9.5px; flex-shrink: 0; }
  .pa-cmd-detail { color: var(--pa-muted); font-size: 9.5px; opacity: .75; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pa-cmd-age { color: var(--pa-muted); opacity: .55; font-size: 9px; flex-shrink: 0; font-variant-numeric: tabular-nums; }
  .pa-cmd-note { margin-top: 7px; padding-top: 6px; border-top: 1px solid var(--pa-border); color: var(--pa-muted); opacity: .6; font-size: 9px; line-height: 1.45; }
  #pa-cmd-btn { background: none; border: 1px solid var(--pa-border); color: var(--pa-muted); font-family: var(--pa-mono); font-size: 10px; letter-spacing: .06em; padding: 3px 8px; cursor: pointer; border-radius: 3px; flex-shrink: 0; transition: color .12s, border-color .12s; }
  #pa-cmd-btn:hover { color: var(--pa-body); border-color: var(--pa-muted); }
  #pa-cmd-btn.active { color: var(--pa-green); border-color: var(--pa-green); }
`
