import { QueryClient } from '@tanstack/react-query';
import { screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { renderWithProviders } from '@tests/helpers/render';
import { type ReactElement, Suspense } from 'react';
import { I18nextProvider } from 'react-i18next';
import { describe, expect, it } from 'vitest';
import { initI18n } from '../../../../i18n';
import { type Project, projectsKeys } from '../../../projects/api';
import { type WorkspaceMember, workspacesKeys } from '../../../workspaces/api';
import type { InboxItem } from '../../api';
import type { IntakeItem } from '../../intake/api';
import IntakeConvertDialog from '../../intake/intake-convert-dialog';
import SourceIntakeDialog from '../source-intake-dialog';

async function getEnglishAppI18n() {
  const instance = initI18n();
  if (!instance.isInitialized) {
    await new Promise<void>((resolve) => instance.once('initialized', () => resolve()));
  }
  await instance.changeLanguage('en');
  return instance;
}

function renderWithAppResources(
  ui: ReactElement,
  queryClient: QueryClient,
  i18n: Awaited<ReturnType<typeof getEnglishAppI18n>>,
) {
  return renderWithProviders(
    <I18nextProvider i18n={i18n}>
      <Suspense fallback={<p>loading</p>}>{ui}</Suspense>
    </I18nextProvider>,
    { queryClient },
  );
}

function createQueryClient(): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Number.POSITIVE_INFINITY } },
  });
  queryClient.setQueryData(projectsKeys.list('ws-1'), [
    { id: 'project-1', name: 'Platform', identifier: 'PLAT' } as Project,
  ]);
  queryClient.setQueryData(workspacesKeys.members('ws-1'), [
    {
      id: 'member-1',
      userId: 'user-1',
      displayName: 'Avery Example',
      email: 'avery@example.test',
      role: 'member',
    } as WorkspaceMember,
  ]);
  return queryClient;
}

async function expectDatePickerUsesCommonResource(): Promise<void> {
  const user = userEvent.setup();
  await user.click(await screen.findByRole('button', { name: 'Select a date' }));
  expect(await screen.findByRole('button', { name: 'Previous month' })).toBeDefined();
  expect(screen.getByRole('button', { name: 'Next month' })).toBeDefined();
  expect(screen.getByRole('columnheader', { name: 'Mon' })).toBeDefined();
}

describe('inbox date pickers with application translations', () => {
  it('loads common date labels in source intake review', async () => {
    const appI18n = await getEnglishAppI18n();
    const item = {
      id: 'signal-1',
      workspaceId: 'ws-1',
      source: 'github',
      kind: 'pull_request.opened',
      sourceDetail: { title: 'Review the release', body: 'Check the notes.' },
    } as InboxItem;
    renderWithAppResources(
      <SourceIntakeDialog workspaceId="ws-1" item={item} open={true} onClose={() => {}} />,
      createQueryClient(),
      appI18n,
    );

    await expectDatePickerUsesCommonResource();
  });

  it('loads common date labels in intake conversion', async () => {
    const appI18n = await getEnglishAppI18n();
    const item = {
      id: 'intake-1',
      title: 'Review the release',
      body: 'Check the notes.',
      triageStatus: 'pending',
      createdAt: 1_700_000_000,
    } as IntakeItem;
    renderWithAppResources(
      <IntakeConvertDialog workspaceId="ws-1" item={item} open={true} onClose={() => {}} />,
      createQueryClient(),
      appI18n,
    );

    await expectDatePickerUsesCommonResource();
  });
});
