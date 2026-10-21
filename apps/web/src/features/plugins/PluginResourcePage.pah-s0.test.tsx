import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PluginResourcePage } from './PluginResourcePage';

const counters = vi.hoisted(() => ({ apiReads: 0, unsafeHookCalls: 0 }));

// In P-S0 the browser must not even discover a plugin-controlled menu URL.
// Throw if the legacy page attempts to read a menu, route or store.
vi.mock('@/services/api', () => ({
  pluginsApi: { list: () => { counters.apiReads++; throw Error('legacy plugin API used'); } },
}));
vi.mock('react-router-dom', () => ({
  useParams: () => { counters.unsafeHookCalls++; throw Error('legacy plugin route read'); },
}));
vi.mock('@/stores', () => ({
  useAuthStore: () => { counters.unsafeHookCalls++; throw Error('legacy auth read'); },
  useThemeStore: () => { counters.unsafeHookCalls++; throw Error('legacy theme read'); },
}));
vi.mock('@/hooks/useHeaderRefresh', () => ({
  useHeaderRefresh: () => { counters.unsafeHookCalls++; throw Error('legacy refresh registered'); },
}));
vi.mock('react-i18next', () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

afterEach(() => {
  vi.unstubAllEnvs();
  counters.apiReads = 0;
  counters.unsafeHookCalls = 0;
});

describe('PAH-S0 plugin page render contract', () => {
  it.each([
    '/v0/resource/plugins/demo/page',
    'relative/page.html',
    'https://outside.invalid/plugin',
    'http://127.0.0.1:18319/v0/resource/plugins/demo',
    'data:text/html,<script>alert(1)</script>',
    'blob:https://example.invalid/opaque',
    'javascript:alert(1)',
    'custom:execute',
  ])('never mounts legacy iframe or discovers a menu (%s)', (_menuURL) => {
    vi.stubEnv('VITE_CPAMP_PAH_S0', '1');
    // P-S0 has no menu-path input by design: no upstream menu is fetched,
    // so all of these paths are unable to reach the component at all.
    const html = renderToStaticMarkup(<PluginResourcePage />);
    expect(html).toContain('plugin_resource.pah_s0_unsupported');
    expect(html).not.toContain('<iframe');
    expect(html).not.toContain('javascript:');
    expect(counters.apiReads).toBe(0);
    expect(counters.unsafeHookCalls).toBe(0);
  });
});
