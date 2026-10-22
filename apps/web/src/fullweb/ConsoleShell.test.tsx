import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConsoleShell, consolePage, type ConsolePage } from './ConsoleShell';
import { setFullWebLanguage } from '../i18n/fullweb';
import { shellText as s } from '../i18n/consoleShell';

describe('Console routes', () => {
  it('resolves direct links only within the committed Console base', () => {
    expect(consolePage('/console', '/console')).toBe('keys');
    expect(consolePage('/console/', '/console')).toBe('keys');
    expect(consolePage('/console/api-keys', '/console')).toBe('keys');
    expect(consolePage('/console/system/gateway/', '/console')).toBe('gateway');
    expect(consolePage('/console/system/settings', '/console')).toBe('settings');
    expect(consolePage('/console/system/installation', '/console')).toBe('installation');
    expect(consolePage('/console-system/gateway', '/console')).toBe('missing');
    expect(consolePage('/elsewhere', '/console')).toBe('missing');
    expect(consolePage('/console/unknown', '/console')).toBe('missing');
    expect(consolePage('/api-keys', '/')).toBe('keys');
  });
});

describe('Console navigation', () => {
  let renderer: ReactTestRenderer | undefined;
  const navigate = vi.fn();
  const listeners = new Map<string, Set<(event: unknown) => void>>();
  let resize: () => void;
  let viewport: { matches: boolean; addEventListener: ReturnType<typeof vi.fn>; removeEventListener: ReturnType<typeof vi.fn> };
  let active: unknown;
  let body: { style: { overflow: string } };
  const focusable = () => {
    const node = { focus: vi.fn(() => { active = node; }), getClientRects: () => [{}], hasAttribute: () => false };
    return node;
  };
  let selected: ReturnType<typeof focusable>;
  let last: ReturnType<typeof focusable>;
  let toggle: ReturnType<typeof focusable>;
  let main: ReturnType<typeof focusable>;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    setFullWebLanguage('en');
    navigate.mockClear();
    listeners.clear();
    active = null;
    selected = focusable(); last = focusable(); toggle = focusable(); main = focusable();
    body = { style: { overflow: 'auto' } };
    viewport = {
      matches: false,
      addEventListener: vi.fn((_name: string, callback: () => void) => { resize = callback; }),
      removeEventListener: vi.fn(),
    };
    vi.stubGlobal('window', { matchMedia: () => viewport });
    vi.stubGlobal('document', {
      body,
      get activeElement() { return active; },
      addEventListener: (name: string, callback: (event: unknown) => void) => {
        if (!listeners.has(name)) listeners.set(name, new Set());
        listeners.get(name)!.add(callback);
      },
      removeEventListener: (name: string, callback: (event: unknown) => void) => listeners.get(name)?.delete(callback),
    });
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer!.unmount());
    renderer = undefined;
    vi.unstubAllGlobals();
    setFullWebLanguage('zh-CN');
  });
  const props = (page: ConsolePage = 'keys', blocked = false) => ({
    basePath: '/console', page, blocked, light: false, onNavigate: navigate,
    onLogout: vi.fn(), onRefresh: vi.fn(), onTheme: vi.fn(), onLanguage: vi.fn(),
    children: <p>Delivered page</p>,
  });
  const testid = (id: string) => renderer!.root.findByProps({ 'data-testid': id });
  const groups = () => renderer!.root.findAllByType('button').filter((node) => node.props.className?.includes('console-group-toggle'));
  const clickGroup = async (index: number) => {
    await act(async () => groups()[index].props.onClick({ currentTarget: toggle }));
  };
  const link = (route: string) => renderer!.root.findByProps({ href: '/console' + route });
  const emit = async (type: string, event: unknown) => {
    await act(async () => { for (const handler of [...(listeners.get(type) || [])]) handler(event); });
  };
  async function mount(page: ConsolePage = 'keys', blocked = false) {
    await act(async () => {
      renderer = create(<ConsoleShell {...props(page, blocked)} />, {
        createNodeMock: (element) => {
          if (element.type === 'aside') return {
            querySelector: () => selected,
            querySelectorAll: () => [selected, last],
            contains: (node: unknown) => node === selected || node === last,
          };
          if (element.type === 'main') return main;
          if ((element.props as Record<string, unknown>)['data-testid'] === 'console-toggle-navigation') return toggle;
          return null;
        },
      });
    });
  }

  it('opens the current route group and updates its breadcrumb after browser navigation', async () => {
    await mount('gateway');
    expect(link('/system/gateway').props['aria-current']).toBe('page');
    expect(groups().map((node) => node.props['aria-expanded'])).toEqual([false, true]);
    expect(testid('console-breadcrumb').findByType('strong').props.children).toBe(s('gateway'));
    await act(async () => renderer!.update(<ConsoleShell {...props('keys')} />));
    expect(link('/api-keys').props['aria-current']).toBe('page');
    expect(link('/system/gateway').props['aria-current']).toBeUndefined();
    expect(testid('console-breadcrumb').findByType('strong').props.children).toBe(s('keys'));
  });

  it('keeps collapsed groups closed, opens one flyout and dismisses it with outside click or Escape', async () => {
    await mount();
    await act(async () => testid('console-collapse-sidebar').props.onClick());
    expect(groups().every((node) => !node.props['aria-expanded'])).toBe(true);
    await clickGroup(0);
    expect(groups()[0].props['aria-expanded']).toBe(true);
    await clickGroup(1);
    expect(groups().map((node) => node.props['aria-expanded'])).toEqual([false, true]);
    await emit('pointerdown', { target: { closest: () => null } });
    expect(groups().every((node) => !node.props['aria-expanded'])).toBe(true);
    await clickGroup(1);
    await emit('keydown', { key: 'Escape', preventDefault: vi.fn() });
    expect(groups().every((node) => !node.props['aria-expanded'])).toBe(true);
    expect(toggle.focus).toHaveBeenCalled();
  });

  it('navigates real links, closes flyouts and leaves modified clicks to the browser', async () => {
    await mount();
    await act(async () => testid('console-collapse-sidebar').props.onClick());
    await clickGroup(1);
    const preventDefault = vi.fn();
    await act(async () => link('/system/settings').props.onClick({ ctrlKey: true, preventDefault }));
    expect(preventDefault).not.toHaveBeenCalled();
    expect(navigate).not.toHaveBeenCalled();
    await act(async () => link('/system/settings').props.onClick({ preventDefault }));
    expect(navigate).toHaveBeenCalledWith('/console/system/settings');
    expect(groups().every((node) => !node.props['aria-expanded'])).toBe(true);
    expect(main.focus).toHaveBeenCalledOnce();
  });

  it('preserves navigation locks for normal and modified clicks', async () => {
    await mount('gateway', true);
    const preventDefault = vi.fn();
    await act(async () => link('/system/settings').props.onClick({ preventDefault }));
    await act(async () => link('/system/settings').props.onClick({ ctrlKey: true, preventDefault }));
    expect(navigate).not.toHaveBeenCalled();
    expect(preventDefault).toHaveBeenCalledTimes(2);
    expect(link('/system/settings').props['aria-disabled']).toBe(true);
  });

  it('traps mobile focus, restores it on Escape, and releases body scrolling across resize', async () => {
    viewport.matches = true;
    await mount();
    expect(testid('console-toggle-navigation').props['aria-expanded']).toBe(false);
    await act(async () => testid('console-toggle-navigation').props.onClick());
    const aside = renderer!.root.findByType('aside');
    expect(aside.props['aria-modal']).toBe(true);
    expect(body.style.overflow).toBe('hidden');
    expect(selected.focus).toHaveBeenCalledOnce();
    active = last;
    const preventDefault = vi.fn();
    await act(async () => aside.props.onKeyDown({ key: 'Tab', preventDefault }));
    expect(active).toBe(selected);
    await act(async () => aside.props.onKeyDown({ key: 'Tab', shiftKey: true, preventDefault }));
    expect(active).toBe(last);
    await act(async () => aside.props.onKeyDown({ key: 'Escape', preventDefault }));
    expect(body.style.overflow).toBe('auto');
    expect(toggle.focus).toHaveBeenCalledOnce();
    await act(async () => testid('console-toggle-navigation').props.onClick());
    viewport.matches = false;
    await act(async () => resize());
    expect(aside.props['aria-modal']).toBeUndefined();
    expect(body.style.overflow).toBe('auto');
    expect(renderer!.root.findByProps({ className: 'console-workspace' }).props.inert).toBeUndefined();
  });

  it('closes the mobile drawer on navigation and backdrop activation', async () => {
    viewport.matches = true;
    await mount();
    await act(async () => testid('console-toggle-navigation').props.onClick());
    await act(async () => link('/api-keys').props.onClick({ preventDefault: vi.fn() }));
    expect(testid('console-toggle-navigation').props['aria-expanded']).toBe(false);
    expect(navigate).toHaveBeenCalledWith('/console/api-keys');
    await act(async () => testid('console-toggle-navigation').props.onClick());
    await act(async () => renderer!.root.findByProps({ className: 'console-backdrop' }).props.onClick());
    expect(testid('console-toggle-navigation').props['aria-expanded']).toBe(false);
    expect(body.style.overflow).toBe('auto');
  });

  it('renders translated navigation in all supported languages', async () => {
    await mount();
    for (const [language, expected] of [['zh-CN', '主导航'], ['zh-TW', '主導覽'], ['en', 'Main navigation'], ['ru', 'Главная навигация']]) {
      await act(async () => renderer!.root.findByType('select').props.onChange({ target: { value: language } }));
      expect(renderer!.root.findByType('aside').props['aria-label']).toBe(expected);
      expect(s('reviewTitle')).toBeTruthy();
    }
  });
});
