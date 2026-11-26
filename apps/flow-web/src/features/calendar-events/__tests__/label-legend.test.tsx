import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { type ReactElement, useState } from 'react';
import { describe, expect, it } from 'vitest';

import LabelLegend, {
  deriveLegend,
  eventLabelKey,
  type LegendEvent,
  NO_LABEL_KEY,
} from '../label-legend';

function ev(labelId: string | null, labelName: string | null, color = '#111111'): LegendEvent {
  return { labelId, labelName, color };
}

describe('deriveLegend', () => {
  it('returns null when no event carries a label', () => {
    expect(deriveLegend([ev(null, null), ev(null, null)], 'en')).toBeNull();
    expect(deriveLegend([], 'en')).toBeNull();
  });

  it('lists each label once, with its colour, sorted by name', () => {
    const legend = deriveLegend(
      [ev('b', 'Zeta', '#222222'), ev('a', 'Alpha', '#333333'), ev('b', 'Zeta', '#222222')],
      'en',
    );
    expect(legend?.labels).toEqual([
      { key: 'a', name: 'Alpha', color: '#333333' },
      { key: 'b', name: 'Zeta', color: '#222222' },
    ]);
    expect(legend?.hasUnlabelled).toBe(false);
  });

  it('flags unlabelled events only alongside labels', () => {
    const legend = deriveLegend([ev('a', 'Alpha'), ev(null, null)], 'en');
    expect(legend?.hasUnlabelled).toBe(true);
  });

  it('treats events with hidden details as unlabelled', () => {
    const hidden = ev('a', null);
    expect(eventLabelKey(hidden)).toBe(NO_LABEL_KEY);
    expect(deriveLegend([hidden], 'en')).toBeNull();
    expect(deriveLegend([hidden, ev('b', 'Beta')], 'en')?.hasUnlabelled).toBe(true);
  });
});

const EVENTS: (LegendEvent & { id: string })[] = [
  { id: 'e1', ...ev('a', 'Alpha') },
  { id: 'e2', ...ev('b', 'Beta') },
  { id: 'e3', ...ev(null, null) },
  { id: 'e4', ...ev('a', null) },
];

/** Holds the hidden set and lists the events that survive it. */
function Harness(): ReactElement {
  const [hidden, setHidden] = useState<ReadonlySet<string>>(() => new Set());
  const legend = deriveLegend(EVENTS, 'en');
  const visible = EVENTS.filter((e) => !hidden.has(eventLabelKey(e)));
  return (
    <>
      {legend ? <LabelLegend legend={legend} hidden={hidden} onHiddenChange={setHidden} /> : null}
      <output data-testid="visible">{visible.map((e) => e.id).join(',')}</output>
    </>
  );
}

describe('LabelLegend', () => {
  it('names the group from the visible caption without repeating it', () => {
    renderWithProviders(<Harness />);
    const group = screen.getByRole('group', { name: 'calendar.labelFilter.group' });
    expect(group.hasAttribute('aria-label')).toBe(false);
    const caption = document.getElementById(group.getAttribute('aria-labelledby') ?? '');
    expect(caption?.textContent).toBe('calendar.labelFilter.group');
  });

  it('gives the no-label chip a neutral colour', () => {
    renderWithProviders(<Harness />);
    const chip = screen.getByRole('button', { name: /labelFilter\.none/ });
    expect(chip.style.getPropertyValue('--chip-color')).toBe('var(--nf-color-fg-muted)');
  });

  it('filters events as chips toggle and Show all restores them', async () => {
    const user = userEvent.setup();
    renderWithProviders(<Harness />);
    const visible = (): string | null => screen.getByTestId('visible').textContent;

    expect(screen.queryByRole('button', { name: /showAll/ })).toBeNull();
    expect(visible()).toBe('e1,e2,e3,e4');

    await user.click(screen.getByRole('button', { name: 'Alpha' }));
    expect(screen.getByRole('button', { name: 'Alpha' }).getAttribute('aria-pressed')).toBe(
      'false',
    );
    expect(visible()).toBe('e2,e3,e4');

    await user.click(screen.getByRole('button', { name: /noneLabel|labelFilter\.none/ }));
    expect(visible()).toBe('e2');

    await user.click(screen.getByRole('button', { name: /showAll/ }));
    expect(visible()).toBe('e1,e2,e3,e4');
    expect(screen.queryByRole('button', { name: /showAll/ })).toBeNull();
  });
});
