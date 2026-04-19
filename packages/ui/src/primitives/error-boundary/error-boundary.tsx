/**
 * ErrorBoundary — catches render errors and shows a fallback UI.
 *
 * Wraps React's class-based error boundary with a functional API.
 * The default fallback shows the error message and a "Try again" button.
 */

import { Component, type ErrorInfo, type ReactElement, type ReactNode } from 'react';
import styles from './error-boundary.module.css';

export interface ErrorFallbackProps {
  /** The caught error. */
  error: Error;
  /** Call to reset the boundary (re-renders children). */
  resetErrorBoundary: () => void;
}

export interface ErrorBoundaryProps {
  /** Content to render when no error. */
  children: ReactNode;
  /** Custom fallback component. If omitted, uses the built-in default. */
  fallback?: (props: ErrorFallbackProps) => ReactElement;
  /** Called when an error is caught (for logging / reporting). */
  onError?: (error: Error, info: ErrorInfo) => void;
}

interface State {
  error: Error | null;
}

/** Default fallback UI shown when no custom fallback is provided. */
function DefaultFallback({ error, resetErrorBoundary }: ErrorFallbackProps): ReactElement {
  return (
    <div className={styles.root} role="alert">
      <p className={styles.title}>Something went wrong</p>
      <p className={styles.message}>{error.message}</p>
      <button type="button" className={styles.retry} onClick={resetErrorBoundary}>
        Try again
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
      const Fallback = this.props.fallback ?? DefaultFallback;
      return <Fallback error={error} resetErrorBoundary={this.reset} />;
    }
    return this.props.children;
  }
}
