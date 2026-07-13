// CSS part 3: tabs, tool log, annotation strip, popup, media queries, state classes
export const CSS_FEATURES = `
  #pa-tabs {
    flex-shrink: 0;
    display: none; flex-direction: row; overflow-x: auto;
    border-bottom: 1px solid var(--pa-border);
    background: color-mix(in srgb, var(--pa-ink) 70%, var(--pa-surf));
    scrollbar-width: none; -ms-overflow-style: none;
  }
  #pa-tabs.visible { display: flex; }
  #pa-tabs::-webkit-scrollbar { display: none; }
  .pa-tab {
    flex-shrink: 0; padding: 8px 14px;
    background: none; border: none; border-bottom: 2px solid transparent;
    color: var(--pa-muted); font-family: var(--pa-mono); font-size: 10px;
    font-weight: 700; letter-spacing: .08em; text-transform: uppercase;
    cursor: pointer; white-space: nowrap; position: relative;
    transition: color .12s, border-bottom-color .12s;
  }
  .pa-tab:hover { color: var(--pa-body); }
  .pa-tab.active { color: var(--pa-body); border-bottom-color: var(--tab-color, var(--pa-green)); }
  .pa-tab-pip { display: inline-block; width: 5px; height: 5px; border-radius: 50%; background: var(--tab-color, var(--pa-green)); margin-right: 5px; vertical-align: middle; margin-bottom: 1px; }
  /* Status dot states: green = listening, grey = idle, hollow = offline */
  .pa-tab-pip.listening { background: var(--pa-green); box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-green) 22%, transparent); }
  .pa-tab-pip.idle      { background: var(--pa-muted); }
  .pa-tab-pip.offline   { background: transparent; box-shadow: inset 0 0 0 1px var(--pa-muted); }
  .pa-tab-unread { position: absolute; top: 3px; right: 2px; min-width: 14px; height: 14px; border-radius: 7px; padding: 0 3px; background: var(--pa-red); color: #fff; font-size: 8px; font-family: var(--pa-mono); font-weight: 700; display: none; align-items: center; justify-content: center; line-height: 1; }
  .pa-tab-unread.visible { display: flex; }
  .pa-tab-label-wrap { display: inline-flex; flex-direction: column; align-items: flex-start; line-height: 1.1; vertical-align: middle; }
  .pa-tab-id { font-size: 8px; font-weight: 400; color: var(--pa-muted); letter-spacing: .04em; text-transform: none; opacity: .7; }
  .pa-tab-x { display: none; margin-left: 6px; vertical-align: middle; color: var(--pa-muted); font-size: 11px; line-height: 1; padding: 0 2px; border-radius: 3px; }
  .pa-tab:hover .pa-tab-x { display: inline-block; }
  .pa-tab-x:hover { color: var(--pa-body); background: color-mix(in srgb, var(--pa-muted) 20%, transparent); }
  .pa-arch-wrap { position: relative; flex-shrink: 0; margin-left: auto; }
  .pa-arch-btn { color: var(--pa-muted); opacity: .8; }
  .pa-arch-menu {
    display: none; position: absolute; top: 100%; right: 4px; z-index: 30;
    min-width: 170px; padding: 4px;
    background: var(--pa-surf); border: 1px solid var(--pa-border); border-radius: 8px;
    box-shadow: 0 6px 20px rgba(0,0,0,.35);
  }
  .pa-arch-menu.open { display: flex; flex-direction: column; gap: 2px; }
  .pa-arch-row {
    display: flex; align-items: center; gap: 6px; padding: 7px 9px;
    background: none; border: none; border-radius: 6px; cursor: pointer;
    color: var(--pa-body); font-family: var(--pa-mono); font-size: 11px; text-align: left;
  }
  .pa-arch-row:hover { background: color-mix(in srgb, var(--pa-muted) 12%, transparent); }
  .pa-arch-row-id { font-size: 8px; color: var(--pa-muted); margin-left: auto; }
  .pa-action-card { display: flex; align-items: center; gap: 9px; padding: 9px 11px; border: 1px solid var(--pa-border); border-radius: 8px; background: color-mix(in srgb, var(--pa-green) 5%, var(--pa-surf2)); }
  .pa-action-icon { color: var(--pa-green); font-size: 13px; flex-shrink: 0; }
  .pa-action-label { flex: 1; font-family: var(--pa-mono); font-size: 11.5px; color: var(--pa-body); }
  .pa-action-btn { flex-shrink: 0; padding: 5px 12px; border-radius: 6px; cursor: pointer; border: 1px solid color-mix(in srgb, var(--pa-green) 45%, transparent); background: color-mix(in srgb, var(--pa-green) 13%, transparent); color: var(--pa-green); font-family: var(--pa-mono); font-size: 11px; }
  .pa-action-btn:hover { background: color-mix(in srgb, var(--pa-green) 24%, transparent); }
  /* Mobile agent switcher: FAB floats 15px above the input area, sheet is a tap-friendly agent list */
  #pa-input-area { position: relative; }
  #pa-fab {
    position: absolute; top: -15px; right: 14px; transform: translateY(-100%);
    width: 40px; height: 40px; border-radius: 50%; z-index: 6; cursor: pointer;
    background: color-mix(in srgb, var(--pa-green) 14%, var(--pa-surf)); color: var(--pa-green);
    border: 1px solid color-mix(in srgb, var(--pa-green) 40%, var(--pa-border));
    font-size: 17px; line-height: 1; box-shadow: 0 4px 14px rgba(0,0,0,.35);
    display: flex; align-items: center; justify-content: center;
  }
  #pa-fab:hover { background: color-mix(in srgb, var(--pa-green) 24%, var(--pa-surf)); }
  #pa-sheet {
    position: absolute; bottom: 0; left: 0; right: 0; z-index: 25;
    display: none; flex-direction: column; max-height: 62%;
    background: var(--pa-surf); border-top: 1px solid var(--pa-border);
    border-radius: 14px 14px 0 0; box-shadow: 0 -10px 30px rgba(0,0,0,.45);
  }
  #pa-sheet.open { display: flex; }
  #pa-sheet-head { display: flex; align-items: center; justify-content: space-between; padding: 12px 16px 8px; font-family: var(--pa-mono); font-size: 11px; letter-spacing: .08em; text-transform: uppercase; color: var(--pa-muted); }
  #pa-sheet-close { background: none; border: none; color: var(--pa-muted); font-size: 14px; cursor: pointer; padding: 4px 6px; }
  #pa-sheet-list { overflow-y: auto; padding: 0 8px 12px; display: flex; flex-direction: column; gap: 2px; }
  .pa-sheet-row {
    display: flex; align-items: center; gap: 11px; min-height: 48px; padding: 10px 12px;
    background: none; border: none; border-radius: 10px; cursor: pointer; text-align: left;
    color: var(--pa-body); font-family: var(--pa-mono); font-size: 13px;
  }
  .pa-sheet-row:hover, .pa-sheet-row:active { background: color-mix(in srgb, var(--pa-muted) 12%, transparent); }
  .pa-sheet-row.active { background: color-mix(in srgb, var(--tab-color, var(--pa-green)) 10%, transparent); }
  .pa-sheet-row .pa-tab-pip { width: 8px; height: 8px; }
  .pa-sheet-name { font-weight: 600; }
  .pa-sheet-id { font-size: 10px; color: var(--pa-muted); margin-left: auto; }
  #pa-sheet-actions { display: grid; grid-template-columns: 1fr 1fr; gap: 6px; padding: 8px 10px 14px; border-top: 1px solid var(--pa-border); }
  .pa-sheet-act {
    min-height: 44px; border-radius: 10px; cursor: pointer;
    background: color-mix(in srgb, var(--pa-muted) 8%, transparent); border: 1px solid var(--pa-border);
    color: var(--pa-body); font-family: var(--pa-mono); font-size: 12px;
  }
  .pa-sheet-act:hover, .pa-sheet-act:active { background: color-mix(in srgb, var(--pa-muted) 16%, transparent); }
  /* Message currently being spoken aloud */
  .pa-bubble.pa-speaking { box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-amber) 55%, transparent); animation: pa-speak-pulse 1.6s ease-in-out infinite; }
  @keyframes pa-speak-pulse { 0%,100% { box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-amber) 55%, transparent); } 50% { box-shadow: 0 0 0 3px color-mix(in srgb, var(--pa-amber) 25%, transparent); } }
  /* System update lines (hook firings etc.) — thin, muted, full-width */
  .pa-sysline { display: flex; align-items: baseline; gap: 7px; padding: 2px 6px; font-family: var(--pa-mono); font-size: 9.5px; color: var(--pa-muted); opacity: .85; }
  .pa-sysline-src { flex-shrink: 0; color: var(--pa-amber); font-weight: 700; letter-spacing: .05em; text-transform: uppercase; font-size: 8.5px; }
  .pa-sysline-src::before { content: '⚙ '; }
  .pa-sysline-text { flex: 1; min-width: 0; word-break: break-word; }
  .pa-sysline-ts { flex-shrink: 0; font-size: 8px; opacity: .7; }
  /* Captain's messages clamp by default; tap to expand */
  .pa-bubble.pa-clamped { max-height: 76px; overflow: hidden; position: relative; cursor: pointer; }
  .pa-bubble.pa-clamped::after {
    content: '⌄ tap to expand'; position: absolute; bottom: 0; left: 0; right: 0;
    padding: 18px 10px 3px; font-size: 9px; text-align: center; color: var(--pa-muted);
    background: linear-gradient(transparent, color-mix(in srgb, var(--pa-blue) 9%, var(--pa-surf2)) 70%);
  }
  .pa-bubble.pa-expandable:not(.pa-clamped) { cursor: pointer; }
  #pa-ann-strip { flex-shrink: 0; display: none; padding: 8px 14px; background: color-mix(in srgb, var(--pa-amber) 5%, var(--pa-surf)); border-bottom: 1px solid var(--pa-border); }
  #pa-ann-strip.visible { display: block; }
  .pa-ann-strip-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 6px; }
  .pa-ann-label { font-family: var(--pa-mono); font-size: 10px; color: var(--pa-amber); font-weight: 700; letter-spacing: .08em; }
  #pa-ann-send { font-family: var(--pa-mono); font-size: 10px; background: color-mix(in srgb,var(--pa-amber) 15%,transparent); border: 1px solid color-mix(in srgb,var(--pa-amber) 35%,transparent); color: var(--pa-amber); padding: 3px 10px; cursor: pointer; border-radius: 3px; }
  .pa-ann-strip-head { gap: 8px; }
  #pa-ann-exit { font-family: var(--pa-mono); font-size: 10px; background: none; border: 1px solid var(--pa-border); color: var(--pa-muted); padding: 3px 10px; cursor: pointer; border-radius: 3px; margin-left: auto; }
  #pa-ann-exit:hover { border-color: var(--pa-muted); color: var(--pa-body); }
  #pa-ann-hint { font-size: 11px; color: var(--pa-muted); margin-bottom: 6px; }
  #pa-ann-hint b { color: var(--pa-amber); font-family: var(--pa-mono); font-weight: 700; }
  #pa-ann-strip.empty #pa-ann-send { display: none; }
  #pa-ann-strip:not(.empty) #pa-ann-hint { display: none; }
  #pa-ann-list { display: flex; flex-direction: column; gap: 4px; max-height: 110px; overflow-y: auto; }
  .pa-ann-item { display: flex; gap: 8px; align-items: flex-start; font-size: 12px; line-height: 1.4; }
  .pa-ann-num { font-family: var(--pa-mono); font-size: 10px; font-weight: 700; background: var(--pa-amber); color: var(--pa-ink); width: 16px; height: 16px; border-radius: 50%; display: flex; align-items: center; justify-content: center; flex-shrink: 0; margin-top: 1px; }
  .pa-ann-el   { color: var(--pa-muted); font-size: 11px; font-family: var(--pa-mono); }
  .pa-ann-text { color: var(--pa-body); }
  .pa-ann-rm   { margin-left: auto; color: var(--pa-muted); cursor: pointer; background: none; border: none; font-size: 13px; flex-shrink: 0; }
  .pa-ann-rm:hover { color: var(--pa-red); }
  #pa-toollog { display: none; flex: 1; overflow-y: auto; padding: 10px 12px; flex-direction: column; gap: 5px; font-family: var(--pa-mono); font-size: 10.5px; }
  #pa-toollog.visible { display: flex; }
  #pa-toollog::-webkit-scrollbar { width: 3px; }
  #pa-toollog::-webkit-scrollbar-thumb { background: var(--pa-border); border-radius: 2px; }
  .pa-tl-entry { padding: 6px 9px; border-radius: 4px; background: var(--pa-surf2); border: 1px solid var(--pa-border); animation: pa-fi .12s ease; flex-shrink: 0; }
  .pa-tl-head { display: flex; align-items: baseline; gap: 6px; }
  .pa-tl-icon { font-size: 11px; }
  .pa-tl-tool { color: var(--pa-green); font-weight: 700; font-size: 10px; }
  .pa-tl-desc { color: var(--pa-body); opacity: .85; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 10px; }
  .pa-tl-ts   { color: var(--pa-muted); opacity: .45; font-size: 9px; flex-shrink: 0; }
  .pa-tl-body { margin-top: 3px; color: var(--pa-muted); font-size: 9.5px; opacity: .75; white-space: pre-wrap; word-break: break-all; line-height: 1.45; }
  .pa-tl-out  { color: var(--pa-muted); opacity: .55; }
  #pa-log-btn { background: none; border: 1px solid var(--pa-border); color: var(--pa-muted); font-family: var(--pa-mono); font-size: 10px; letter-spacing: .06em; padding: 3px 8px; cursor: pointer; border-radius: 3px; flex-shrink: 0; transition: color .12s, border-color .12s; }
  #pa-log-btn:hover { color: var(--pa-body); border-color: var(--pa-muted); }
  #pa-log-btn.active { color: var(--pa-green); border-color: var(--pa-green); }
  .pa-hover { outline: 2px solid color-mix(in srgb,var(--pa-amber) 55%,transparent) !important; outline-offset: 1px !important; cursor: crosshair !important; }
  #pa-popup { position: fixed; z-index: 10001; background: var(--pa-surf); border: 1px solid var(--pa-amber); border-radius: 8px; padding: 10px 12px; width: 260px; box-shadow: 0 4px 20px rgba(0,0,0,.5); display: none; }
  #pa-popup.visible { display: block; }
  #pa-popup-lbl { font-family:var(--pa-mono);font-size:10px;color:var(--pa-amber);margin-bottom:7px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap; }
  #pa-popup-in { width:100%;background:var(--pa-surf2);border:1px solid var(--pa-border);color:var(--pa-body);border-radius:5px;padding:6px 9px;font-size:12.5px;font-family:var(--pa-sans);resize:none;outline:none;transition:border-color .12s;min-height:60px; }
  #pa-popup-in:focus { border-color: var(--pa-amber); }
  #pa-popup-in::placeholder { color:var(--pa-muted);opacity:.5; }
  #pa-popup-btns { display:flex;gap:7px;margin-top:8px;justify-content:flex-end; }
  .pa-pb { font-family:var(--pa-mono);font-size:10px;letter-spacing:.06em;padding:4px 10px;border-radius:4px;cursor:pointer; }
  #pa-popup-cancel { background:none;border:1px solid var(--pa-border);color:var(--pa-muted); }
  #pa-popup-add    { background:color-mix(in srgb,var(--pa-amber) 18%,transparent);border:1px solid color-mix(in srgb,var(--pa-amber) 35%,transparent);color:var(--pa-amber); }
  .pa-ann-marker { position:absolute;width:18px;height:18px;background:var(--pa-amber);color:var(--pa-ink);border-radius:50%;font-family:var(--pa-mono);font-size:10px;font-weight:700;display:flex;align-items:center;justify-content:center;pointer-events:none;z-index:9997;border:1.5px solid rgba(0,0,0,.3);box-shadow:0 1px 4px rgba(0,0,0,.4); }
  .pa-lavish-card {
    display: flex; align-items: center; gap: 10px;
    padding: 10px 12px; border-radius: 8px;
    background: color-mix(in srgb, #f4c95d 8%, var(--pa-surf2));
    border: 1px solid color-mix(in srgb, #f4c95d 30%, var(--pa-border));
    animation: pa-fi .18s ease; flex-shrink: 0;
  }
  .pa-lavish-card.closed { opacity: .45; pointer-events: none; }
  .pa-lavish-icon { font-size: 18px; flex-shrink: 0; }
  .pa-lavish-body { flex: 1; min-width: 0; }
  .pa-lavish-label { font-family: var(--pa-mono); font-size: 9px; letter-spacing: .08em; color: #f4c95d; font-weight: 700; text-transform: uppercase; }
  .pa-lavish-name  { font-size: 12px; color: var(--pa-body); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pa-lavish-btn   { flex-shrink: 0; font-family: var(--pa-mono); font-size: 10px; font-weight: 700; letter-spacing: .06em; padding: 5px 10px; border-radius: 5px; background: color-mix(in srgb, #f4c95d 18%, transparent); border: 1px solid color-mix(in srgb, #f4c95d 40%, transparent); color: #f4c95d; text-decoration: none; }
  .pa-lavish-btn:hover { background: color-mix(in srgb, #f4c95d 28%, transparent); }
  /* Standalone PWA behaves like mobile browser: 85vw drawer, right 15% is the
     tap-out dismissal strip (backdrop), trigger visible to reopen. */
  /* Agent replies use the full panel width — it's a chat pane, not a phone bubble */
  .pa-msg.agent .pa-bc { max-width: 100%; flex: 1; min-width: 0; }
  @media (min-width: 960px) {
    :root { --pa-w: 380px; }
    #pa-backdrop { display: none !important; }
    #pa-trigger  { display: none !important; }
    #pa-close    { display: none !important; }
    #pa-drawer { transition: none; box-shadow: 2px 0 16px rgba(0,0,0,.3); }
  }
  #pa-drawer.agent-away #pa-hdr { opacity: .75; }
  #pa-drawer.agent-away .pa-dot { background: var(--pa-muted) !important; box-shadow: none !important; animation: none !important; }
  #pa-drawer.agent-away #pa-title::after { content: ' — unattended'; color: var(--pa-muted); font-weight: 400; font-size: 10px; }
  #pa-drawer.compacting { background: color-mix(in srgb, var(--pa-amber) 9%, var(--pa-surf)); border-right-color: color-mix(in srgb, var(--pa-amber) 45%, transparent); transition: background .4s ease, border-color .4s ease; }
  #pa-drawer.compacting #pa-hdr { background: color-mix(in srgb, var(--pa-amber) 14%, var(--pa-ink)); border-bottom-color: color-mix(in srgb, var(--pa-amber) 35%, transparent); }
  #pa-drawer.compacting #pa-input-area { background: color-mix(in srgb, var(--pa-amber) 7%, var(--pa-ink)); }
  #pa-drawer.compacting #pa-thread { background: color-mix(in srgb, var(--pa-amber) 4%, var(--pa-surf)); }
`
