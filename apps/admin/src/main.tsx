import React from 'react';
import ReactDOM from 'react-dom/client';
import { App } from './App';
import { LightPlatform } from './LightPlatform';
import '@fontsource/outfit/400.css';
import '@fontsource/outfit/500.css';
import '@fontsource/outfit/600.css';
import '@fontsource/outfit/700.css';
import './styles.css';
import './tailadmin.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    {import.meta.env.VITE_PLATFORM_MODE === 'true' ? <LightPlatform /> : <App />}
  </React.StrictMode>,
);
