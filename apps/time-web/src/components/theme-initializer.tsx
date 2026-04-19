/**
 * ThemeInitializer is now a no-op. Theme management has been consolidated
 * into the shared ThemeProvider from @nodate-flow/ui, which is wired up
 * in time-web's providers/theme-provider.tsx.
 *
 * This file is kept as a stub so existing imports (e.g. __root.tsx) do
 * not break.
 */
export default function ThemeInitializer(): null {
  return null;
}
