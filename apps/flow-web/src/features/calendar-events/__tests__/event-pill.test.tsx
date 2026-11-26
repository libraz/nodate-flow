import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { EventPillPreview, eventColorStyle, eventPillClassName } from '../event-pill';

describe('eventColorStyle', () => {
  it('returns only the --evt variable', () => {
    expect(eventColorStyle('#47B2F7')).toEqual({ '--evt': '#47B2F7' });
  });
});

describe('eventPillClassName', () => {
  it('gives each kind its own treatment class', () => {
    const classes = ['event', 'block', 'free', 'milestone'].map(eventPillClassName);
    expect(new Set(classes).size).toBe(4);
  });

  it('reads an unknown kind as event', () => {
    expect(eventPillClassName('mystery')).toBe(eventPillClassName('event'));
  });
});

describe('EventPillPreview', () => {
  it('paints the title and exposes the colour as --evt', () => {
    render(<EventPillPreview color="#F35F8C" title="Client visit" />);
    const pill = screen.getByTestId('event-pill-preview');
    expect(pill.style.getPropertyValue('--evt')).toBe('#F35F8C');
    expect(pill.textContent).toBe('Client visit');
  });
});
