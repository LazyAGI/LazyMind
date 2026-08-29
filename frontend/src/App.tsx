import { BrowserRouter } from 'react-router-dom';
import AppRouter from './router';
import { BASENAME } from './globalState';

function App() {
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
