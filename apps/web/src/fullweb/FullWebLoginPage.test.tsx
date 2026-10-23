import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { fullWebLanguage } from '../i18n/fullweb';
import { FullWebLoginPage } from './FullWebLoginPage';

vi.mock('../app/AppLifecycle', () => ({ AppLifecycle: () => null }));
vi.mock('../features/login/LoginPage', () => ({ LoginPage: () => <div /> }));

let renderer: ReactTestRenderer | undefined;
afterEach(async () => {
  if (renderer) await act(async () => renderer?.unmount());
  await i18n.changeLanguage('zh-CN');
  vi.unstubAllGlobals();
});

it('carries the existing login language into the Fresh journey', async () => {
  vi.stubGlobal('window', { history: {} });
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  await act(async () => {
    renderer = create(
      <FullWebLoginPage
        adapter={{
          address: 'https://manager.example/console',
          error: '',
          restore: async () => {},
          authenticate: async () => true,
        }}
      />
    );
  });
  for (const language of ['en', 'zh-TW', 'zh-CN']) {
    await act(async () => {
      await i18n.changeLanguage(language);
    });
    expect(fullWebLanguage()).toBe(language);
  }
});
