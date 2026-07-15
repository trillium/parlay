// CSS part 2: thread, messages, bubbles, thinking indicator, input, connection banner
export const CSS_THREAD = `
  #pa-thread {
    flex: 1; overflow-y: auto; padding: 16px 14px 56px;
    display: flex; flex-direction: column; gap: 14px; scroll-behavior: smooth;
  }
  #pa-thread::-webkit-scrollbar { width: 3px; }
  #pa-thread::-webkit-scrollbar-thumb { background: var(--pa-border); border-radius: 2px; }
  #pa-empty {
    flex: 1; display: flex; flex-direction: column;
    align-items: center; justify-content: center;
    gap: 8px; text-align: center; padding: 24px; color: var(--pa-muted); font-size: 12px;
  }
  .pa-empty-dot { width: 8px; height: 8px; border-radius: 50%; background: var(--pa-green); opacity: .3; margin: 0 auto; }
  .pa-msg { display: flex; gap: 8px; animation: pa-fi .18s ease; }
  .pa-msg.user { flex-direction: row-reverse; }
  @keyframes pa-fi { from{opacity:0;transform:translateY(3px)}to{opacity:1} }
  .pa-av {
    width: 24px; height: 24px; border-radius: 50%; flex-shrink: 0;
    display: flex; align-items: center; justify-content: center;
    font-family: var(--pa-mono); font-size: 8px; font-weight: 700; margin-top: 2px;
  }
  .pa-av.agent { background: color-mix(in srgb,var(--pa-green) 14%,var(--pa-ink)); color: var(--pa-green); border: 1px solid color-mix(in srgb,var(--pa-green) 22%,transparent); }
  .pa-av.user  { background: color-mix(in srgb,var(--pa-blue)  14%,var(--pa-ink)); color: var(--pa-blue);  border: 1px solid color-mix(in srgb,var(--pa-blue)  22%,transparent); }
  .pa-bc { display: flex; flex-direction: column; max-width: min(80%,280px); gap: 3px; }
  .pa-msg.user .pa-bc { align-items: flex-end; }
  .pa-meta { font-family: var(--pa-mono); font-size: 9px; color: var(--pa-muted); padding: 0 4px; display: flex; gap: 5px; align-items: baseline; }
  .pa-meta-n { font-weight: 600; }
  .pa-meta-id { font-size: 8px; opacity: .45; font-weight: 400; }
  .pa-msg-status { font-size: 10px; line-height: 1; margin-left: 1px; }
  .pa-msg-status.queued { color: var(--pa-amber); opacity: .7; }
  .pa-msg-status.received { color: var(--pa-green); }
  .pa-bubble { padding: 9px 12px; font-size: 12.5px; line-height: 1.6; white-space: pre-wrap; word-break: break-word; }
  .pa-bubble.agent {
    background: color-mix(in srgb,var(--pa-green) 7%,var(--pa-surf2));
    border: 1px solid color-mix(in srgb,var(--pa-green) 16%,var(--pa-border));
    border-radius: 3px 10px 10px 10px; font-family: var(--pa-mono); font-size: 11.5px;
  }
  .pa-bubble.user {
    background: color-mix(in srgb,var(--pa-blue) 9%,var(--pa-surf2));
    border: 1px solid color-mix(in srgb,var(--pa-blue) 20%,var(--pa-border));
    border-radius: 10px 3px 10px 10px;
  }
  .pa-thinking { display: flex; gap: 8px; animation: pa-fi .18s ease; }
  .pa-thinking-dots {
    display: flex; gap: 3px; padding: 9px 13px;
    background: color-mix(in srgb,var(--pa-green) 7%,var(--pa-surf2));
    border: 1px solid color-mix(in srgb,var(--pa-green) 16%,var(--pa-border));
    border-radius: 3px 10px 10px 10px;
  }
  .pa-thinking-dots b { display:block;width:4px;height:4px;border-radius:50%;background:var(--pa-green);opacity:.3;animation:pa-dot .9s ease-in-out infinite; }
  .pa-thinking-dots b:nth-child(2){animation-delay:.15s}.pa-thinking-dots b:nth-child(3){animation-delay:.3s}
  @keyframes pa-dot{0%,80%,100%{opacity:.3;transform:scale(1)}40%{opacity:1;transform:scale(1.3)}}
  #pa-input-area { flex-shrink:0;border-top:1px solid var(--pa-border);padding:10px 14px 14px;background:var(--pa-ink); }
  /* Single non-wrapping flex line: [📎 attach] [ textarea (grows) ] [send].
     min-width:0 on the textarea lets it shrink instead of shoving the buttons
     onto their own lines at narrow (mobile) widths. */
  #pa-input-row { display:flex;flex-wrap:nowrap;align-items:flex-end;gap:8px; }
  #pa-input {
    flex:1 1 auto;min-width:0;background:var(--pa-surf2);border:1px solid var(--pa-border);color:var(--pa-body);
    border-radius:7px;padding:8px 11px;font-size:13px;font-family:var(--pa-sans);
    line-height:1.5;resize:none;min-height:38px !important;max-height:140px;outline:none;
    transition:border-color .15s;box-sizing:border-box !important;
  }
  #pa-input-area { min-height:62px !important; }
  #pa-input-row  { min-height:38px !important; }
  #pa-input:focus { border-color: color-mix(in srgb,var(--pa-blue) 50%,transparent); }
  #pa-input::placeholder { color:var(--pa-muted);opacity:.5; }
  #pa-send {
    flex-shrink:0;align-self:flex-end;
    height:34px;padding:0 12px;
    background:color-mix(in srgb,var(--pa-blue) 14%,transparent);
    border:1px solid color-mix(in srgb,var(--pa-blue) 28%,transparent);
    color:var(--pa-blue);border-radius:8px;
    font-family:var(--pa-mono);font-size:11px;font-weight:700;letter-spacing:.08em;
    cursor:pointer;transition:background .12s,border-color .12s;
  }
  #pa-send:hover:not(:disabled){background:color-mix(in srgb,var(--pa-blue) 22%,transparent);}
  #pa-send:disabled{opacity:.35;cursor:default;}
  #pa-hint{margin-top:5px;font-family:var(--pa-mono);font-size:10px;color:var(--pa-muted);opacity:.35;text-align:right;}
  #pa-conn-banner {
    font-family: var(--pa-mono); font-size: 10px; letter-spacing: .05em;
    padding: 4px 14px; text-align: center; display: none;
  }
  #pa-conn-banner.show { display: block; }
  #pa-conn-banner.reconnecting { background: color-mix(in srgb,var(--pa-amber) 12%,var(--pa-surf)); color: var(--pa-amber); }
  #pa-conn-banner.offline      { background: color-mix(in srgb,var(--pa-red)   12%,var(--pa-surf)); color: var(--pa-red); }
`
