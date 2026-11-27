const HANDOFF_ROLES = ['assignee', 'reviewer', 'approver'] as const;
export type HandoffRole = (typeof HANDOFF_ROLES)[number];

export const HANDOFF_ROLE_KEY: Record<HandoffRole, string> = {
  assignee: 'taskWorkflow.roles.assignee',
  reviewer: 'taskWorkflow.roles.reviewer',
  approver: 'taskWorkflow.roles.approver',
};

export function isHandoffRole(value: string): value is HandoffRole {
  return value in HANDOFF_ROLE_KEY;
}
