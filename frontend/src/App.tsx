import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import AppRouter from './router';
import { BASENAME } from './globalState';
import { useEffect } from 'react';
import { startManagedBrowserSync } from './runtime/managedBrowser';

const router = createBrowserRouter([{ path: '*', element: <AppRouter /> }], {
  basename: BASENAME || undefined,
  future: { v7_relativeSplatPath: true },
});

function App() {
  useEffect(startManagedBrowserSync, []);
  return (
    <RouterProvider router={router} />
  );
}

export default App;
