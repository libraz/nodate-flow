import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import Markdown from './markdown';

const URL = 'https://evil.example/p.png';

describe('Markdown images', () => {
  it('renders img by default', () => {
    const { container } = render(<Markdown>{`![a](${URL})`}</Markdown>);
    expect(container.querySelector('img')).not.toBeNull();
  });

  it('turns an inline image into a link when allowImages is false', () => {
    const { container } = render(<Markdown allowImages={false}>{`![alt text](${URL})`}</Markdown>);
    expect(container.querySelector('img')).toBeNull();
    const a = container.querySelector('a');
    expect(a?.getAttribute('href')).toBe(URL);
    expect(a?.textContent).toBe('alt text');
  });

  it('turns a reference-style image into a link when allowImages is false', () => {
    const { container } = render(
      <Markdown allowImages={false}>{`![pic][r]\n\n[r]: ${URL}`}</Markdown>,
    );
    expect(container.querySelector('img')).toBeNull();
    expect(container.querySelector('a')?.getAttribute('href')).toBe(URL);
  });

  it('uses the URL as link text when alt is empty', () => {
    const { container } = render(<Markdown allowImages={false}>{`![](${URL})`}</Markdown>);
    expect(container.querySelector('a')?.textContent).toBe(URL);
  });
});
