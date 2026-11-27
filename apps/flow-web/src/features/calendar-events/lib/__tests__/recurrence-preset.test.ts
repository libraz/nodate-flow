import { describe, expect, it } from 'vitest';
import { presetToRRule, rruleToPreset, weekdayName } from '../recurrence-preset';

describe('weekdayName', () => {
  it('names the weekday in the language being read', () => {
    expect(weekdayName('2030-06-17', 'en')).toBe('Monday');
    expect(weekdayName('2030-06-17', 'ja')).toBe('月曜日');
    expect(weekdayName('2030-06-17', 'zh')).toBe('星期一');
  });

  it('tracks the date it is given', () => {
    expect(weekdayName('2030-06-15', 'en')).toBe('Saturday');
    expect(weekdayName('2030-06-16', 'en')).toBe('Sunday');
  });

  it('says nothing rather than guessing when the value is not a date', () => {
    expect(weekdayName('', 'en')).toBe('');
    expect(weekdayName('not-a-date', 'en')).toBe('');
  });
});

describe('presetToRRule', () => {
  it('returns null for the none preset', () => {
    expect(presetToRRule('none', '2030-06-15')).toBeNull();
  });

  it('emits canonical lowercase freq tokens matching the expander contract', () => {
    expect(presetToRRule('daily', '2030-06-15')).toEqual({ freq: 'daily' });
    expect(presetToRRule('weekly', '2030-06-15')).toEqual({ freq: 'weekly' });
    expect(presetToRRule('monthly', '2030-06-15')).toEqual({ freq: 'monthly' });
    expect(presetToRRule('yearly', '2030-06-15')).toEqual({ freq: 'yearly' });
  });

  it('emits the uppercase weekday codes the shape CHECK stores for the weekdays preset', () => {
    expect(presetToRRule('weekdays', '2030-06-15')).toEqual({
      freq: 'weekly',
      byDay: ['MO', 'TU', 'WE', 'TH', 'FR'],
    });
  });

  it('never emits an uppercase freq token', () => {
    for (const preset of ['daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const) {
      const rule = presetToRRule(preset, '2030-06-15');
      expect(rule).not.toBeNull();
      const freq = (rule as { freq: string }).freq;
      expect(freq).toBe(freq.toLowerCase());
    }
  });
});

describe('rruleToPreset', () => {
  it('reports no repeat only when there is genuinely no rule', () => {
    expect(rruleToPreset(null)).toBe('none');
    expect(rruleToPreset(undefined)).toBe('none');
  });

  it('round-trips every preset the control can author', () => {
    for (const preset of ['daily', 'weekdays', 'weekly', 'monthly', 'yearly'] as const) {
      const rule = presetToRRule(preset, '2030-06-15');
      expect(rruleToPreset(rule)).toBe(preset);
    }
  });

  it('accepts uppercase weekday tokens, which RFC 5545 rules carry', () => {
    expect(rruleToPreset({ freq: 'weekly', byDay: ['MO', 'TU', 'WE', 'TH', 'FR'] })).toBe(
      'weekdays',
    );
  });

  it('refuses to flatten a rule a preset would silently truncate', () => {
    // Each of these says something no preset can, so answering with one
    // would drop that part of the rule the moment the user saved.
    expect(rruleToPreset({ freq: 'weekly', interval: 2 })).toBe('custom');
    expect(rruleToPreset({ freq: 'daily', count: 10 })).toBe('custom');
    expect(rruleToPreset({ freq: 'daily', until: '2030-12-31' })).toBe('custom');
    expect(rruleToPreset({ freq: 'monthly', byMonthDay: [1, 15] })).toBe('custom');
    expect(rruleToPreset({ freq: 'weekly', byDay: ['MO', 'WE'] })).toBe('custom');
    expect(rruleToPreset({ freq: 'daily', byDay: ['MO'] })).toBe('custom');
    expect(rruleToPreset({ freq: 'monthly', byDay: ['MO'], bySetPos: [2] })).toBe('custom');
  });

  it('never authors a rule for the value it uses to describe one', () => {
    // Selecting 'custom' has to be a no-op: it names a rule this control
    // did not write and cannot rebuild.
    expect(presetToRRule('custom', '2030-06-15')).toBeNull();
  });
});
