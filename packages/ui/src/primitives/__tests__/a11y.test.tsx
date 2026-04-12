/**
 * Accessibility smoke tests for key UI primitives.
 * Each test renders the component with minimal required props and asserts
 * zero axe-core violations.
 */

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';

import Badge from '../badge/badge';
import Button from '../button/button';
import Chip from '../chip/chip';
import Input from '../input/input';
import Select from '../select/select';

describe('a11y smoke tests', () => {
  it('Button has no a11y violations', async () => {
    const { container } = render(<Button>Click me</Button>);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('Input has no a11y violations', async () => {
    const { container } = render(
      <>
        <label htmlFor="test-input">Name</label>
        <Input id="test-input" />
      </>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });

  it('Select has no a11y violations', async () => {
    const { container } = render(
      <>
        <label htmlFor="test-select">Choose</label>
        <Select id="test-select">
          <option value="a">Option A</option>
          <option value="b">Option B</option>
        </Select>
      </>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });

  it('Badge has no a11y violations', async () => {
    const { container } = render(<Badge>Active</Badge>);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('Chip has no a11y violations', async () => {
    const { container } = render(<Chip>Filter</Chip>);
    expect(await axe(container)).toHaveNoViolations();
  });

  it('Chip with dismiss button has no a11y violations', async () => {
    const { container } = render(
      <Chip onDismiss={() => {}} dismissLabel="Remove filter">
        Active filter
      </Chip>,
    );
    expect(await axe(container)).toHaveNoViolations();
  });
});
