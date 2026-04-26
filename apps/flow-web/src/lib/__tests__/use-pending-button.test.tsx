import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';

import { type PendingMutationLike, usePendingButton } from '../use-pending-button';

interface ProbeProps {
  mutation: PendingMutationLike;
  onClick?: () => void;
  extraDisabled?: readonly boolean[];
}

function Probe({ mutation, onClick, extraDisabled }: ProbeProps): ReactElement {
  const props = usePendingButton(mutation, onClick, extraDisabled);
  return (
    <button type="button" {...props}>
      go
    </button>
  );
}

function getButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: 'go' }) as HTMLButtonElement;
}

describe('usePendingButton', () => {
  it('marks the button disabled and busy while the mutation is pending', () => {
    render(<Probe mutation={{ isPending: true }} onClick={() => undefined} />);
    const btn = getButton();
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute('aria-busy')).toBe('true');
  });

  it('treats legacy isLoading as pending', () => {
    render(<Probe mutation={{ isLoading: true }} onClick={() => undefined} />);
    const btn = getButton();
    expect(btn.disabled).toBe(true);
    expect(btn.getAttribute('aria-busy')).toBe('true');
  });

  it('passes the click through when not pending', () => {
    const onClick = vi.fn();
    render(<Probe mutation={{ isPending: false }} onClick={onClick} />);
    fireEvent.click(getButton());
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it('blocks clicks while pending even if the underlying button bypasses disabled', () => {
    const onClick = vi.fn();
    render(<Probe mutation={{ isPending: true }} onClick={onClick} />);
    // Force the click despite disabled to validate the wrapped guard.
    const btn = getButton();
    btn.removeAttribute('disabled');
    fireEvent.click(btn);
    expect(onClick).not.toHaveBeenCalled();
  });

  it('disables when any extra gate is truthy', () => {
    render(
      <Probe
        mutation={{ isPending: false }}
        onClick={() => undefined}
        extraDisabled={[false, true, false]}
      />,
    );
    expect(getButton().disabled).toBe(true);
  });

  it('keeps onClick undefined when none is supplied', () => {
    render(<Probe mutation={{ isPending: false }} />);
    // Should still render and not throw on click.
    fireEvent.click(getButton());
  });
});
