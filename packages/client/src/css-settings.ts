export const CSS_SETTINGS = `
  #pa-settings-overlay {
    position: fixed; inset: 0; z-index: 10000;
    background: rgba(0,0,0,.6);
    display: flex; align-items: center; justify-content: center;
    opacity: 0; pointer-events: none;
    transition: opacity .18s ease;
  }
  #pa-settings-overlay.open { opacity: 1; pointer-events: auto; }

  #pa-settings-modal {
    background: var(--pa-surf); border: 1px solid var(--pa-border);
    border-radius: 8px; padding: 20px 22px 18px;
    width: min(420px, 90vw);
    /* Mobile-first: never taller than the viewport; the accordion keeps content
       short, but a fully-expanded section can still scroll inside the modal. */
    max-height: 90vh; overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    box-shadow: 0 8px 40px rgba(0,0,0,.6);
    font-family: var(--pa-sans); color: var(--pa-body);
    display: flex; flex-direction: column; gap: 10px;
  }

  /* Accordion groups: each <details> is one collapsible section. The <summary>
     is a full-width tap target sized for a phone. Collapsed groups keep the
     panel short and scannable so any setting is 1-2 taps away. */
  .pa-settings-group {
    border: 1px solid var(--pa-border); border-radius: 6px;
    background: var(--pa-surf2);
    overflow: hidden;
  }
  .pa-settings-summary {
    list-style: none; cursor: pointer; user-select: none;
    display: flex; align-items: center; gap: 8px;
    padding: 12px 14px; min-height: 20px;
    font-family: var(--pa-mono); font-size: 11px; font-weight: 700;
    letter-spacing: .1em; text-transform: uppercase;
    color: var(--pa-body);
    transition: color .12s, background .12s;
  }
  .pa-settings-summary::-webkit-details-marker { display: none; }
  .pa-settings-summary::after {
    content: '▸'; margin-left: auto;
    font-size: 10px; color: var(--pa-muted);
    transition: transform .15s ease;
  }
  .pa-settings-group[open] > .pa-settings-summary::after { transform: rotate(90deg); }
  .pa-settings-summary:hover { color: var(--pa-blue); background: color-mix(in srgb, var(--pa-blue) 6%, var(--pa-surf2)); }
  .pa-settings-summary-tag {
    font-size: 8px; font-weight: 700; letter-spacing: .08em;
    padding: 2px 6px; border-radius: 3px;
    color: var(--pa-blue);
    background: color-mix(in srgb, var(--pa-blue) 14%, transparent);
  }
  .pa-settings-group-body {
    display: flex; flex-direction: column; gap: 14px;
    padding: 4px 14px 14px;
    border-top: 1px solid var(--pa-border);
  }

  #pa-settings-modal h2 {
    font-family: var(--pa-mono); font-size: 11px; font-weight: 700;
    letter-spacing: .12em; text-transform: uppercase;
    color: var(--pa-body); margin: 0;
  }

  .pa-settings-section {
    display: flex; flex-direction: column; gap: 8px;
  }
  .pa-settings-label {
    font-family: var(--pa-mono); font-size: 10px; font-weight: 700;
    letter-spacing: .1em; text-transform: uppercase;
    color: var(--pa-muted);
  }
  .pa-settings-row {
    display: flex; gap: 8px;
  }
  .pa-settings-radio {
    flex: 1; position: relative;
  }
  .pa-settings-radio input[type="radio"] {
    position: absolute; opacity: 0; width: 0; height: 0;
  }
  .pa-settings-radio label {
    display: flex; align-items: center; justify-content: center;
    padding: 7px 12px; border-radius: 4px;
    border: 1px solid var(--pa-border);
    background: var(--pa-surf2); color: var(--pa-muted);
    font-family: var(--pa-mono); font-size: 11px; letter-spacing: .06em;
    cursor: pointer; transition: border-color .12s, color .12s, background .12s;
    user-select: none;
  }
  .pa-settings-radio input[type="radio"]:checked + label {
    border-color: var(--pa-blue);
    color: var(--pa-blue);
    background: color-mix(in srgb, var(--pa-blue) 8%, var(--pa-surf2));
  }
  .pa-settings-radio label:hover {
    border-color: var(--pa-muted);
    color: var(--pa-body);
  }

  #pa-settings-all-wrap, .pa-settings-all-wrap {
    display: flex; align-items: center; gap: 8px;
  }
  #pa-settings-all-wrap input[type="checkbox"],
  .pa-settings-all-wrap input[type="checkbox"] {
    width: 14px; height: 14px; accent-color: var(--pa-blue); cursor: pointer; flex-shrink: 0;
  }
  #pa-settings-all-wrap label,
  .pa-settings-all-wrap label {
    font-family: var(--pa-mono); font-size: 11px; color: var(--pa-muted); cursor: pointer;
  }
  #pa-settings-projects, #pa-settings-submit-phrases {
    background: var(--pa-surf2); border: 1px solid var(--pa-border);
    border-radius: 4px; padding: 8px 10px;
    font-family: var(--pa-mono); font-size: 11px; color: var(--pa-body);
    resize: vertical; min-height: 64px; outline: none;
    transition: border-color .12s;
  }
  #pa-settings-projects:focus,
  #pa-settings-submit-phrases:focus { border-color: var(--pa-blue); }
  #pa-settings-projects:disabled,
  #pa-settings-submit-phrases:disabled { opacity: .35; cursor: not-allowed; }
  #pa-settings-clear-phrase {
    background: var(--pa-surf2); border: 1px solid var(--pa-border);
    border-radius: 4px; padding: 6px 10px;
    font-family: var(--pa-mono); font-size: 11px; color: var(--pa-body);
    outline: none; width: 100%; box-sizing: border-box;
    transition: border-color .12s;
  }
  #pa-settings-clear-phrase:focus { border-color: var(--pa-blue); }
  #pa-settings-clear-phrase:disabled { opacity: .35; cursor: not-allowed; }
  .pa-settings-hint {
    font-size: 10px; color: var(--pa-muted); line-height: 1.5;
  }

  .pa-settings-footer {
    display: flex; justify-content: flex-end; gap: 8px; padding-top: 2px;
  }
  .pa-settings-btn {
    font-family: var(--pa-mono); font-size: 11px; letter-spacing: .06em;
    padding: 6px 14px; border-radius: 4px; cursor: pointer;
    border: 1px solid var(--pa-border); transition: border-color .12s, color .12s, background .12s;
  }
  #pa-settings-cancel {
    background: none; color: var(--pa-muted);
  }
  #pa-settings-cancel:hover { border-color: var(--pa-muted); color: var(--pa-body); }

  #pa-settings-btn-gear {
    background: none; border: 1px solid transparent; color: var(--pa-muted);
    font-size: 14px; line-height: 1; padding: 2px 5px; cursor: pointer;
    border-radius: 3px; flex-shrink: 0;
    transition: color .12s, border-color .12s;
  }
  #pa-settings-btn-gear:hover { color: var(--pa-body); border-color: var(--pa-border); }
`
