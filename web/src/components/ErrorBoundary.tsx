import { Component, type ErrorInfo, type ReactNode } from 'react'
import { AlertTriangle, ChevronDown, ChevronUp, Home, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { isChunkLoadError } from '@/lib/chunkErrors'

const CHUNK_RELOAD_KEY = 'hserver_chunk_reload'
const CHUNK_RELOAD_WINDOW_MS = 60_000

interface Props {
  children: ReactNode
  fallbackTitle?: string
}

interface State {
  hasError: boolean
  error: Error | null
  errorInfo: ErrorInfo | null
  detailsOpen: boolean
}

export class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props)
    this.state = {
      hasError: false,
      error: null,
      errorInfo: null,
      detailsOpen: false,
    }
  }

  static getDerivedStateFromError(error: Error): Partial<State> {
    return { hasError: true, error }
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    this.setState({ errorInfo })
    console.error('[ErrorBoundary] Caught error:', error, errorInfo)

    if (isChunkLoadError(error)) {
      const route = `${window.location.pathname}${window.location.search}`
      const now = Date.now()
      let previous: { route?: string; timestamp?: number } = {}
      try {
        previous = JSON.parse(sessionStorage.getItem(CHUNK_RELOAD_KEY) ?? '{}')
      } catch {
        // Invalid browser state should never block recovery.
      }
      const recentlyRetried = previous.route === route
        && typeof previous.timestamp === 'number'
        && now - previous.timestamp < CHUNK_RELOAD_WINDOW_MS
      if (!recentlyRetried) {
        try {
          sessionStorage.setItem(CHUNK_RELOAD_KEY, JSON.stringify({ route, timestamp: now }))
        } catch {
          // Reload still provides recovery when browser storage is unavailable.
        }
        window.location.reload()
      }
    }
  }

  handleRetry = () => {
    if (isChunkLoadError(this.state.error)) {
      window.location.reload()
      return
    }
    this.setState({ hasError: false, error: null, errorInfo: null, detailsOpen: false })
  }

  handleGoHome = () => {
    window.location.href = '/'
  }

  toggleDetails = () => {
    this.setState((prev) => ({ detailsOpen: !prev.detailsOpen }))
  }

  render() {
    if (!this.state.hasError) {
      return this.props.children
    }

    const { error, errorInfo, detailsOpen } = this.state
    const chunkFailure = isChunkLoadError(error)
    const title = this.props.fallbackTitle ?? (chunkFailure ? 'Page update could not be loaded' : 'Something went wrong')

    return (
      <div className="min-h-[400px] flex items-center justify-center p-6">
        <div className="w-full max-w-lg">
          <div className="flex flex-col items-center text-center mb-6">
            <div className="w-14 h-14 rounded-full bg-red-500/10 border border-red-500/20 flex items-center justify-center mb-4">
              <AlertTriangle className="w-7 h-7 text-red-400" />
            </div>
            <h2 className="text-white text-xl font-semibold">{title}</h2>
            <p className="text-zinc-400 text-sm mt-2 max-w-sm">
              {chunkFailure
                ? 'The connection was interrupted while loading this page. Refresh to download the page again.'
                : error?.message
                ? error.message
                : 'An unexpected error occurred. You can try again or return to the dashboard.'}
            </p>
          </div>

          <div className="flex items-center justify-center gap-3 mb-4">
            <Button onClick={this.handleRetry} className="bg-blue-600 hover:bg-blue-500 text-white">
              <RefreshCw className="w-4 h-4 mr-2" />
              {chunkFailure ? 'Refresh Page' : 'Try Again'}
            </Button>
            <Button
              variant="outline"
              onClick={this.handleGoHome}
              className="border-zinc-700 text-zinc-300 hover:bg-zinc-800 hover:text-white"
            >
              <Home className="w-4 h-4 mr-2" />
              Go to Dashboard
            </Button>
          </div>

          {(error || errorInfo) && (
            <div className="border border-zinc-800 rounded-lg overflow-hidden">
              <button
                onClick={this.toggleDetails}
                className="w-full flex items-center justify-between px-4 py-3 text-sm text-zinc-400 hover:text-white hover:bg-zinc-800/50 transition-colors"
              >
                <span>Error details</span>
                {detailsOpen ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
              </button>
              {detailsOpen && (
                <div className="border-t border-zinc-800 p-4 bg-zinc-900/50 space-y-3">
                  {error && (
                    <div>
                      <p className="text-zinc-500 text-xs uppercase tracking-wide mb-1">Error</p>
                      <pre className="text-red-400 text-xs font-mono bg-zinc-950 rounded p-3 overflow-auto whitespace-pre-wrap break-words">
                        {error.toString()}
                      </pre>
                    </div>
                  )}
                  {errorInfo?.componentStack && (
                    <div>
                      <p className="text-zinc-500 text-xs uppercase tracking-wide mb-1">Component Stack</p>
                      <pre className="text-zinc-400 text-xs font-mono bg-zinc-950 rounded p-3 overflow-auto max-h-48 whitespace-pre-wrap break-words">
                        {errorInfo.componentStack}
                      </pre>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    )
  }
}

export default ErrorBoundary
