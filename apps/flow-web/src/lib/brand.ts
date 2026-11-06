import { resolveBrand } from '@nodate-flow/ui/brand';

/** Deployment branding, compiled in at build time and validated by the brand Vite plugin. */
const brand = resolveBrand(import.meta.env);

export const APP_NAME = brand.appName;
export const APP_LOGO = brand.logo;
export const APP_ICON = brand.icon;
