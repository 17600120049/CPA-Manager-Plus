import { createRoot } from 'react-dom/client';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { readRoutingContext } from './routingContext';
import { BOOTSTRAP_UI, consumeHandoff, createBootstrapTransport } from './bootstrapTransport';
import { FreshBootstrapPage } from './FreshBootstrapPage';
import './carrier.css';

const context = readRoutingContext(document);
const root = createRoot(document.getElementById('root')!);
if (window.location.pathname === BOOTSTRAP_UI) {
  const handoff = consumeHandoff(window.location, window.history);
  const transport = createBootstrapTransport();
  root.render(<FreshBootstrapPage handoff={handoff} transport={transport} />);
} else if (!context) {
  root.render(<p>Routing authority unavailable</p>);
} else {
  const router = createBrowserRouter(
    [
      { path: '/', element: <p>CPAMP technical carrier — no product design authority</p> },
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
