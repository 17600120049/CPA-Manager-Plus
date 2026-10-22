import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
  type MouseEvent,
  type SetStateAction,
} from 'react';
import { fullWebText as t, fullWebLanguage, setFullWebLanguage } from '../i18n/fullweb';
import { shellText as s } from '../i18n/consoleShell';
import symbol from '../assets/brand/cpamp-symbol-color.png?inline';
import wordmark from '../assets/brand/cpamp-wordmark-color.png?inline';
import darkWordmark from '../assets/brand/cpamp-wordmark-on-dark.png?inline';
import {
  IconKey,
  IconSidebarDashboard,
  IconSidebarConfig,
  IconSidebarPlugins,
  IconSidebarSystem,
  IconSidebarUsage,
} from '../components/ui/icons';
import { FullWebIcon } from './FullWebIcon';
import './consoleShell.css';

export type ConsolePage =
  | 'home'
  | 'accounts'
  | 'providers'
  | 'models'
  | 'health'
  | 'keys'
  | 'policies'
  | 'monitoring'
  | 'usage'
  | 'prices'
  | 'runtime'
  | 'config'
  | 'maintenance'
  | 'logs'
  | 'updates'
  | 'plugins'
  | 'gateway'
  | 'settings'
  | 'installation'
  | 'missing';
const groups = [
  {
    id: 'resources',
    pages: [
      { id: 'accounts', label: 'accounts', route: '/accounts' },
      { id: 'providers', label: 'providers', route: '/ai-providers' },
      { id: 'models', label: 'models', route: '/models' },
      { id: 'health', label: 'health', route: '/credential-health' },
    ],
  },
  {
    id: 'clients',
    pages: [
      { id: 'keys', label: 'keys', route: '/api-keys' },
      { id: 'policies', label: 'policies', route: '/access-policies' },
    ],
  },
  {
    id: 'observe',
    pages: [
      { id: 'monitoring', label: 'monitoring', route: '/monitoring' },
      { id: 'usage', label: 'usage', route: '/usage-analytics' },
      { id: 'prices', label: 'prices', route: '/model-prices' },
    ],
  },
  {
    id: 'system',
    pages: [
      { id: 'runtime', label: 'runtime', route: '/system' },
      { id: 'config', label: 'config', route: '/config' },
      { id: 'gateway', label: 'gateway', route: '/system/gateway' },
      { id: 'settings', label: 'settings', route: '/system/settings' },
      { id: 'maintenance', label: 'maintenance', route: '/system/maintenance' },
      { id: 'logs', label: 'logs', route: '/logs' },
      { id: 'updates', label: 'updates', route: '/system/updates' },
      { id: 'installation', label: 'installationOverview', route: '/system/installation' },
    ],
  },
  {
    id: 'plugins',
    pages: [{ id: 'plugins', label: 'plugins', route: '/plugins' }],
  },
] as const;
function routeGroups(
  state: { page: ConsolePage; open: string[] },
  page: ConsolePage,
  collapsed: boolean
) {
  if (state.page === page) return state.open;
  if (collapsed) return [];
  const owner = groups.find((group) => group.pages.some((item) => item.id === page));
  return owner && !state.open.includes(owner.id) ? [...state.open, owner.id] : state.open;
}
export function consolePage(path: string, base: string): ConsolePage {
  const prefix = base.replace(/\/$/, '');
  if (path !== prefix && !path.startsWith(prefix + '/')) return 'missing';
  const route = path.slice(prefix.length).replace(/\/$/, '');
  if (!route) return 'home';
  // Dashboard remains the stable Console landing page. Keep the prototype's
  // direct /dashboard link compatible without adding a second dashboard view.
  if (route === '/dashboard') return 'home';
  return (
    groups.flatMap((group) => [...group.pages]).find((item) => item.route === route)?.id ||
    'missing'
  ) as ConsolePage;
}

function readSidebarCookie() {
  try {
    return typeof document.cookie === 'string' && document.cookie.split(';').some((item) => item.trim() === 'cpamp_console_sidebar=collapsed');
  } catch {
    return false;
  }
}

