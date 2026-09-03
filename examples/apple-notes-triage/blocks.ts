// blocks.ts — render + parse the agent's own delimited blocks (discussion #244).
//
// Notes bodies are HTML with no hidden metadata layer, so these blocks are
// plain visible text, delimited so the agent can find and replace only its
// own blocks and never touch the user's dictated content.

import type { QuestionBlock, ReceiptBlock } from './types.ts';

const FILED_HEADER = '--- filed ---';
const FILED_FOOTER = '-------------';
const QUESTION_HEADER = '--- needs a store ---';
const QUESTION_FOOTER = '-------------------';

export const QUESTION_ANSWER_PLACEHOLDER = '(write a store name here, or at the top/bottom of the note)';

function pad2(n: number): string {
  return String(n).padStart(2, '0');
}

// "YYYY-MM-DD HH:MM" in local time — matches the example in discussion #244.
export function formatTimestamp(date: Date): string {
  return `${date.getFullYear()}-${pad2(date.getMonth() + 1)}-${pad2(date.getDate())} ${pad2(date.getHours())}:${pad2(date.getMinutes())}`;
}

export function renderReceiptBlock(store: string, bead: string, date: Date): string {
  return [FILED_HEADER, `store: ${store}`, `bead: ${bead}`, formatTimestamp(date), FILED_FOOTER].join('\n');
}

export function renderQuestionBlock(bestGuesses: string[], reason: string, answer = QUESTION_ANSWER_PLACEHOLDER): string {
  return [
    QUESTION_HEADER,
    `best guesses: ${bestGuesses.join(', ')}`,
    `answer: ${answer}`,
    `reason: ${reason}`,
    QUESTION_FOOTER,
  ].join('\n');
}

interface ExtractResult {
  inner: string;
  rest: string;
}

function extractBetween(body: string, header: string, footer: string): ExtractResult | null {
  const start = body.indexOf(header);
  if (start === -1) return null;
  const footerStart = body.indexOf(footer, start + header.length);
  if (footerStart === -1) return null;
  const end = footerStart + footer.length;
  const inner = body.slice(start + header.length, footerStart);
  const rest = body.slice(0, start) + body.slice(end);
  return { inner, rest };
}

function fieldValue(lines: string[], name: string): string {
  const prefix = `${name}:`;
  const line = lines.find((l) => l.trim().toLowerCase().startsWith(prefix));
  if (!line) return '';
  return line.slice(line.indexOf(':') + 1).trim();
}

function parseReceipt(inner: string): ReceiptBlock {
  const lines = inner
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
  const store = fieldValue(lines, 'store');
  const bead = fieldValue(lines, 'bead');
  const timestamp = lines.find((l) => !l.toLowerCase().startsWith('store:') && !l.toLowerCase().startsWith('bead:')) ?? '';
  return { kind: 'filed', store, bead, timestamp };
}

function parseQuestion(inner: string): QuestionBlock {
  const lines = inner
    .split('\n')
    .map((l) => l.trim())
    .filter(Boolean);
  const bestGuessesRaw = fieldValue(lines, 'best guesses');
  const bestGuesses = bestGuessesRaw
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
  const answer = fieldValue(lines, 'answer');
  const reason = fieldValue(lines, 'reason');
  return { kind: 'question', bestGuesses, answer, reason };
}

export interface ParsedBlocks {
  receipt: ReceiptBlock | null;
  question: QuestionBlock | null;
  // Body with any recognized block(s) removed and whitespace collapsed —
  // this is the text every other module treats as "the note's real content".
  rest: string;
}

export function parseBlocks(body: string): ParsedBlocks {
  let rest = body;
  let receipt: ReceiptBlock | null = null;
  let question: QuestionBlock | null = null;

  const filed = extractBetween(rest, FILED_HEADER, FILED_FOOTER);
  if (filed) {
    receipt = parseReceipt(filed.inner);
    rest = filed.rest;
  }

  const pending = extractBetween(rest, QUESTION_HEADER, QUESTION_FOOTER);
  if (pending) {
    question = parseQuestion(pending.inner);
    rest = pending.rest;
  }

  return { receipt, question, rest: rest.replace(/[ \t]+/g, ' ').replace(/\n{3,}/g, '\n\n').trim() };
}

// The question block's `answer:` field carries a real answer only once the
// user has overwritten the placeholder text.
export function pendingAnswer(question: QuestionBlock | null): string | null {
  if (!question) return null;
  if (!question.answer || question.answer === QUESTION_ANSWER_PLACEHOLDER) return null;
  return question.answer;
}

export function prependBlock(rest: string, block: string): string {
  return rest ? `${block}\n\n${rest}` : block;
}
