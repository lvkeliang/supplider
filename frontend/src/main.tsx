import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { ToastProvider } from './components/Toast'
import { ErrorBoundary } from './components/ErrorBoundary'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {/* Outermost: a render error in any view shows a retry page instead of a
        blank webview (TR-12). ToastProvider stays inside so its API errors
        are caught too. */}
    <ErrorBoundary>
      {/* Mounted above App's gateKey-remounted tree so a toast survives view
          navigation and the down→online reconnect remount. */}
      <ToastProvider>
        <App />
      </ToastProvider>
    </ErrorBoundary>
  </React.StrictMode>,
)
