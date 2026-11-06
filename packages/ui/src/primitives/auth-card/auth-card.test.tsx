import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { DEFAULT_APP_NAME } from '../../brand/brand';
import AuthCard from './auth-card';

const asset = { src: 'https://cdn.example.com/a.svg', darkSrc: undefined };

describe('AuthCard brand', () => {
  it('renders the brand mark and the wordmark with neither logo nor icon', () => {
    const { container } = render(<AuthCard appName={DEFAULT_APP_NAME}>body</AuthCard>);
    expect(screen.getByText(DEFAULT_APP_NAME)).toBeTruthy();
    expect(container.querySelector('[data-brand-mark="lg"]')).not.toBeNull();
    expect(container.querySelector('img')).toBeNull();
  });

  it('renders only the logo, with the app name as alt and no wordmark', () => {
    const { container } = render(
      <AuthCard appName="Acme" logo={asset}>
        body
      </AuthCard>,
    );
    const img = container.querySelector('img');
    expect(img?.getAttribute('src')).toBe(asset.src);
    expect(img?.getAttribute('alt')).toBe('Acme');
    expect(screen.queryByText('Acme')).toBeNull();
    expect(container.querySelector('[data-brand-mark]')).toBeNull();
  });

  it('renders a decorative icon before the wordmark when only an icon is set', () => {
    const { container } = render(
      <AuthCard appName="Acme" icon={asset}>
        body
      </AuthCard>,
    );
    const img = container.querySelector('img[alt=""]');
    expect(img?.getAttribute('src')).toBe(asset.src);
    expect(container.querySelector('[data-brand-mark]')).toBeNull();
    const wordmark = screen.getByText('Acme');
    expect(img?.compareDocumentPosition(wordmark)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });

  it('prefers the logo when both are set', () => {
    const { container } = render(
      <AuthCard
        appName="Acme"
        logo={asset}
        icon={{ src: 'https://cdn.example.com/i.svg', darkSrc: undefined }}
      >
        body
      </AuthCard>,
    );
    expect(container.querySelectorAll('img')).toHaveLength(1);
    expect(screen.queryByText('Acme')).toBeNull();
  });
});
