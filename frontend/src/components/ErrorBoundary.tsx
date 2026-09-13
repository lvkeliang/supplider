import { Component, type ErrorInfo, type ReactNode } from 'react'

/**
 * Root error boundary (TR-12): React unmounts the WHOLE tree on an uncaught
 * render/lifecycle error, leaving a blank webview. The app talks to a local
 * database and survives a backend restart via the connection gate, but a
 * single unexpected data shape in one view could still white-screen it —
 * this boundary turns that into a friendly page with a retry.
 *
 * Retry remounts the subtree (a fresh fetch usually fixes a bad transient
 * state); 重新加载 fully reloads as the last resort.
 */

interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
  resetKey: number
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null, resetKey: 0 }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo): void {
    // Surface in the Tauri/browser console for diagnosis; the user sees the
    // fallback page instead of a blank screen.
    console.error('[ErrorBoundary]', error, info.componentStack ?? '')
  }

  private retry = () => {
    this.setState((s) => ({ error: null, resetKey: s.resetKey + 1 }))
  }

  private reload = () => {
    window.location.reload()
  }

  render(): ReactNode {
    if (this.state.error) {
      return (
        <div className="grid min-h-screen place-items-center px-4">
          <div className="w-full max-w-sm text-center">
            <div className="mx-auto mb-4 grid h-12 w-12 place-items-center rounded-lg bg-red-50 text-2xl text-red-500">
              !
            </div>
            <h1 className="text-base font-semibold text-slate-800 dark:text-slate-100">页面出现了一点问题</h1>
            <p className="mt-1.5 text-sm text-slate-500 dark:text-slate-400">
              数据保存在本地不会丢失。可以重试当前页面；若反复出现，请重启应用。
            </p>
            {this.state.error.message && (
              <pre className="mt-3 max-h-32 overflow-auto rounded-md bg-slate-50 dark:bg-slate-800 p-2 text-left text-xs text-slate-500 dark:text-slate-400">
                {this.state.error.message}
              </pre>
            )}
            <div className="mt-5 flex justify-center gap-2">
              <button type="button" className="btn-primary" onClick={this.retry}>
                重试
              </button>
              <button type="button" className="btn-ghost" onClick={this.reload}>
                重新加载
              </button>
            </div>
          </div>
        </div>
      )
    }
    // The key forces a clean remount of the whole subtree after retry.
    return <div key={this.state.resetKey}>{this.props.children}</div>
  }
}
