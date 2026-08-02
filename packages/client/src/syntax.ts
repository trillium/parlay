// syntax.ts — lightweight in-house regex syntax highlighter.
// ~220 lines, synchronous, no WASM, no external deps.
// Languages: bash/sh, ts/tsx, js/jsx, json, diff, py/python, go.
// Caller receives a string of HTML; each token is wrapped in
// <span class="pa-t-{kw|str|cmt|num|fn}"> and everything is HTML-escaped.
// Unknown languages fall back to plain escaped text.

// ── Keyword sets ────────────────────────────────────────────────────────────

const TS_KW = new Set([
  'abstract','as','async','await','break','case','catch','class','const',
  'continue','debugger','declare','default','delete','do','else','enum',
  'export','extends','false','finally','for','from','function','if',
  'implements','import','in','instanceof','interface','keyof','let',
  'module','namespace','new','null','of','override','package','private',
  'protected','public','readonly','return','satisfies','static','super',
  'switch','this','throw','true','try','type','typeof','undefined','var',
  'void','while','with','yield','infer','never','unknown','any',
  'string','number','boolean','symbol','object','bigint',
])

const PY_KW = new Set([
  'and','as','assert','async','await','break','class','continue','def',
  'del','elif','else','except','False','finally','for','from','global',
  'if','import','in','is','lambda','None','nonlocal','not','or','pass',
  'raise','return','True','try','while','with','yield',
])

const GO_KW = new Set([
  'break','case','chan','const','continue','default','defer','else',
  'fallthrough','for','func','go','goto','if','import','interface','map',
  'package','range','return','select','struct','switch','type','var',
  'nil','true','false','error','string','int','int8','int16','int32',
  'int64','uint','uint8','uint16','uint32','uint64','float32','float64',
  'bool','byte','rune','any',
])

const BASH_KW = new Set([
  'if','then','elif','else','fi','for','in','do','done','while','until',
  'case','esac','function','return','exit','local','readonly','export',
  'unset','set','shift','source','true','false',
])

const JSON_KW = new Set(['true', 'false', 'null'])

const KW: Record<string, Set<string>> = {
  ts: TS_KW, tsx: TS_KW, js: TS_KW, jsx: TS_KW,
  py: PY_KW, python: PY_KW,
  go: GO_KW,
  bash: BASH_KW, sh: BASH_KW,
  json: JSON_KW,
}

// ── Helpers ─────────────────────────────────────────────────────────────────

function e(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

function sp(cls: string, content: string): string {
  return `<span class="pa-t-${cls}">${content}</span>`
}

// ── Diff highlighter (line-based, not token-based) ───────────────────────────

function highlightDiff(code: string): string {
  return code.split('\n').map(line => {
    if (line.startsWith('+++') || line.startsWith('---')) return sp('cmt', e(line))
    if (line.startsWith('+')) return sp('str', e(line))
    if (line.startsWith('-')) return sp('kw',  e(line))
    if (line.startsWith('@@')) return sp('fn',  e(line))
    return e(line)
  }).join('\n')
}

// ── General tokenizer ────────────────────────────────────────────────────────
// One regex alternation, matched in priority order so comments beat strings,
// strings beat identifiers. We escape each token individually.

function buildRx(lang: string): RegExp {
  const parts: string[] = []

  // Block comment (C-style)
  if (!['bash', 'sh', 'py', 'python'].includes(lang)) {
    parts.push('/\\*[\\s\\S]*?\\*/')
  }

  // Line comment
  if (['bash', 'sh', 'py', 'python'].includes(lang)) {
    parts.push('#[^\\n]*')
  } else {
    parts.push('//[^\\n]*')
  }

  // Python triple-quoted strings
  if (['py', 'python'].includes(lang)) {
    parts.push('"""[\\s\\S]*?"""', "'''[\\s\\S]*?'''")
  }

  // Template literals (JS/TS)
  if (['ts', 'tsx', 'js', 'jsx'].includes(lang)) {
    parts.push('`(?:[^`\\\\]|\\\\.)*`')
  }

  // Regular strings (single + double)
  parts.push('"(?:[^"\\\\\\n]|\\\\.)*"', "'(?:[^'\\\\\\n]|\\\\.)*'")

  // Hex + decimal numbers
  parts.push('0x[0-9a-fA-F]+', '\\b\\d+\\.?\\d*(?:[eE][+-]?\\d+)?\\b')

  // Identifiers
  parts.push('[A-Za-z_$][A-Za-z0-9_$]*')

  // Fallthrough: one char at a time
  parts.push('[\\s\\S]')

  return new RegExp(parts.join('|'), 'g')
}

// Cache compiled regexes by lang
const RX_CACHE: Record<string, RegExp> = {}
function getRx(lang: string): RegExp {
  if (!RX_CACHE[lang]) RX_CACHE[lang] = buildRx(lang)
  // Reset lastIndex before each use (global flag)
  RX_CACHE[lang].lastIndex = 0
  return RX_CACHE[lang]
}

function tokenize(code: string, lang: string): string {
  const kws = KW[lang] ?? new Set<string>()
  const rx = getRx(lang)
  const isLineComment = ['bash', 'sh', 'py', 'python'].includes(lang) ? '#' : '//'
  let out = ''
  let m: RegExpExecArray | null

  while ((m = rx.exec(code)) !== null) {
    const tok = m[0]
    const escaped = e(tok)

    // Block comment
    if (tok.startsWith('/*')) { out += sp('cmt', escaped); continue }

    // Line comment
    if (tok.startsWith(isLineComment)) { out += sp('cmt', escaped); continue }

    // Python triple-quote
    if ((lang === 'py' || lang === 'python') &&
        (tok.startsWith('"""') || tok.startsWith("'''"))) {
      out += sp('str', escaped); continue
    }

    // Strings and template literals
    if (tok[0] === '"' || tok[0] === "'" || tok[0] === '`') {
      out += sp('str', escaped); continue
    }

    // Numbers
    if (tok[0] === '0' && tok[1] === 'x') { out += sp('num', escaped); continue }
    if (tok[0] >= '0' && tok[0] <= '9')   { out += sp('num', escaped); continue }

    // Identifiers: keyword, function call, or plain
    if (/^[A-Za-z_$][A-Za-z0-9_$]*$/.test(tok)) {
      if (kws.has(tok)) {
        out += sp('kw', escaped)
      } else {
        // Peek ahead: is next non-whitespace char a '('?
        const rest = code.slice(m.index + tok.length)
        if (/^\s*\(/.test(rest)) out += sp('fn', escaped)
        else out += escaped
      }
      continue
    }

    out += escaped
  }

  return out
}

// ── Public API ───────────────────────────────────────────────────────────────

/** Highlight `code` for the given `lang`. Returns HTML string, all content escaped. */
export function highlight(code: string, lang: string): string {
  const l = lang.toLowerCase()
  if (l === 'diff' || l === 'patch') return highlightDiff(code)
  if (KW[l] !== undefined || ['ts', 'tsx', 'js', 'jsx'].includes(l)) return tokenize(code, l)
  // Unknown lang: plain escape
  return e(code)
}
