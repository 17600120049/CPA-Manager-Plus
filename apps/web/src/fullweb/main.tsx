import { createRoot } from 'react-dom/client';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { readRoutingContext } from './routingContext';
import { BOOTSTRAP_UI, consumeHandoff, createBootstrapTransport } from './bootstrapTransport';
import { FreshBootstrapPage } from './FreshBootstrapPage';
import './carrier.css';

const context = readRoutingContext(document);
const root = createRoot(document.getElementById('root')!);
if (window.location.pathname === BOOTSTRAP_UI) {
  let transport = createBootstrapTransport();
  let mount = 0;
  const openHandoff = () => {
    const handoff = consumeHandoff(window.location, window.history);
    transport.clear();
    transport = createBootstrapTransport();
    root.render(<FreshBootstrapPage key={++mount} handoff={handoff} transport={transport} />);
  };
  // Pasting a host-issued link into this already-open page may navigate only
  // the fragment. Retire the previous in-memory authority before remounting.
  window.addEventListener('hashchange', openHandoff);
  openHandoff();
} else if (!context) {
  root.render(<p>Routing authority unavailable</p>);
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
  root.render(<RouterProvider router={router} />);
}
