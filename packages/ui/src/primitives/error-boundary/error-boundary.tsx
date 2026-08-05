/**
 * ErrorBoundary — catches render errors and shows a fallback UI.
 *
 * Wraps React's class-based error boundary with a functional API.
 * The default fallback shows the error message and a "Try again" button.
 */

import { Component, type ErrorInfo, type ReactElement, type ReactNode } from 'react';
import styles from './error-boundary.module.css';

/**
 * What ErrorBoundary hands to a custom `fallback`.
 *
 * Named for the boundary rather than for the fallback because the
 * ErrorFallback primitive next door exports its own, different
 * `ErrorFallbackProps` — the props of that component. Two public types
 * with one name is only a problem once both reach the package barrel,
 * which is how this stayed invisible while ErrorBoundary was missing
 * from it.
 */
export interface ErrorBoundaryFallbackProps {
  /** The caught error. */
  error: Error;
  /** Call to reset the boundary (re-renders children). */
  resetErrorBoundary: () => void;
}

export interface ErrorBoundaryProps {
  /** Content to render when no error. */
  children: ReactNode;
  /** Custom fallback component. If omitted, uses the built-in default. */
  fallback?: (props: ErrorBoundaryFallbackProps) => ReactElement;
  /** Called when an error is caught (for logging / reporting). */
  onError?: (error: Error, info: ErrorInfo) => void;
  /** Title shown in the default fallback UI. Defaults to "Something went wrong". */
  fallbackTitle?: string;
  /** Action button label in the default fallback UI. Defaults to "Try again". */
  fallbackAction?: string;
}

interface State {
  error: Error | null;
}

/** Default fallback UI shown when no custom fallback is provided. */
function DefaultFallback({
  error,
  resetErrorBoundary,
  title = 'Something went wrong',
  action = 'Try again',
}: ErrorBoundaryFallbackProps & { title?: string; action?: string }): ReactElement {
  return (
    <div className={styles.root} role="alert">
      <p className={styles.title}>{title}</p>
      <p className={styles.message}>{error.message}</p>
      <button type="button" className={styles.retry} onClick={resetErrorBoundary}>
        {action}
      </button>
    </div>
  );
}

/** ErrorBoundary catches render errors in its children and shows a fallback UI. */
export default class ErrorBoundary extends Component<ErrorBoundaryProps, State> {
  override state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    this.props.onError?.(error, info);
  }

  private reset = (): void => {
    this.setState({ error: null });
  };

  override render(): ReactNode {
    const { error } = this.state;
    if (error) {
      if (this.props.fallback) {
        const Fallback = this.props.fallback;
        return <Fallback error={error} resetErrorBoundary={this.reset} />;
      }
      return (
        <DefaultFallback
          error={error}
          resetErrorBoundary={this.reset}
          {...(this.props.fallbackTitle !== undefined ? { title: this.props.fallbackTitle } : {})}
          {...(this.props.fallbackAction !== undefined
            ? { action: this.props.fallbackAction }
            : {})}
        />
      );
    }
    return this.props.children;
  }
}
