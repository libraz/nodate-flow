import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it } from 'vitest';
import { authApiBaseUrl } from '../../../lib/sdk';
import CreatorChip from '../creator-chip';

describe('CreatorChip avatar', () => {
  it('resolves a root-relative avatar against the auth API', () => {
    const { container } = renderWithProviders(
      <CreatorChip displayName="Ada Lovelace" avatarUrl="/users/u-1/avatar?v=2" />,
    );
    const img = container.querySelector('img');
    expect(img?.getAttribute('src')).toBe(
      new URL('/users/u-1/avatar?v=2', authApiBaseUrl).toString(),
    );
  });

  it('keeps an absolute avatar URL unchanged', () => {
    const { container } = renderWithProviders(
      <CreatorChip displayName="Ada Lovelace" avatarUrl="https://cdn.example.test/a.png" />,
    );
    expect(container.querySelector('img')?.getAttribute('src')).toBe(
      'https://cdn.example.test/a.png',
    );
  });
});
