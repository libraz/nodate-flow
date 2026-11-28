/** Component tests for AttachmentThumbnail and ImageGallery. */

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Suspense } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { AttachmentThumbnail, LazyImageGallery } from '../image-gallery';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: Record<string, string>) =>
      key === 'gallery.labels.index_of_total'
        ? `${opts?.index} of ${opts?.total}`
        : opts?.filename
          ? `${key}:${opts.filename}`
          : key,
  }),
}));

afterEach(() => {
  document.documentElement.removeAttribute('data-theme');
});

describe('<AttachmentThumbnail>', () => {
  it('renders the image and reports load errors when ready', async () => {
    const onImageError = vi.fn();
    const { container } = render(
      <AttachmentThumbnail
        filename="a.png"
        status="ready"
        src="https://blob.example/a.png"
        onImageError={onImageError}
        onOpen={vi.fn()}
      />,
    );
    const img = container.querySelector('img');
    expect(img?.getAttribute('src')).toBe('https://blob.example/a.png');
    expect(img?.getAttribute('alt')).toBe('');
    img?.dispatchEvent(new Event('error'));
    expect(onImageError).toHaveBeenCalledTimes(1);
  });

  it('passes the clicked button to onOpen', async () => {
    const onOpen = vi.fn();
    render(
      <AttachmentThumbnail
        filename="a.png"
        status="loading"
        onImageError={vi.fn()}
        onOpen={onOpen}
      />,
    );
    const button = screen.getByRole('button', { name: 'gallery.open_preview:a.png' });
    await userEvent.click(button);
    expect(onOpen).toHaveBeenCalledWith(button);
  });

  it('shows an icon button with a hint when oversized', async () => {
    const onOpen = vi.fn();
    const { container } = render(
      <AttachmentThumbnail
        filename="big.png"
        status="oversized"
        onImageError={vi.fn()}
        onOpen={onOpen}
      />,
    );
    const button = screen.getByRole('button', { name: 'gallery.open_preview:big.png' });
    expect(button.getAttribute('title')).toBe('gallery.too_large_hint');
    expect(container.querySelector('img')).toBeNull();
    await userEvent.click(button);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it('is an inert, non-interactive slot when unavailable', () => {
    const { container } = render(
      <AttachmentThumbnail
        filename="a.png"
        status="unavailable"
        onImageError={vi.fn()}
        onOpen={vi.fn()}
      />,
    );
    expect(screen.queryByRole('button')).toBeNull();
    expect(container.querySelector('img')).toBeNull();
  });
});

const slides = [
  { id: 'a1', src: 'https://blob.example/a.png', filename: 'a.png' },
  { id: 'b1', src: 'https://blob.example/b.png', filename: 'b.png' },
];

function renderGallery(
  overrides: Partial<{ index: number; onClose: () => void; onDownload: (id: string) => void }> = {},
): void {
  render(
    <Suspense fallback={null}>
      <LazyImageGallery
        slides={slides}
        index={overrides.index ?? 0}
        onClose={overrides.onClose ?? vi.fn()}
        onDownload={overrides.onDownload ?? vi.fn()}
        onExited={vi.fn()}
      />
    </Suspense>,
  );
}

describe('<ImageGallery>', () => {
  it('opens on the requested slide with translated controls and routes download by id', async () => {
    const onDownload = vi.fn();
    renderGallery({ index: 1, onDownload });
    const download = await screen.findByRole('button', { name: 'gallery.labels.download' });
    for (const name of [
      'gallery.labels.previous',
      'gallery.labels.next',
      'gallery.labels.zoom_in',
      'gallery.labels.rotate_cw',
      'gallery.labels.play',
      'gallery.labels.close',
    ]) {
      expect(screen.getByRole('button', { name })).toBeTruthy();
    }
    await userEvent.click(download);
    expect(onDownload).toHaveBeenCalledWith('b1');
  });

  it('applies the dark sibling of the active theme to the portal container', async () => {
    document.documentElement.setAttribute('data-theme', 'washi-light');
    renderGallery();
    await screen.findByRole('button', { name: 'gallery.labels.close' });
    await waitFor(() => {
      expect(document.body.querySelector('[data-theme="washi-dark"]')).not.toBeNull();
    });
  });

  it('keeps a dark theme id as-is', async () => {
    document.documentElement.setAttribute('data-theme', 'glass-dark');
    renderGallery();
    await screen.findByRole('button', { name: 'gallery.labels.close' });
    expect(document.body.querySelector('[data-theme="glass-dark"]')).not.toBeNull();
  });
});
