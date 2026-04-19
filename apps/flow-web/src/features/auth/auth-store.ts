/**
 * Re-export the shared auth store from @nodate-flow/sdk.
 * Kept as a bridge so existing imports continue to resolve.
 */
export {
  authStore,
  useAuth,
  selectAccessToken,
  selectUser,
  selectIsAuthenticated,
  type AuthUser,
  type AuthState,
} from '@nodate-flow/sdk';
