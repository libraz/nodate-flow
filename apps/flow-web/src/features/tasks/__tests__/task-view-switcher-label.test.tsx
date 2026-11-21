import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import TaskViewSwitcher from '../task-view-switcher';

describe('TaskViewSwitcher labels', () => {
  it('keeps the view names as accessible names and wraps the text in the collapsible label span', () => {
    render(<TaskViewSwitcher />);
    const radios = screen.getAllByRole('radio');
    expect(radios).toHaveLength(4);
    for (const radio of radios) {
      expect(radio.getAttribute('aria-label')).toBeTruthy();
      const label = radio.querySelector('span > span');
      expect(label?.className).toContain('label');
    }
  });
});
