import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { installPluginHost } from './plugins/host'
import { initTheme } from './theme'
import './design/fonts.css'
import './design/tokens.css'
import './design/base.css'
import './design/primitives.css'
import App from './App'

initTheme()
installPluginHost()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
