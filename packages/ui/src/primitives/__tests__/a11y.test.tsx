/**
 * Accessibility smoke tests for all UI primitives.
 *
 * Each primitive renders in its minimal valid configuration and is
 * asserted against axe-core via `vitest-axe`. The goal is breadth, not
 * depth: deeper, behaviour-specific a11y assertions live alongside each
 * primitive's own `*.test.tsx`. This file guarantees zero serious or
 * critical axe violations across every primitive in `packages/ui/src/primitives/`.
 *
 * When adding a new primitive, also add a smoke render here. The default
 * rule set is axe-core's "wcag2a" / "wcag2aa" / "best-practice" tags
 * (the `vitest-axe` default), which matches the WCAG 2.1 AA target
 * called out in the design-system spec.
 */

import { render } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { axe } from 'vitest-axe';

import Avatar from '../avatar/avatar';
import Badge from '../badge/badge';
import Breadcrumb, { BreadcrumbItem, BreadcrumbSeparator } from '../breadcrumb/breadcrumb';
import Button from '../button/button';
import Card from '../card/card';
import Checkbox from '../checkbox/checkbox';
import Chip from '../chip/chip';
import Combobox from '../combobox/combobox';
import Dialog from '../dialog/dialog';
import Drawer from '../drawer/drawer';
import EmptyState from '../empty-state/empty-state';
import ErrorFallback from '../error-fallback/error-fallback';
import FormField from '../form-field/form-field';
import Input from '../input/input';
import Popover from '../popover/popover';
import Radio from '../radio/radio';
import ScrollArea from '../scroll-area/scroll-area';
import SegmentedControl from '../segmented-control/segmented-control';
import Select from '../select/select';
import Separator from '../separator/separator';
import Skeleton from '../skeleton/skeleton';
import Spinner from '../spinner/spinner';
import Switch from '../switch/switch';
import Tabs from '../tabs/tabs';
import Textarea from '../textarea/textarea';
import ToggleChip, { ToggleChipGroup } from '../toggle-chip/toggle-chip';
import Tooltip from '../tooltip/tooltip';

async function expectClean(element: Element): Promise<void> {
  expect(await axe(element)).toHaveNoViolations();
}

