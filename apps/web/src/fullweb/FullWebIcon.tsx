import type { SVGProps } from 'react';

const paths = {
  dashboard: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
  link: 'M10 13a5 5 0 0 0 7 .1l3-3a5 5 0 0 0-7-7l-2 2 M14 11a5 5 0 0 0-7-.1l-3 3a5 5 0 0 0 7 7l2-2',
  key: 'M14 4a6 6 0 1 1-3 11L4 22H1v-3l7-7a6 6 0 0 1 6-8Z M16 7h.01',
  gateway: 'M4 4h16v6H4z M4 14h16v6H4z M7 7h.01 M7 17h.01 M12 10v4',
  settings:
    'M10 3h4l.7 3 2 .9 2.8-.9 2 3.5-2.2 2.1v2.3l2.2 2.1-2 3.5-2.8-.9-2 .9-.7 3h-4l-.7-3-2-.9-2.8.9-2-3.5 2.2-2.1v-2.3L2.5 9.5l2-3.5 2.8.9 2-.9.7-3Z M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6',
  installation: 'M12 3v12 M8 11l4 4 4-4 M4 16v5h16v-5',
  help: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M9.5 9a2.5 2.5 0 1 1 4 2c-1 .6-1.5 1-1.5 2 M12 16h.01',
  fold: 'M3 4h18v16H3z M9 4v16 M16 9l-3 3 3 3',
  menu: 'M4 6h16 M4 12h16 M4 18h16',
  close: 'M6 6l12 12 M6 18 18 6',
  chevron: 'M9 5l7 7-7 7',
  sun: 'M12 3v2 M12 19v2 M3 12h2 M19 12h2 M5.6 5.6 7 7 M17 17l1.4 1.4 M5.6 18.4 7 17 M17 7l1.4-1.4 M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8',
  moon: 'M20.5 14.5A9 9 0 0 1 9.5 3.5a9 9 0 1 0 11 11Z',
  refresh: 'M20 7v5h-5 M4 17v-5h5 M6.1 7a7 7 0 0 1 11.5-2L20 8 M4 16l2.4 3A7 7 0 0 0 18 17',
  logout: 'M9 4H4v16h5 M13 7l5 5-5 5 M8 12h13',
  globe: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M3 12h18 M12 3c4 5 4 13 0 18-4-5-4-13 0-18Z',
  check: 'M4 12l5 5L20 6',
  alert: 'M12 3 2 21h20L12 3Z M12 9v5 M12 18h.01',
  arrowRight: 'M4 12h16 M14 6l6 6-6 6',
  shield: 'M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6l-8-3Z M8 12l3 3 5-6',
  clock: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 7v5l3 2',
  server: 'M4 3h16v7H4z M4 14h16v7H4z M8 6.5h.01 M8 17.5h.01 M12 6.5h5 M12 17.5h5',
  copy: 'M9 9h12v12H9z M15 9V3H3v12h6',
  eye: 'M2 12s4-7 10-7 10 7 10 7-4 7-10 7-10-7-10-7Z M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6',
  plus: 'M12 4v16 M4 12h16',
  search: 'M10.5 3a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15 M16 16l5 5',
  chevronRight: 'M9 5l7 7-7 7',
  arrowLeft: 'M20 12H4 M10 6l-6 6 6 6',
  info: 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 11v6 M12 7h.01',
  sparkle: 'M12 3l1.8 5.2L19 10l-5.2 1.8L12 17l-1.8-5.2L5 10l5.2-1.8L12 3 M5 17v4 M3 19h4 M19 3v4 M17 5h4',
} as const;

export type FullWebIconName = keyof typeof paths;

export function FullWebIcon({
  name,
  size = 18,
  className = '',
  ...props
}: SVGProps<SVGSVGElement> & { name: FullWebIconName; size?: number }) {
  return (
    <svg
      className={`fullweb-icon ${className}`}
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.6}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
      {...props}
    >
      <path d={paths[name]} />
    </svg>
  );
}
