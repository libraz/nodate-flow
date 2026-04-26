/**
 * Component tests for AuditLogComingSoon.
 *
 * The empty state stands in for the audit log route while the backend
 * handler is not yet registered. These tests assert the structural
 * shape (status role, two paragraphs of i18n copy) and the absence of
 * a11y violations.
 */

import { screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';

import { renderWithProviders } from '../../../test/helpers/render';
import AuditLogComingSoon from '../audit-log-coming-soon';

describe('<AuditLogComingSoon>', () => {
  it('renders the audit log heading from the settings namespace', () => {
    renderWithProviders(<AuditLogComingSoon />);

    const heading = screen.getByRole('heading', { level: 1 });
    expect(heading.textContent).toBe('audit_log.title');
  });

  it('renders the coming-soon title and body from the audit.empty keys', () => {
    renderWithProviders(<AuditLogComingSoon />);

    expect(screen.getByText('audit.empty.title')).toBeDefined();
    expect(screen.getByText('audit.empty.body')).toBeDefined();
  });

  it('exposes the empty state as a status region for assistive tech', () => {
    renderWithProviders(<AuditLogComingSoon />);

    const status = screen.getByRole('status');
    expect(status).toBeDefined();
    expect(status.textContent).toContain('audit.empty.title');
    expect(status.textContent).toContain('audit.empty.body');
  });

  it('has no a11y violations', async () => {
    const { container } = renderWithProviders(<AuditLogComingSoon />);
    const results = await axe(container);
    expect(results).toHaveNoViolations();
  });
});
