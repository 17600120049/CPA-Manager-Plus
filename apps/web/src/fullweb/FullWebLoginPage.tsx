import { BrowserRouter, MemoryRouter } from 'react-router-dom';
import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import '../i18n';
import { AppLifecycle } from '../app/AppLifecycle';
import { LoginPage, type ConsoleLoginAdapter } from '../features/login/LoginPage';
import { setFullWebLanguage } from '../i18n/fullweb';

export function FullWebLoginPage({ adapter }: { adapter: ConsoleLoginAdapter }) {
  const { i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language;
  useEffect(() => {
    // The existing login owns language selection. Carry it into the Fresh
    // journey rather than switching back to navigator.language after login.
    setFullWebLanguage(language);
  }, [language]);
  const Router = typeof window.history?.replaceState === 'function' ? BrowserRouter : MemoryRouter;
  return (
    <Router>
      <AppLifecycle />
      <LoginPage consoleAuth={adapter} />
    </Router>
  );
}
