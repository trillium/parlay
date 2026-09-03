import { describe, expect, test } from 'bun:test';
import {
  parseBlocks,
  pendingAnswer,
  prependBlock,
  QUESTION_ANSWER_PLACEHOLDER,
  renderQuestionBlock,
  renderReceiptBlock,
} from './blocks.ts';

describe('receipt block round-trip', () => {
  test('renders and re-parses to the same fields', () => {
    const date = new Date('2026-09-03T14:22:00');
    const rendered = renderReceiptBlock('task', 'task-ab12', date);
    const body = prependBlock('Pick up milk', rendered);
    const parsed = parseBlocks(body);

    expect(parsed.receipt).toEqual({ kind: 'filed', store: 'task', bead: 'task-ab12', timestamp: '2026-09-03 14:22' });
    expect(parsed.question).toBeNull();
    expect(parsed.rest).toBe('Pick up milk');
  });
});

describe('question block round-trip', () => {
  test('renders with the placeholder answer and re-parses', () => {
    const rendered = renderQuestionBlock(['ideas', 'task'], 'ambiguous');
    const body = prependBlock('hmm not sure', rendered);
    const parsed = parseBlocks(body);

    expect(parsed.question).toEqual({
      kind: 'question',
      bestGuesses: ['ideas', 'task'],
      answer: QUESTION_ANSWER_PLACEHOLDER,
      reason: 'ambiguous',
    });
    expect(parsed.rest).toBe('hmm not sure');
    expect(pendingAnswer(parsed.question)).toBeNull();
  });

  test('an overwritten answer is a real pending answer', () => {
    const rendered = renderQuestionBlock(['ideas'], 'ambiguous', 'task');
    const parsed = parseBlocks(rendered);
    expect(pendingAnswer(parsed.question)).toBe('task');
  });
});

describe('parseBlocks on plain notes', () => {
  test('a note with neither block leaves rest untouched (modulo whitespace collapse)', () => {
    const parsed = parseBlocks('Just a plain note');
    expect(parsed.receipt).toBeNull();
    expect(parsed.question).toBeNull();
    expect(parsed.rest).toBe('Just a plain note');
  });

  test('collapses excess blank lines and trims', () => {
    const parsed = parseBlocks('\n\n\nPadded note\n\n\n');
    expect(parsed.rest).toBe('Padded note');
  });
});

describe('pendingAnswer', () => {
  test('null for a null question', () => {
    expect(pendingAnswer(null)).toBeNull();
  });
});
