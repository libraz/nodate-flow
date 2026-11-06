/**
 * AuthCard bound to the deployment brand (app name and logo).
 */
import BaseAuthCard, {
  type AuthCardProps as BaseAuthCardProps,
} from '@nodate-flow/ui/primitives/auth-card';
import type { ReactElement } from 'react';
import { APP_LOGO_URL, APP_NAME } from '@/lib/brand';

export type AuthCardProps = Omit<BaseAuthCardProps, 'appName' | 'logoUrl'>;

export default function AuthCard(props: AuthCardProps): ReactElement {
  return <BaseAuthCard {...props} appName={APP_NAME} logoUrl={APP_LOGO_URL} />;
}
