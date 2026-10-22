import { useCallback, useEffect, useRef, useState, type ReactNode, type MouseEvent, type SetStateAction } from 'react';
import { fullWebText as t, fullWebLanguage, setFullWebLanguage } from '../i18n/fullweb';
import { shellText as s } from '../i18n/consoleShell';
import logo from '../assets/brand/cpamp-logo-horizontal-color.svg?inline';
import './consoleShell.css';

export type ConsolePage = 'keys' | 'gateway' | 'settings' | 'installation' | 'missing';
const groups = [
  { id: 'clients', icon: '◇', pages: [{ id: 'keys', label: 'keys', route: '/api-keys' }] },
  { id: 'system', icon: '⚙', pages: [
    { id: 'gateway', label: 'gateway', route: '/system/gateway' },
    { id: 'settings', label: 'settings', route: '/system/settings' },
    { id: 'installation', label: 'overview', route: '/system/installation' },
  ] },
] as const;
function routeGroups(state: { page: ConsolePage; open: string[] }, page: ConsolePage, collapsed: boolean) {
  if (state.page === page) return state.open;
  if (collapsed) return [];
  const owner = groups.find((group) => group.pages.some((item) => item.id === page));
  return owner && !state.open.includes(owner.id) ? [...state.open, owner.id] : state.open;
}
export function consolePage(path: string, base: string): ConsolePage {
  const prefix = base.replace(/\/$/, '');
  if (path !== prefix && !path.startsWith(prefix + '/')) return 'missing';
  const route = path.slice(prefix.length).replace(/\/$/, '');
  if (!route) return 'keys';
  return groups.flatMap((group) => [...group.pages]).find((page) => page.route === route)?.id || 'missing';
}
export function ConsoleShell({ basePath, children, onLogout, onRefresh, blocked, light, onTheme, page, onNavigate, onLanguage }: {
  basePath: string; children: ReactNode; page: ConsolePage; onNavigate: (path: string) => void;
  onLogout: () => void; onRefresh: () => void; blocked: boolean; light: boolean; onTheme: () => void;
  onLanguage: () => void;
}) {
  const [collapsed, setCollapsed] = useState(false);
  const [mobile, setMobile] = useState(false);
  const [narrow, setNarrow] = useState(() => window.matchMedia?.('(max-width: 768px)').matches ?? false);
  const [navigation, setNavigation] = useState(() => ({ page, open: groups.filter((group) => group.pages.some((item) => item.id === page)).map((group) => group.id) as string[] }));
  const open = routeGroups(navigation, page, collapsed);
  const setOpen = useCallback((next: SetStateAction<string[]>) => {
    setNavigation((current) => ({ page, open: typeof next === 'function' ? next(routeGroups(current, page, collapsed)) : next }));
  }, [page, collapsed]);
  const [language, setLanguage] = useState(fullWebLanguage());
  const sidebar = useRef<HTMLElement>(null);
  const toggle = useRef<HTMLButtonElement>(null);
  const main = useRef<HTMLElement>(null);
  const groupTrigger = useRef<HTMLButtonElement | null>(null);
  const owner = groups.find((group) => group.pages.some((item) => item.id === page));
  const leaf = owner?.pages.find((item) => item.id === page);

  function close() { setMobile(false); }
  const wasMobile = useRef(false);
  useEffect(() => {
    if (!mobile && wasMobile.current) toggle.current?.focus();
    wasMobile.current = mobile;
  }, [mobile]);
  function collapse() { setOpen(collapsed && owner ? [owner.id] : []); setCollapsed(!collapsed); }
  useEffect(() => {
    const viewport = window.matchMedia?.('(max-width: 768px)');
    if (!viewport) return;
    const resize = () => { setNarrow(viewport.matches); if (!viewport.matches) setMobile(false); };
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
    if (!sidebar.current?.contains(document.activeElement)) sidebar.current?.querySelector<HTMLButtonElement>('button')?.focus();
    return () => { document.body.style.overflow = previous; };
  }, [mobile]);
  function navigate(event: MouseEvent<HTMLAnchorElement>, route: string) {
    if (blocked) { event.preventDefault(); return; }
    if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
    event.preventDefault();
    onNavigate(basePath + route);
    if (collapsed) setOpen([]);
    if (mobile) close();
    else main.current?.focus();
  }
  return (
    <div data-testid="console-shell" className={`console-shell ${collapsed ? 'is-collapsed' : ''} ${mobile ? 'is-mobile-open' : ''}`}>
      {mobile && <button className="console-backdrop" tabIndex={-1} aria-label={s('close')} onClick={close} />}
      <aside ref={sidebar} id="console-sidebar" className="console-sidebar" aria-label={s('navigation')} aria-modal={mobile || undefined} role={mobile ? 'dialog' : undefined}
        onKeyDown={(event) => {
          if (event.key === 'Escape' && mobile) { event.preventDefault(); close(); return; }
          if (event.key !== 'Tab' || !mobile) return;
          const items = Array.from(sidebar.current!.querySelectorAll<HTMLElement>('a[href],button')).filter((item) => item.getClientRects().length && !item.hasAttribute('disabled'));
          const first = items[0], last = items[items.length - 1];
          if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
          if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
        }}>
        <a className="console-brand" href={basePath} aria-disabled={blocked || undefined} onClick={(event) => navigate(event, '')}><img src={logo} alt="CPAMP" /></a>
        <button className="console-mobile-close" aria-label={s('close')} onClick={close}>×</button>
        <nav data-testid="console-navigation">
          {groups.map((group) => <div className="console-nav-group" key={group.id} onBlur={(event) => {
            if (collapsed && !event.currentTarget.contains(event.relatedTarget)) setOpen((current) => current.filter((id) => id !== group.id));
          }}>
            <button className={`console-group-toggle ${owner?.id === group.id ? 'is-current-group' : ''}`} aria-expanded={open.includes(group.id)} aria-controls={`nav-${group.id}`} aria-label={s(group.id)} title={s(group.id)}
              onClick={(event) => { groupTrigger.current = event.currentTarget; setOpen((current) => current.includes(group.id) ? current.filter((id) => id !== group.id) : collapsed ? [group.id] : [...current, group.id]); }}>
              <span aria-hidden="true">{group.icon}</span><span className="console-label">{s(group.id)}</span><span className="console-chevron" aria-hidden="true">{open.includes(group.id) ? '⌄' : '›'}</span>
            </button>
            {open.includes(group.id) && <div className="console-nav-children" id={`nav-${group.id}`}>
              {group.pages.map((item) => <a key={item.id} data-testid={item.id === 'gateway' ? 'gateway-retry-navigation' : undefined} href={basePath + item.route} aria-current={page === item.id ? 'page' : undefined} aria-disabled={blocked || undefined}
                onClick={(event) => navigate(event, item.route)} title={s(item.label)}><span aria-hidden="true">·</span><span>{s(item.label)}</span></a>)}
            </div>}
          </div>)}
        </nav>
        <footer><a href="https://seakee.github.io/CPA-Manager-Plus/" target="_blank" rel="noreferrer" title={s('help')}>? <span className="console-label">{s('help')}</span></a>
          <button data-testid="console-collapse-sidebar" aria-label={s(narrow ? 'close' : collapsed ? 'expand' : 'collapse')} onClick={() => narrow ? close() : collapse()}>{collapsed ? '»' : '«'}</button></footer>
      </aside>
      <div className="console-workspace" inert={mobile ? true : undefined}>
        <header className="console-topbar">
          <div className="console-top-left"><button data-testid="console-toggle-navigation" ref={toggle} aria-label={s('toggle')} aria-controls="console-sidebar" aria-expanded={narrow ? mobile : !collapsed} onClick={() => {
            if (window.matchMedia('(max-width: 768px)').matches) { setCollapsed(false); setOpen(owner ? [owner.id] : []); setMobile(!mobile); }
            else collapse();
          }}>☰</button><nav data-testid="console-breadcrumb" aria-label={s('breadcrumb')} className="console-breadcrumb">{owner && <span>{s(owner.id)} <span aria-hidden="true">/</span> </span>}<strong aria-current="page">{leaf ? s(leaf.label) : s('notFound')}</strong></nav></div>
          <div className="console-tools">
            <select aria-label={s('language')} value={language} onChange={(event) => { setFullWebLanguage(event.target.value); setLanguage(event.target.value); onLanguage(); }}>
              <option value="zh-CN">简体中文</option><option value="zh-TW">繁體中文</option><option value="en">English</option><option value="ru">Русский</option>
            </select>
            <button aria-label={t('theme')} onClick={onTheme}>{light ? '☾' : '☀'}</button>
            <button aria-label={t('refresh')} onClick={onRefresh}>↻</button>
            <button disabled={blocked} onClick={onLogout}>{t('logout')}</button>
          </div>
        </header>
        <main ref={main} tabIndex={-1} className="console-content" data-testid="console-operation">
          <div className="console-page-head"><h1>{leaf ? s(leaf.label) : s('notFound')}</h1></div>
          {page === 'missing' && <div className="info-card"><p>{s('notFound')}</p><a href={basePath} onClick={(event) => navigate(event, '')}>{s('returnHome')}</a></div>}
          {children}
        </main>
      </div>
    </div>
  );
}
