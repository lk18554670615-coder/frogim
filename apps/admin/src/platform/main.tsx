import React from 'react';
import ReactDOM from 'react-dom/client';
import { PlatformAdmin } from './PlatformAdmin';
import '@fontsource/outfit/400.css';
import '@fontsource/outfit/500.css';
import '@fontsource/outfit/600.css';
import '@fontsource/outfit/700.css';
import '../styles.css';
import '../tailadmin.css';
import './platform.css';

ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><PlatformAdmin /></React.StrictMode>);
