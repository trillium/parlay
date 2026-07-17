// CSS: debug-snapshot attachment pill styles (split from css-features.ts)
export const CSS_DEBUG = `
  /* Debug-snapshot attachment pill — appended inside the preceding message row */
  .pa-debug-pill { margin-top: 4px; }
  .pa-debug-toggle {
    display: inline-flex; align-items: center; gap: 5px;
    background: none; border: 1px solid var(--pa-border);
    border-radius: 5px; padding: 3px 8px;
    font-family: var(--pa-mono); font-size: 10px; color: var(--pa-muted);
    cursor: pointer; transition: border-color .15s, color .15s;
  }
  .pa-debug-toggle:hover { border-color: var(--pa-blue); color: var(--pa-blue); }
  .pa-debug-open .pa-debug-toggle { border-color: var(--pa-blue); color: var(--pa-blue); }
  .pa-debug-ts { font-size: 9px; opacity: .6; }
  .pa-debug-body {
    margin-top: 5px; padding: 8px 10px;
    background: var(--pa-ink); border: 1px solid var(--pa-border); border-radius: 6px;
    font-family: var(--pa-mono); font-size: 10px; color: var(--pa-muted);
    overflow-x: auto; white-space: pre; max-height: 200px; overflow-y: auto;
  }
`
