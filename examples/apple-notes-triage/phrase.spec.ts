import { describe, expect, test } from 'bun:test';
import { findTrigger } from './phrase.ts';

describe('findTrigger', () => {
  test('finds the phrase at the end of a note', () => {
    const m = findTrigger('Pick up milk\ncommander triage');
    expect(m.found).toBe(true);
    expect(m.argument).toBeNull();
    expect(m.withoutTrigger).toBe('Pick up milk');
  });

  test('is case-insensitive', () => {
    expect(findTrigger('Commander Triage').found).toBe(true);
    expect(findTrigger('COMMANDER TRIAGE').found).toBe(true);
  });

  test('captures a store-name argument', () => {
    const m = findTrigger('Buy a card\ncommander triage task');
    expect(m.found).toBe(true);
    expect(m.argument).toBe('task');
    expect(m.withoutTrigger).toBe('Buy a card');
  });

  test('matches mid-text, not just at the end', () => {
    const m = findTrigger('commander triage task\nBuy a card for the party');
    expect(m.found).toBe(true);
    expect(m.argument).toBe('task');
    expect(m.withoutTrigger).toBe('Buy a card for the party');
  });

  test('tolerates a dictation comma between words', () => {
    const m = findTrigger('Buy milk\ncommander, triage');
    expect(m.found).toBe(true);
  });

  test('tolerates trailing punctuation', () => {
    const m = findTrigger('Buy milk\ncommander triage.');
    expect(m.found).toBe(true);
    expect(m.withoutTrigger).toBe('Buy milk');
  });

  test('is configurable to a different phrase', () => {
    const m = findTrigger('Buy milk\nfile this away', 'file this away');
    expect(m.found).toBe(true);
    expect(m.withoutTrigger).toBe('Buy milk');
  });

  test('does not match when the phrase is absent', () => {
    const m = findTrigger('Just a regular note with no trigger');
    expect(m.found).toBe(false);
    expect(m.argument).toBeNull();
    expect(m.withoutTrigger).toBe('Just a regular note with no trigger');
  });

  test('does not treat unrelated text as an argument across a newline', () => {
    const m = findTrigger('commander triage\nBuy a card for the party');
    expect(m.found).toBe(true);
    expect(m.argument).toBeNull();
    expect(m.withoutTrigger).toBe('Buy a card for the party');
  });
});
