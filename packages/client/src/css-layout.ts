// CSS part 1: variables, trigger, badge, backdrop, drawer, header, TTS button
export const CSS_LAYOUT = `
  :root {
    --pa-ink:    #0D1117;
    --pa-surf:   #161B22;
    --pa-surf2:  #1C2128;
    --pa-border: #30363D;
    --pa-muted:  #7D8590;
    --pa-body:   #E6EDF3;
    --pa-green:  #3FB950;
    --pa-blue:   #58A6FF;
    --pa-amber:  #F0B429;
    --pa-red:    #F85149;
    --pa-mono:   'SFMono-Regular','SF Mono',Menlo,Consolas,'Courier New',monospace;
    --pa-sans:   -apple-system,'SF Pro Text','Segoe UI',system-ui,sans-serif;
    --pa-w:      85vw;
  }
  #pa-trigger {
    position: fixed; bottom: 22px; right: 22px; z-index: 9998;
    width: 46px; height: 46px; border-radius: 50%;
    background: var(--pa-surf); border: 1px solid var(--pa-border);
    color: var(--pa-green);
    display: flex; align-items: center; justify-content: center;
    cursor: pointer; box-shadow: 0 2px 12px rgba(0,0,0,.5);
    transition: border-color .15s, box-shadow .15s;
    font-size: 20px; user-select: none;
  }
  #pa-trigger:hover { border-color: var(--pa-green); box-shadow: 0 2px 16px rgba(63,185,80,.25); }
  #pa-trigger.open  { color: var(--pa-muted); }
  #pa-ann-btn {
    position: fixed; bottom: 76px; right: 22px; z-index: 9998;
    width: 38px; height: 38px; border-radius: 50%;
    background: var(--pa-surf); border: 1px solid var(--pa-border);
    color: var(--pa-muted);
    display: flex; align-items: center; justify-content: center;
    cursor: pointer; box-shadow: 0 2px 10px rgba(0,0,0,.4);
    transition: border-color .15s, color .15s, box-shadow .15s;
    font-size: 16px; user-select: none;
  }
  #pa-ann-btn:hover { border-color: var(--pa-amber); color: var(--pa-amber); box-shadow: 0 2px 14px rgba(240,180,41,.2); }
  #pa-ann-btn.active { color: var(--pa-amber); border-color: var(--pa-amber); background: color-mix(in srgb, var(--pa-amber) 10%, var(--pa-surf)); }
  #pa-badge {
    position: absolute; top: -4px; right: -4px;
    background: var(--pa-red); color: #fff;
    font-family: var(--pa-mono); font-size: 10px; font-weight: 700;
    width: 18px; height: 18px; border-radius: 50%;
    display: none; align-items: center; justify-content: center;
    border: 1.5px solid var(--pa-ink);
  }
  #pa-badge.visible { display: flex; }
  #pa-backdrop {
    position: fixed; inset: 0; z-index: 9998;
    background: rgba(0,0,0,.45);
    opacity: 0; pointer-events: none;
    transition: opacity .22s cubic-bezier(.4,0,.2,1);
  }
  #pa-backdrop.open { opacity: 1; pointer-events: auto; }
  #pa-drawer {
    position: fixed; top: 0; left: 0;
    width: var(--pa-w); height: 100%;
    z-index: 9999;
    background: var(--pa-surf); border-right: 1px solid var(--pa-border);
    display: flex; flex-direction: column;
    transform: translateX(-100%);
    transition: transform .22s cubic-bezier(.4,0,.2,1);
    box-shadow: 4px 0 24px rgba(0,0,0,.5);
    font-family: var(--pa-sans); font-size: 14px; color: var(--pa-body);
  }
  #pa-drawer.open { transform: translateX(0); }
  /* Light mode — toggled via "light mode" / "dark mode" / "toggle theme" command */
  #pa-drawer.pa-light {
    --pa-ink:    #ffffff;
    --pa-surf:   #f6f8fa;
    --pa-surf2:  #eaeef2;
    --pa-border: #d0d7de;
    --pa-muted:  #57606a;
    --pa-body:   #1f2328;
    background: var(--pa-surf); border-right-color: var(--pa-border); color: var(--pa-body);
    box-shadow: 4px 0 24px rgba(0,0,0,.15);
  }
  #pa-drawer.pa-light #pa-hdr { background: var(--pa-ink); border-bottom-color: var(--pa-border); }
  #pa-drawer.pa-light #pa-thread::-webkit-scrollbar-thumb { background: var(--pa-border); }
  #pa-drawer.pa-light .pa-bubble.user { background: color-mix(in srgb,var(--pa-blue) 12%,var(--pa-surf2)); border-color: color-mix(in srgb,var(--pa-blue) 22%,var(--pa-border)); color: var(--pa-body); }
  #pa-drawer.pa-light .pa-bubble.agent { color: var(--pa-body); }
  #pa-drawer.pa-light .pa-input-wrap { background: var(--pa-ink); border-top-color: var(--pa-border); }
  #pa-drawer.pa-light textarea#pa-input { background: var(--pa-surf2); border-color: var(--pa-border); color: var(--pa-body); }
  #pa-drawer.pa-light .pa-tabs { background: var(--pa-ink); border-bottom-color: var(--pa-border); }
  #pa-drawer.pa-light .pa-tab { color: var(--pa-muted); border-color: transparent; }
  #pa-drawer.pa-light .pa-tab.active { background: var(--pa-surf); color: var(--pa-body); border-color: var(--pa-border); }
  #pa-drawer.pa-light .pa-debug-body { background: var(--pa-surf2); }
  #pa-hdr {
    flex-shrink: 0; padding: 11px 14px;
    background: var(--pa-ink); border-bottom: 1px solid var(--pa-border);
    display: flex; align-items: center; gap: 8px;
  }
  .pa-dot { width: 7px; height: 7px; border-radius: 50%; background: var(--pa-green); flex-shrink: 0; box-shadow: 0 0 0 2px color-mix(in srgb, var(--pa-green) 20%, transparent); }
  .pa-dot.thinking { background: var(--pa-blue); animation: pa-blink .9s ease-in-out infinite; }
  .pa-dot.offline  { background: var(--pa-muted); box-shadow: none; }
  @keyframes pa-blink { 0%,100%{opacity:1}50%{opacity:.2} }
  #pa-title { font-family: var(--pa-mono); font-size: 11px; font-weight: 700; letter-spacing: .1em; text-transform: uppercase; flex: 1; }
  #pa-sub { color: var(--pa-muted); font-weight: 400; margin-left: 5px; }
  .pa-hdr-btn {
    background: none; border: 1px solid var(--pa-border); color: var(--pa-muted);
    font-family: var(--pa-mono); font-size: 10px; letter-spacing: .06em;
    padding: 3px 8px; cursor: pointer; border-radius: 3px; flex-shrink: 0;
    transition: color .12s, border-color .12s;
  }
  .pa-hdr-btn:hover { color: var(--pa-body); border-color: var(--pa-muted); }
  .pa-hdr-btn.active { color: var(--pa-amber); border-color: var(--pa-amber); }
  #pa-close { background: none; border: none; color: var(--pa-muted); cursor: pointer; font-size: 16px; line-height: 1; padding: 2px 4px; }
  #pa-close:hover { color: var(--pa-body); }
  #pa-tts-btn {
    background: none; border: 1px solid var(--pa-border); color: var(--pa-muted);
    font-family: var(--pa-mono); font-size: 10px; letter-spacing: .06em;
    padding: 3px 8px; cursor: pointer; border-radius: 3px; flex-shrink: 0;
    transition: color .12s, border-color .12s;
  }
  #pa-tts-btn:hover { color: var(--pa-body); border-color: var(--pa-muted); }
  #pa-tts-btn.active { color: var(--pa-blue); border-color: var(--pa-blue); }
`
