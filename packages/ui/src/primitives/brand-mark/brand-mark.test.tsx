import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import BrandMark from './brand-mark';

describe('BrandMark', () => {
  it('renders the lg mark with a halo and a glyph, hidden from assistive tech', () => {
    const { container } = render(<BrandMark size="lg" />);
    const mark = container.querySelector('[data-brand-mark="lg"]');
    expect(mark?.getAttribute('aria-hidden')).toBe('true');
    expect(mark?.querySelector('span')).not.toBeNull();
    expect(mark?.querySelector('svg')).not.toBeNull();
  });

  it('renders the sm mark without a halo, hidden from assistive tech', () => {
    const { container } = render(<BrandMark size="sm" />);
    const mark = container.querySelector('[data-brand-mark="sm"]');
    expect(mark?.getAttribute('aria-hidden')).toBe('true');
    expect(mark?.querySelector('span')).toBeNull();
    expect(mark?.querySelector('svg')).not.toBeNull();
  });
});
