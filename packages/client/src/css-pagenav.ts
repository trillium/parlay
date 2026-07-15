export const CSS_PAGENAV = `
  #pa-nav-overlay {
    position: fixed; inset: 0; z-index: 10001;
    background: rgba(0,0,0,.6);
    display: flex; align-items: flex-start; justify-content: center;
    padding-top: 12vh;
    opacity: 0; pointer-events: none;
    transition: opacity .15s ease;
  }
  #pa-nav-overlay.open { opacity: 1; pointer-events: auto; }

  #pa-nav-modal {
    background: var(--pa-surf); border: 1px solid var(--pa-border);
    border-radius: 8px; box-shadow: 0 8px 40px rgba(0,0,0,.6);
    width: min(440px, 92vw); max-height: 70vh;
    display: flex; flex-direction: column; overflow: hidden;
  }

  #pa-nav-search {
    background: var(--pa-surf2); border: none; border-bottom: 1px solid var(--pa-border);
    color: var(--pa-body); font-family: var(--pa-sans); font-size: 14px;
    padding: 13px 16px; outline: none; width: 100%; box-sizing: border-box;
  }
  #pa-nav-search::placeholder { color: var(--pa-muted); opacity: .6; }

  #pa-nav-list {
    overflow-y: auto; -webkit-overflow-scrolling: touch;
    padding: 6px; display: flex; flex-direction: column; gap: 2px;
  }
  .pa-nav-row {
    display: flex; align-items: baseline; gap: 10px;
    padding: 8px 10px; border-radius: 5px;
    background: none; border: none; width: 100%; text-align: left;
    cursor: pointer; color: var(--pa-body);
  }
  .pa-nav-row.sel { background: color-mix(in srgb, var(--pa-blue) 16%, var(--pa-surf2)); }
  .pa-nav-tag {
    font-family: var(--pa-mono); font-size: 12px; color: var(--pa-blue);
    flex-shrink: 0; white-space: nowrap;
  }
  .pa-nav-title {
    font-size: 12px; color: var(--pa-muted);
    overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  }
  .pa-nav-empty { padding: 16px; text-align: center; color: var(--pa-muted); font-size: 12px; }

  .pa-nav-hint {
    padding: 8px 14px; border-top: 1px solid var(--pa-border);
    font-family: var(--pa-mono); font-size: 10px; color: var(--pa-muted); opacity: .7;
  }
`
