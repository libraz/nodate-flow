import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { DEFAULT_APP_NAME } from '../../brand/brand';
import AuthCard from './auth-card';

describe('AuthCard brand', () => {
  it('renders the app name as the wordmark and no img without a logo', () => {
    const { container } = render(<AuthCard appName={DEFAULT_APP_NAME}>body</AuthCard>);
    expect(screen.getByText(DEFAULT_APP_NAME)).toBeTruthy();
    expect(container.querySelector('img')).toBeNull();
  });

  it('renders a decorative logo img before the wordmark', () => {
    const { container } = render(
      <AuthCard appName="Acme" logoUrl="https://cdn.example.com/logo.svg">
        body
      </AuthCard>,
    );
    const img = container.querySelector('img[alt=""]');
    expect(img?.getAttribute('src')).toBe('https://cdn.example.com/logo.svg');
    const wordmark = screen.getByText('Acme');
    expect(img?.compareDocumentPosition(wordmark)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  });
});
