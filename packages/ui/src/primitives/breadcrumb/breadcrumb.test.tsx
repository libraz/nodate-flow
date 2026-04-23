import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import { Breadcrumb, BreadcrumbItem, BreadcrumbSeparator } from './breadcrumb';

const THEMES = ['aurora-light', 'aurora-dark', 'dotline-light', 'dotline-dark'] as const;

function Trail(): ReactElement {
  return (
    <Breadcrumb>
      <BreadcrumbItem href="/workspaces/123">Acme</BreadcrumbItem>
      <BreadcrumbSeparator />
      <BreadcrumbItem href="/workspaces/123/projects/abc">Demo Project</BreadcrumbItem>
      <BreadcrumbSeparator />
      <BreadcrumbItem>Tasks</BreadcrumbItem>
    </Breadcrumb>
  );
}

describe.each(THEMES)('Breadcrumb [%s]', (theme) => {
  beforeEach(() => {
    document.documentElement.setAttribute('data-theme', theme);
  });
  afterEach(() => {
    document.documentElement.removeAttribute('data-theme');
  });

  it('renders a <nav aria-label="breadcrumb"> wrapping an <ol>', () => {
    render(<Trail />);
    const nav = screen.getByRole('navigation', { name: 'breadcrumb' });
    expect(nav.tagName).toBe('NAV');
    const list = nav.querySelector('ol');
    expect(list).not.toBeNull();
  });

  it('uses a custom accessible label when provided', () => {
    render(
      <Breadcrumb label="Page hierarchy">
        <BreadcrumbItem href="/a">A</BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>B</BreadcrumbItem>
      </Breadcrumb>,
    );
    expect(screen.getByRole('navigation', { name: 'Page hierarchy' })).toBeDefined();
  });

  it('renders link items as <a href>', () => {
    render(<Trail />);
    const link = screen.getByRole('link', { name: 'Acme' });
    expect(link.tagName).toBe('A');
    expect(link.getAttribute('href')).toBe('/workspaces/123');
  });

  it('marks the last item without href as aria-current="page"', () => {
    render(<Trail />);
    const current = screen.getByText('Tasks');
    expect(current.getAttribute('aria-current')).toBe('page');
  });

  it('does not mark intermediate link items as aria-current', () => {
    render(<Trail />);
    const intermediate = screen.getByRole('link', { name: 'Demo Project' });
    expect(intermediate.getAttribute('aria-current')).toBeNull();
  });

  it('forces aria-current when current={true} on a non-last item', () => {
    render(
      <Breadcrumb>
        <BreadcrumbItem current href="/a">
          A
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem href="/b">B</BreadcrumbItem>
      </Breadcrumb>,
    );
    // With current=true the item renders as a <span>, not a <a>.
    const current = screen.getByText('A');
    expect(current.tagName).toBe('SPAN');
    expect(current.getAttribute('aria-current')).toBe('page');
  });

  it('renders separators as <li aria-hidden="true"> with the default glyph', () => {
    render(<Trail />);
    const separators = screen
      .getByRole('navigation', { name: 'breadcrumb' })
      .querySelectorAll('li[aria-hidden="true"]');
    expect(separators).toHaveLength(2);
    expect(separators[0]?.textContent).toBe('›');
  });

  it('accepts a custom separator glyph via children', () => {
    render(
      <Breadcrumb>
        <BreadcrumbItem href="/a">A</BreadcrumbItem>
        <BreadcrumbSeparator>/</BreadcrumbSeparator>
        <BreadcrumbItem>B</BreadcrumbItem>
      </Breadcrumb>,
    );
    const sep = screen
      .getByRole('navigation', { name: 'breadcrumb' })
      .querySelector('li[aria-hidden="true"]');
    expect(sep?.textContent).toBe('/');
  });

  it('fires the onClick handler when an item link is activated', async () => {
    const user = userEvent.setup();
    const fn = vi.fn();
    render(
      <Breadcrumb>
        <BreadcrumbItem href="/a" onClick={fn}>
          A
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>B</BreadcrumbItem>
      </Breadcrumb>,
    );
    await user.click(screen.getByRole('link', { name: 'A' }));
    expect(fn).toHaveBeenCalledTimes(1);
  });

  it('asChild clones the child element and applies the link class', () => {
    render(
      <Breadcrumb>
        <BreadcrumbItem asChild>
          <a href="/custom" data-testid="custom-link">
            Custom
          </a>
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>End</BreadcrumbItem>
      </Breadcrumb>,
    );
    const link = screen.getByTestId('custom-link');
    expect(link.tagName).toBe('A');
    expect(link.getAttribute('href')).toBe('/custom');
    // className is merged (non-empty) so consumers inherit muted-link styling.
    expect(link.className.length).toBeGreaterThan(0);
  });

  it('asChild merges onClick from both the wrapper and the child element', async () => {
    const user = userEvent.setup();
    const wrapper = vi.fn();
    const inner = vi.fn();
    render(
      <Breadcrumb>
        <BreadcrumbItem asChild onClick={wrapper}>
          <a
            href="/custom"
            onClick={(e) => {
              e.preventDefault();
              inner();
            }}
          >
            Custom
          </a>
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>End</BreadcrumbItem>
      </Breadcrumb>,
    );
    await user.click(screen.getByRole('link', { name: 'Custom' }));
    expect(wrapper).toHaveBeenCalledTimes(1);
    expect(inner).toHaveBeenCalledTimes(1);
  });

  it('has no a11y violations (full trail)', async () => {
    const { container } = render(<Trail />);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('has no a11y violations (asChild variant)', async () => {
    const { container } = render(
      <Breadcrumb label="custom">
        <BreadcrumbItem asChild>
          <a href="/one">One</a>
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem asChild>
          <a href="/two">Two</a>
        </BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>Three</BreadcrumbItem>
      </Breadcrumb>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });
});
