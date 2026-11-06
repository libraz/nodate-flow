import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import BrandImage from './brand-image';

describe('BrandImage', () => {
  it('renders one img without a dark source', () => {
    const { container } = render(
      <BrandImage asset={{ src: 'https://cdn.example/l.svg', darkSrc: undefined }} alt="Acme" />,
    );
    const imgs = container.querySelectorAll('img');
    expect(imgs).toHaveLength(1);
    expect(imgs[0]?.getAttribute('src')).toBe('https://cdn.example/l.svg');
    expect(imgs[0]?.getAttribute('alt')).toBe('Acme');
  });

  it('renders a light and a dark img with the same alt', () => {
    const { container } = render(
      <BrandImage
        asset={{ src: 'https://cdn.example/l.svg', darkSrc: 'https://cdn.example/d.svg' }}
        alt="Acme"
        className="caller"
      />,
    );
    const imgs = container.querySelectorAll('img');
    expect(imgs).toHaveLength(2);
    expect(imgs[0]?.getAttribute('src')).toBe('https://cdn.example/l.svg');
    expect(imgs[1]?.getAttribute('src')).toBe('https://cdn.example/d.svg');
    expect([...imgs].map((i) => i.getAttribute('alt'))).toEqual(['Acme', 'Acme']);
    expect(imgs[0]?.className).not.toBe(imgs[1]?.className);
    expect(container.firstElementChild?.className).toContain('caller');
  });
});
