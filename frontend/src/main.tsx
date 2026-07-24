import React from 'react';
import ReactDOM from 'react-dom/client';
import App from './App';

// Bootstrap the React 18 application.
//
// The Go backend serves the production bundle from /static; in
// development Vite serves this file directly and proxies API calls to
// :8080 (see vite.config.ts).
ReactDOM.createRoot(document.getElementById('root') as HTMLElement).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
