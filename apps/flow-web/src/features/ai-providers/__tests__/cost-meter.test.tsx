import { screen } from '@testing-library/react';
import { renderWithProviders } from '@tests/helpers/render';
import { describe, expect, it, vi } from 'vitest';

import AiCostMeter from '../cost-meter';

vi.mock('../../../lib/use-current-workspace', () => ({
  useActiveWorkspaceId: () => null,
}));

describe('AiCostMeter', () => {
  it('reserves its slot instead of rendering nothing while no workspace is resolved', () => {
    const { container } = renderWithProviders(<AiCostMeter />);
    expect(container.querySelector('[aria-hidden="true"]')).not.toBeNull();
    expect(screen.queryByRole('link')).toBeNull();
  });
});