function writeSidebarCookie(collapsed: boolean) {
  try {
    document.cookie = `cpamp_console_sidebar=${collapsed ? 'collapsed' : 'expanded'};path=/;max-age=31536000;SameSite=Lax`;
  } catch {
    // UI preference persistence is best effort and never blocks Console.
  }
}
export function ConsoleShell({
  basePath,
  children,
  onLogout,
  onRefresh,
  blocked,
  light,
  onTheme,
  page,
  onNavigate,
  onLanguage,
}: {
  basePath: string;
  children: ReactNode;
  page: ConsolePage;
  onNavigate: (path: string) => void;
  onLogout: () => void;
  onRefresh: () => void;
  blocked: boolean;
  light: boolean;
  onTheme: () => void;
  onLanguage: () => void;
}) {
  const [collapsed, setCollapsed] = useState(readSidebarCookie);
  const [mobile, setMobile] = useState(false);
  const [narrow, setNarrow] = useState(
    () => window.matchMedia?.('(max-width: 768px)').matches ?? false
  );
  const [navigation, setNavigation] = useState(() => ({
    page,
    open: groups
      .filter((group) => group.pages.some((item) => item.id === page))
      .map((group) => group.id) as string[],
  }));
  const open = routeGroups(navigation, page, collapsed);
  const setOpen = useCallback(
    (next: SetStateAction<string[]>) => {
      setNavigation((current) => ({
        page,
        open: typeof next === 'function' ? next(routeGroups(current, page, collapsed)) : next,
      }));
    },
    [page, collapsed]
  );
  const [language, setLanguage] = useState(fullWebLanguage());
  const sidebar = useRef<HTMLElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const main = useRef<HTMLElement>(null);
  const groupTrigger = useRef<HTMLButtonElement | null>(null);
  const owner = groups.find((group) => group.pages.some((item) => item.id === page));
  const leaf = owner?.pages.find((item) => item.id === page);
  const title = page === 'home' ? s('home') : leaf ? s(leaf.label) : s('notFound');

  function close() {
    setMobile(false);
  }
  const wasMobile = useRef(false);
  useEffect(() => {
    if (!mobile && wasMobile.current) toggle.current?.focus();
    wasMobile.current = mobile;
  }, [mobile]);
  function collapse() {
    setOpen(collapsed && owner ? [owner.id] : []);
    setCollapsed(!collapsed);
  }
  useEffect(() => writeSidebarCookie(collapsed), [collapsed]);
  useEffect(() => {
    const viewport = window.matchMedia?.('(max-width: 768px)');
    if (!viewport) return;
    const resize = () => {
      setNarrow(viewport.matches);
      if (!viewport.matches) setMobile(false);
    };
    viewport.addEventListener('change', resize);
    return () => viewport.removeEventListener('change', resize);
  }, []);
  useEffect(() => {
    if (!collapsed || !open.length) return;
    const dismiss = (event: PointerEvent) => {
      const target = event.target as Element | null;
      if (!target?.closest('.console-nav-group')) setOpen([]);
    };
    const escape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      event.preventDefault();
      setOpen([]);
      groupTrigger.current?.focus();
    };
    document.addEventListener('pointerdown', dismiss);
    document.addEventListener('keydown', escape);
    return () => {
      document.removeEventListener('pointerdown', dismiss);
      document.removeEventListener('keydown', escape);
    };
  }, [collapsed, open, setOpen]);
  useEffect(() => {
    if (!mobile) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    sidebar.current?.querySelector<HTMLAnchorElement>('a[aria-current="page"]')?.focus();
    if (!sidebar.current?.contains(document.activeElement))
      sidebar.current?.querySelector<HTMLButtonElement>('button')?.focus();
    return () => {
      document.body.style.overflow = previous;
    };
  }, [mobile]);
  function navigate(event: MouseEvent<HTMLAnchorElement>, route: string) {
    if (blocked) {
      event.preventDefault();
      return;
    }
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    onNavigate(basePath + route);
    if (collapsed) setOpen([]);
    if (mobile) close();
    else main.current?.focus();
  }
  return (
    <div
      data-testid="console-shell"
      className={`console-shell ${collapsed ? 'is-collapsed' : ''} ${mobile ? 'is-mobile-open' : ''}`}
    >
      {mobile && (
        <button
          className="console-backdrop sidebar-backdrop visible"
          tabIndex={-1}
          aria-label={s('close')}
          onClick={close}
        />
      )}
      <aside
        ref={sidebar}
        id="console-sidebar"
        className={`console-sidebar sidebar ${collapsed ? 'collapsed' : ''} ${mobile ? 'open' : ''}`}
        aria-label={s('navigation')}
        aria-modal={mobile || undefined}
        role={mobile ? 'dialog' : undefined}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && mobile) {
            event.preventDefault();
            close();
            return;
          }
          if (event.key !== 'Tab' || !mobile) return;
          const items = Array.from(
            sidebar.current!.querySelectorAll<HTMLElement>('a[href],button')
          ).filter((item) => item.getClientRects().length && !item.hasAttribute('disabled'));
          const first = items[0],
            last = items[items.length - 1];
          if (event.shiftKey && document.activeElement === first) {
            event.preventDefault();
            last?.focus();
          }
          if (!event.shiftKey && document.activeElement === last) {
            event.preventDefault();
            first?.focus();
          }
        }}
      >
        <a
          className="console-brand sidebar-brand"
          href={basePath}
          aria-label="CPAMP"
          aria-disabled={blocked || undefined}
          onClick={(event) => navigate(event, '')}
        >
          <span className="sidebar-brand-main">
            <img className="sidebar-brand-symbol" src={symbol} alt="" />
            {(!collapsed || narrow) && (
              <img
                className="sidebar-brand-wordmark"
                src={light ? wordmark : darkWordmark}
                alt="CPAMP"
              />
            )}
          </span>
        </a>
        <button
          className="console-mobile-close console-icon-button btn btn-ghost"
          aria-label={s('close')}
          onClick={close}
        >
          <FullWebIcon name="close" />
        </button>
        <nav className="console-sidebar-navigation nav-section" data-testid="console-navigation">
          <a
            className={`console-nav-home nav-item ${page === 'home' ? 'active' : ''}`}
            data-testid="console-home-navigation"
            href={basePath}
            aria-current={page === 'home' ? 'page' : undefined}
            aria-disabled={blocked || undefined}
            title={s('home')}
            onClick={(event) => navigate(event, '')}
          >
            <span className="nav-icon">
              <IconSidebarDashboard size={20} />
            </span>
            <span className="console-label nav-label">{s('home')}</span>
          </a>
          {groups.map((group) => (
            <div
              className="console-nav-group"
              key={group.id}
              onBlur={(event) => {
                if (collapsed && !event.currentTarget.contains(event.relatedTarget))
                  setOpen((current) => current.filter((id) => id !== group.id));
              }}
            >
              <button
                className={`console-group-toggle nav-item ${owner?.id === group.id ? 'is-current-group' : ''} ${collapsed && owner?.id === group.id ? 'active' : ''}`}
                aria-expanded={open.includes(group.id)}
                aria-controls={`nav-${group.id}`}
                aria-label={s(group.id)}
                title={s(group.id)}
                onClick={(event) => {
                  groupTrigger.current = event.currentTarget;
                  setOpen((current) =>
                    current.includes(group.id)
                      ? current.filter((id) => id !== group.id)
                      : collapsed
                        ? [group.id]
                        : [...current, group.id]
                  );
                }}
              >
                <span className="nav-icon">
                  {group.id === 'resources' ? (
                    <IconSidebarConfig size={20} />
                  ) : group.id === 'clients' ? (
                    <IconKey size={20} />
                  ) : group.id === 'observe' ? (
                    <IconSidebarUsage size={20} />
                  ) : group.id === 'system' ? (
                    <IconSidebarSystem size={20} />
                  ) : (
                    <IconSidebarPlugins size={20} />
                  )}
                </span>
                <span className="console-label nav-label">{s(group.id)}</span>
                <FullWebIcon
                  name="chevron"
                  size={14}
                  className={`console-chevron ${open.includes(group.id) ? 'is-open' : ''}`}
                />
              </button>
              {open.includes(group.id) && (
                <div className="console-nav-children" id={`nav-${group.id}`}>
                  {group.pages.map((item) => (
                    <a
                      key={item.id}
                      className={`nav-item ${page === item.id ? 'active' : ''}`}
                      data-testid={item.id === 'gateway' ? 'gateway-retry-navigation' : undefined}
                      href={basePath + item.route}
                      aria-current={page === item.id ? 'page' : undefined}
                      aria-disabled={blocked || undefined}
                      onClick={(event) => navigate(event, item.route)}
                      title={s(item.label)}
                    >
                      <span className="nav-label">{s(item.label)}</span>
                    </a>
                  ))}
                </div>
              )}
            </div>
          ))}
        </nav>
        <footer>
          <a
            href="https://seakee.github.io/CPA-Manager-Plus/"
            target="_blank"
            rel="noreferrer"
            title={s('help')}
          >
            <FullWebIcon name="help" />
            <span className="console-label">{s('help')}</span>
          </a>
          <button
            className="console-icon-button btn btn-ghost"
            data-testid="console-collapse-sidebar"
            aria-label={s(narrow ? 'close' : collapsed ? 'expand' : 'collapse')}
            onClick={() => (narrow ? close() : collapse())}
          >
            <FullWebIcon name="fold" className={collapsed ? 'console-expand-icon' : ''} />
          </button>
        </footer>
      </aside>
      <div className="console-workspace" inert={mobile ? true : undefined}>
        <header className="console-topbar main-header navbar">
          <div className="console-top-left navbar-left">
            <button
              className="hamburger-container"
              data-testid="console-toggle-navigation"
              ref={toggle}
              aria-label={s('toggle')}
              aria-controls="console-sidebar"
              aria-expanded={narrow ? mobile : !collapsed}
              onClick={() => {
                if (window.matchMedia('(max-width: 768px)').matches) {
                  setCollapsed(false);
                  setOpen(owner ? [owner.id] : []);
                  setMobile(!mobile);
                } else collapse();
              }}
            >
              <FullWebIcon name={narrow ? 'menu' : 'fold'} />
            </button>
            <nav
              data-testid="console-breadcrumb"
              aria-label={s('breadcrumb')}
              className="console-breadcrumb app-breadcrumb"
            >
              {owner && (
                <span>
                  {s(owner.id)}{' '}
                  <span className="console-breadcrumb-divider" aria-hidden="true">
                    /
                  </span>{' '}
                </span>
              )}
              <strong aria-current="page">{title}</strong>
            </nav>
          </div>
          <div className="console-tools navbar-right">
            <label className="console-language">
              <FullWebIcon name="globe" size={16} />
              <select
                aria-label={s('language')}
                value={language}
                onChange={(event) => {
                  setFullWebLanguage(event.target.value);
                  setLanguage(event.target.value);
                  onLanguage();
                }}
              >
                <option value="zh-CN">简体中文</option>
                <option value="zh-TW">繁體中文</option>
                <option value="en">English</option>
                <option value="ru">Русский</option>
              </select>
            </label>
            <button
              className="console-icon-button console-refresh btn btn-ghost"
              aria-label={t('refresh')}
              title={t('refresh')}
              onClick={onRefresh}
            >
              <FullWebIcon name="refresh" />
            </button>
            <button
              className="console-icon-button btn btn-ghost"
              aria-label={t('theme')}
              title={t('theme')}
              onClick={onTheme}
            >
              <FullWebIcon name={light ? 'sun' : 'moon'} />
            </button>
            <button
              className="console-icon-button btn btn-ghost"
              aria-label={t('logout')}
              title={t('logout')}
              disabled={blocked}
              onClick={onLogout}
            >
              <FullWebIcon name="logout" />
            </button>
          </div>
        </header>
        <main ref={main} tabIndex={-1} className="console-content" data-testid="console-operation">
          {page !== 'keys' && (
            <div className="console-page-head card">
              <h1>{title}</h1>
            </div>
          )}
          {page === 'missing' && (
            <div className="info-card">
              <p>{s('notFound')}</p>
              <a href={basePath} onClick={(event) => navigate(event, '')}>
                {s('returnHome')}
              </a>
            </div>
          )}
          {children}
        </main>
      </div>
    </div>
  );
}
