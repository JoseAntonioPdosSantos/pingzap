import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import App from './App'
import Remote from './pages/Remote'
import './styles.css'

// A personal web remote link (/c/<token>) opens the remote alone, without
// the management interface or its login.
const remote = window.location.pathname.match(/^\/c\/([^/]+)\/?$/)

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {remote ? (
      <Remote token={decodeURIComponent(remote[1])} />
    ) : (
      <BrowserRouter>
        <App />
      </BrowserRouter>
    )}
  </React.StrictMode>,
)
