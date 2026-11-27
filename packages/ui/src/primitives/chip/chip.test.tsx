import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { axe } from 'vitest-axe';
import Chip from './chip';

describe('Chip custom colour', () => {
  it('passes the colour through --chip-color only', () => {
    render(<Chip color="#ff8800">Urgent</Chip>);
    const root = screen.getByText('Urgent').parentElement as HTMLElement;
    expect(root.style.getPropertyValue('--chip-color')).toBe('#ff8800');
    expect(root.style.length).toBe(1);
    expect(root.querySelector('[aria-hidden]')).not.toBeNull();
  });

  it('puts the full text in title and keeps a caller title', () => {
    const { rerender } = render(<Chip color="red">Long label</Chip>);
    expect(screen.getByTitle('Long label')).toBeTruthy();
    rerender(
      <Chip color="red" title="Custom">
        Long label
      </Chip>,
    );
    expect(screen.getByTitle('Custom')).toBeTruthy();
  });

  it('renders no dot or custom property without a colour', () => {
    render(<Chip>Plain</Chip>);
    const root = screen.getByText('Plain').parentElement as HTMLElement;
    expect(root.style.getPropertyValue('--chip-color')).toBe('');
    expect(root.querySelector('[aria-hidden]')).toBeNull();
  });

  it('keeps dismiss behaviour and has no a11y violations', async () => {
    const onDismiss = vi.fn();
    const { container } = render(
      <Chip color="oklch(0.8 0.1 200)" onDismiss={onDismiss} dismissLabel="Remove label">
        Bug
      </Chip>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Remove label' }));
    expect(onDismiss).toHaveBeenCalledOnce();
    expect(await axe(container)).toHaveNoViolations();
  });
});

describe('Chip size', () => {
  it('applies the sm class only when requested', () => {
    const { rerender } = render(<Chip size="sm">A</Chip>);
    const sm = (screen.getByText('A').parentElement as HTMLElement).className;
    rerender(<Chip>A</Chip>);
    const md = (screen.getByText('A').parentElement as HTMLElement).className;
    expect(sm).not.toBe(md);
  });

  it('keeps the dismiss button in sm', async () => {
    const onDismiss = vi.fn();
    render(
      <Chip size="sm" onDismiss={onDismiss} dismissLabel="Remove">
        A
      </Chip>,
    );
    await userEvent.click(screen.getByRole('button', { name: 'Remove' }));
    expect(onDismiss).toHaveBeenCalledOnce();
  });
});
