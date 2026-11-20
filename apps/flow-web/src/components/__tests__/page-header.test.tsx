/** Component tests for PageHeader's help integration. */

import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import PageHeader from '../page-header';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: { defaultValue?: unknown }) =>
      key === 'demo.summary' ? 'Demo summary' : (opts?.defaultValue ?? key),
  }),
}));

describe('<PageHeader> help', () => {
  it('renders no help button by default', () => {
    render(<PageHeader title="Inbox" />);
    expect(screen.queryByRole('button', { name: 'help.trigger' })).toBeNull();
    expect(screen.getByRole('heading', { level: 1, name: 'Inbox' })).not.toBeNull();
  });

  it('adds the button and uses the topic summary as description', () => {
    render(<PageHeader title="Inbox" help="demo" />);
    expect(screen.getByRole('button', { name: 'help.trigger' })).not.toBeNull();
    expect(screen.getByRole('heading', { level: 1, name: 'Inbox' })).not.toBeNull();
    expect(screen.getByText('Demo summary')).not.toBeNull();
  });

  it('prefers an explicit description over the summary', () => {
    render(<PageHeader title="Inbox" help="demo" description="Own text" />);
    expect(screen.getByText('Own text')).not.toBeNull();
    expect(screen.queryByText('Demo summary')).toBeNull();
  });

  it('shows no description when the topic has no summary', () => {
    render(<PageHeader title="Inbox" help="missing" />);
    expect(screen.queryByText('missing.summary')).toBeNull();
  });
});
