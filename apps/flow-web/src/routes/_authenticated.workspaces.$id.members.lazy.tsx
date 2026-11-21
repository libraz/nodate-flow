/**
 * /workspaces/$id/members — workspace member list (lazy).
 *
 * The page heading names the page; the table carries its own section heading.
 */

import { createLazyFileRoute, getRouteApi } from '@tanstack/react-router';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import PageHeader from '../components/page-header';
import WorkspaceMembersTable from '../features/workspaces/workspace-members-table';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/members');

function WorkspaceMembersRoute(): ReactElement {
  const { t } = useTranslation('common');
  const { id } = routeApi.useParams();
  return (
    <>
      <PageHeader title={t('nav.members')} description={t('workspaces.members.page_description')} />
      <WorkspaceMembersTable workspaceId={id} />
    </>
  );
}

export const Route = createLazyFileRoute('/_authenticated/workspaces/$id/members')({
  component: WorkspaceMembersRoute,
});
