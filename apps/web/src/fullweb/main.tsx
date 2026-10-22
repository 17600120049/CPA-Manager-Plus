import { createRoot } from 'react-dom/client';
import type { ReactNode } from 'react';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { AppBackground } from '../components/common/AppBackground';
import { readRoutingContext } from './routingContext';
import { BOOTSTRAP_UI, consumeHandoff, createBootstrapTransport } from './bootstrapTransport';
import { FreshBootstrapPage } from './FreshBootstrapPage';
import '../styles/global.scss';
import './carrier.css';

const context = readRoutingContext(document);
const root = createRoot(document.getElementById('root')!);
const render = (page: ReactNode) =>
  root.render(
    <>
      <AppBackground />
      <div className="app-content">{page}</div>
    </>
  );
if (window.location.pathname === BOOTSTRAP_UI) {
  let transport = createBootstrapTransport();
  let mount = 0;
  const openHandoff = () => {
    const handoff = consumeHandoff(window.location, window.history);
    transport.clear();
    transport = createBootstrapTransport();
    render(<FreshBootstrapPage key={++mount} handoff={handoff} transport={transport} />);
  };
  // Pasting a host-issued link into this already-open page may navigate only
  // the fragment. Retire the previous in-memory authority before remounting.
  window.addEventListener('hashchange', openHandoff);
  openHandoff();
} else if (!context) {
  render(<p>Routing authority unavailable</p>);
} else {
  const router = createBrowserRouter(
    [
      {
        path: '*',
        hydrateFallbackElement: <></>,
        lazy: async () => {
          const { FreshConsolePage } = await import('./FreshConsolePage');
          return { Component: () => <FreshConsolePage context={context} /> };
        },
      },
      {
        path: '/technical-route',
        hydrateFallbackElement: <></>,
        lazy: async () => {
          const { ConsoleCarrier } = await import('./ConsoleCarrier');
          return { Component: ConsoleCarrier };
        },
      },
    ],
    { basename: context.basePath }
  );
  render(<RouterProvider router={router} />);
}
