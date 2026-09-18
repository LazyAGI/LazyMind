import { BrowserRouter } from 'react-router-dom';
import AppRouter from './router';
import { BASENAME } from './globalState';
import { useEffect } from 'react';
import { startManagedBrowserSync } from './runtime/managedBrowser';
import { startBrowserNotifications } from './modules/notifications/browser';

function App() {
  useEffect(startManagedBrowserSync, []);
  useEffect(startBrowserNotifications, []);
  return (
    <BrowserRouter
      basename={BASENAME || undefined}
      future={{
        v7_relativeSplatPath: true,
      }}
    >
      <AppRouter />
    </BrowserRouter>
  );
}

export default App;
