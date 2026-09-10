import React from 'react'
import ReactDOM from 'react-dom/client'
import App from './App'
import { ToastProvider } from './components/Toast'
import './index.css'

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {/* Mounted above App's gateKey-remounted tree so a toast survives view
        navigation and the down→online reconnect remount. */}
    <ToastProvider>
      <App />
    </ToastProvider>
  </React.StrictMode>,
)