describe('a11y smoke tests — primitives', () => {
  it('Avatar with src has no a11y violations', async () => {
    const { container } = render(<Avatar src="https://example.test/u.png" alt="User avatar" />);
    await expectClean(container);
  });

  it('Avatar with initials fallback has no a11y violations', async () => {
    const { container } = render(<Avatar alt="Jane Doe" initials="JD" />);
    await expectClean(container);
  });

  it('Badge has no a11y violations', async () => {
    const { container } = render(<Badge>Active</Badge>);
    await expectClean(container);
  });

  it('Breadcrumb has no a11y violations', async () => {
    const { container } = render(
      <Breadcrumb label="Breadcrumb">
        <BreadcrumbItem href="/">Home</BreadcrumbItem>
        <BreadcrumbSeparator />
        <BreadcrumbItem>Projects</BreadcrumbItem>
      </Breadcrumb>,
    );
    await expectClean(container);
  });

  it('Button has no a11y violations', async () => {
    const { container } = render(<Button>Click me</Button>);
    await expectClean(container);
  });

  it('Button with as="a" (polymorphic) has no a11y violations', async () => {
    const { container } = render(
      <Button as="a" href="/example">
        Link button
      </Button>,
    );
    await expectClean(container);
  });

  it('Card has no a11y violations', async () => {
    const { container } = render(
      <Card>
        <p>Card body</p>
      </Card>,
    );
    await expectClean(container);
  });

  it('Card with as="article" (polymorphic) has no a11y violations', async () => {
    const { container } = render(
      <Card as="article" aria-label="Article card">
        <h2>Title</h2>
        <p>Body</p>
      </Card>,
    );
    await expectClean(container);
  });

  it('Checkbox has no a11y violations when labelled', async () => {
    const { container } = render(
      <>
        <label htmlFor="cb">Subscribe</label>
        <Checkbox id="cb" />
      </>,
    );
    await expectClean(container);
  });

  it('Chip has no a11y violations', async () => {
    const { container } = render(<Chip>Filter</Chip>);
    await expectClean(container);
  });

  it('Chip with dismiss button has no a11y violations', async () => {
    const { container } = render(
      <Chip onDismiss={() => {}} dismissLabel="Remove filter">
        Active filter
      </Chip>,
    );
    await expectClean(container);
  });

  it('Combobox has no a11y violations', async () => {
    const { container } = render(
      <Combobox
        options={[
          { value: 'a', label: 'Alpha' },
          { value: 'b', label: 'Beta' },
        ]}
        aria-label="Choose option"
      />,
    );
    await expectClean(container);
  });

  it('Dialog (open) has no a11y violations', async () => {
    const { baseElement } = render(
      <Dialog open onClose={() => {}} title="Confirm">
        <button type="button">OK</button>
      </Dialog>,
    );
    await expectClean(baseElement);
  });

  it('Drawer (open) has no a11y violations', async () => {
    const { baseElement } = render(
      <Drawer open onClose={() => {}} title="Filters">
        <button type="button">Close</button>
      </Drawer>,
    );
    await expectClean(baseElement);
  });

  it('EmptyState has no a11y violations', async () => {
    const { container } = render(<EmptyState title="No results" description="Try a new query" />);
    await expectClean(container);
  });

  it('ErrorFallback has no a11y violations', async () => {
    const { container } = render(<ErrorFallback title="Something went wrong" />);
    await expectClean(container);
  });

  it('FormField (with label, hint, error) has no a11y violations', async () => {
    const { container } = render(
      <FormField label="Email" description="We never share it" error="Required">
        {(c) => <Input {...c} />}
      </FormField>,
    );
    await expectClean(container);
  });

  it('Input has no a11y violations when labelled', async () => {
    const { container } = render(
      <>
        <label htmlFor="test-input">Name</label>
        <Input id="test-input" />
      </>,
    );
    await expectClean(container);
  });

  it('Input dir="rtl" has no a11y violations', async () => {
    const { container } = render(
      <>
        <label htmlFor="rtl-input">Notes</label>
        <Input id="rtl-input" dir="rtl" />
      </>,
    );
    await expectClean(container);
  });

  it('Popover (closed) has no a11y violations', async () => {
    const { container } = render(
      <Popover content={<p>Body</p>}>
        <button type="button">Open</button>
      </Popover>,
    );
    await expectClean(container);
  });

  it('Radio has no a11y violations when labelled', async () => {
    const { container } = render(
      <fieldset>
        <legend>Pick one</legend>
        <label>
          <Radio name="g" value="a" />A
        </label>
        <label>
          <Radio name="g" value="b" />B
        </label>
      </fieldset>,
    );
    await expectClean(container);
  });

  it('ScrollArea has no a11y violations when labelled', async () => {
    const { container } = render(
      <ScrollArea aria-label="Scroll region" maxBlockSize={120}>
        <p>Content</p>
      </ScrollArea>,
    );
    await expectClean(container);
  });

  it('SegmentedControl has no a11y violations', async () => {
    const { container } = render(
      <SegmentedControl
        ariaLabel="View"
        value="month"
        onChange={() => {}}
        options={[
          { value: 'month', label: 'Month' },
          { value: 'week', label: 'Week' },
          { value: 'day', label: 'Day' },
        ]}
      />,
    );
    await expectClean(container);
  });

  it('Select has no a11y violations when labelled', async () => {
    const { container } = render(
      <>
        <label htmlFor="test-select">Choose</label>
        <Select id="test-select">
          <option value="a">Option A</option>
          <option value="b">Option B</option>
        </Select>
      </>,
    );
    await expectClean(container);
  });

  it('Separator has no a11y violations', async () => {
    const { container } = render(
      <div>
        <p>Above</p>
        <Separator />
        <p>Below</p>
      </div>,
    );
    await expectClean(container);
  });

  it('Skeleton has no a11y violations', async () => {
    const { container } = render(
      <div aria-busy="true">
        <Skeleton style={{ inlineSize: 100, blockSize: 16 }} />
      </div>,
    );
    await expectClean(container);
  });

  it('Spinner has no a11y violations', async () => {
    const { container } = render(<Spinner label="Loading" />);
    await expectClean(container);
  });

  it('Switch has no a11y violations when labelled', async () => {
    const { container } = render(
      <>
        <label htmlFor="sw">Notifications</label>
        <Switch id="sw" defaultChecked aria-label="Notifications" />
      </>,
    );
    await expectClean(container);
  });

  it('Tabs has no a11y violations', async () => {
    const { container } = render(
      <Tabs
        aria-label="Sections"
        items={[
          { value: 'a', label: 'Tab A', content: <p>A body</p> },
          { value: 'b', label: 'Tab B', content: <p>B body</p> },
        ]}
        defaultValue="a"
      />,
    );
    await expectClean(container);
  });

  it('Textarea has no a11y violations when labelled', async () => {
    const { container } = render(
      <>
        <label htmlFor="ta">Notes</label>
        <Textarea id="ta" />
      </>,
    );
    await expectClean(container);
  });

  it('Textarea dir="rtl" has no a11y violations', async () => {
    const { container } = render(
      <>
        <label htmlFor="ta-rtl">Notes</label>
        <Textarea id="ta-rtl" dir="rtl" />
      </>,
    );
    await expectClean(container);
  });

  it('ToggleChip has no a11y violations', async () => {
    const { container } = render(
      <ToggleChipGroup label="Layers">
        <ToggleChip pressed={false} onPressedChange={() => {}}>
          Layer
        </ToggleChip>
      </ToggleChipGroup>,
    );
    await expectClean(container);
  });

  it('Tooltip (closed) has no a11y violations', async () => {
    const { container } = render(
      <Tooltip content="Help text">
        <button type="button">Trigger</button>
      </Tooltip>,
    );
    await expectClean(container);
  });
});
