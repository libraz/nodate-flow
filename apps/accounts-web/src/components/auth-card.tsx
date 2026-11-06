/**
 * AuthCard bound to the deployment brand (app name, logo and icon).
 */
import BaseAuthCard, {
  type AuthCardProps as BaseAuthCardProps,
} from '@nodate-flow/ui/primitives/auth-card';
import type { ReactElement } from 'react';
import { APP_ICON, APP_LOGO, APP_NAME } from '@/lib/brand';

export type AuthCardProps = Omit<BaseAuthCardProps, 'appName' | 'logo' | 'icon'>;

export default function AuthCard(props: AuthCardProps): ReactElement {
  return <BaseAuthCard {...props} appName={APP_NAME} logo={APP_LOGO} icon={APP_ICON} />;
}
