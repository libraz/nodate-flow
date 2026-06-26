/**
 * Re-export the shared auth store from @nodate-flow/sdk.
 * Kept as a bridge so existing imports continue to resolve.
 */
export {
  type AuthState,
  type AuthUser,
  authStore,
  selectAccessToken,
  selectIsAuthenticated,
  selectUser,
  useAuth,
} from '@nodate-flow/sdk';
