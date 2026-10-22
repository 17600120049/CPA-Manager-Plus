import { BrowserRouter, MemoryRouter } from 'react-router-dom';
import '../i18n';
import { AppLifecycle } from '../app/AppLifecycle';
import { LoginPage, type ConsoleLoginAdapter } from '../features/login/LoginPage';

export function FullWebLoginPage({ adapter }: { adapter: ConsoleLoginAdapter }) {
  const Router = typeof window.history?.replaceState === 'function' ? BrowserRouter : MemoryRouter;
  return (
    <Router>
      <AppLifecycle />
      <LoginPage consoleAuth={adapter} />
    </Router>
  );
}
