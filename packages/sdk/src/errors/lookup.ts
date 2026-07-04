import { AiErrors } from './ai.js';
import { AuthErrors } from './auth.js';
import { CalendarErrors } from './calendar.js';
import { ConstraintErrors } from './constraint.js';
import { ExportErrors } from './export.js';
import { InstanceErrors } from './instance.js';
import { IntegrationGhErrors } from './integration-gh.js';
import { IntegrationGoogleErrors } from './integration-google.js';
import { IntegrationSlackErrors } from './integration-slack.js';
import { IntegrationsErrors } from './integrations.js';
import { InternalErrors } from './internal.js';
import { ItemErrors } from './item.js';
import { McpErrors } from './mcp.js';
import { PageErrors } from './page.js';
import { RateErrors } from './rate.js';
import { RelationErrors } from './relation.js';
import { ShareErrors } from './share.js';
import { TimeboxErrors } from './timebox.js';
import { UserErrors } from './user.js';
import { ValidationErrors } from './validation.js';
import { WebhookErrors } from './webhook.js';
import { WorkspaceErrors } from './workspace.js';
import { WsErrors } from './ws.js';

export interface ErrorDefinition {
  code: string;
  status: number;
  message: string;
  i18nKey?: string;
}

const ERROR_GROUPS: ReadonlyArray<Record<string, ErrorDefinition>> = [
  AiErrors,
  AuthErrors,
  CalendarErrors,
  ConstraintErrors,
  ExportErrors,
  InstanceErrors,
  IntegrationGhErrors,
  IntegrationGoogleErrors,
  IntegrationSlackErrors,
  IntegrationsErrors,
  InternalErrors,
  ItemErrors,
  McpErrors,
  PageErrors,
  RateErrors,
  RelationErrors,
  ShareErrors,
  TimeboxErrors,
  UserErrors,
  ValidationErrors,
  WebhookErrors,
  WorkspaceErrors,
  WsErrors,
];

const ERROR_BY_CODE = new Map<string, ErrorDefinition>(
  ERROR_GROUPS.flatMap((group) => Object.values(group).map((def) => [def.code, def] as const)),
);

export function lookupErrorDefinition(code: string | undefined): ErrorDefinition | undefined {
  if (!code) return undefined;
  return ERROR_BY_CODE.get(code);
}

export function lookupErrorI18nKey(code: string | undefined): string | undefined {
  return lookupErrorDefinition(code)?.i18nKey;
}
