import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import AppRouter from './router';
import { BASENAME } from './globalState';
import { useEffect } from 'react';
import { startManagedBrowserSync } from './runtime/managedBrowser';
import { startBrowserNotifications } from './modules/notifications/browser';

const router = createBrowserRouter([{ path: '*', element: <AppRouter /> }], {
  basename: BASENAME || undefined,
  future: { v7_relativeSplatPath: true },
});

function App() {
  useEffect(startManagedBrowserSync, []);
  useEffect(startBrowserNotifications, []);
  return (
    <RouterProvider router={router} />
  );
}

export default App;
