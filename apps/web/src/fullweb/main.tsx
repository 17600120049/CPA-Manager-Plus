import { createRoot } from 'react-dom/client';
import { createBrowserRouter, RouterProvider } from 'react-router-dom';
import { readRoutingContext } from './routingContext';
import './carrier.css';

// Non-product carrier only: no Console layout, Bootstrap workflow or login UI.
const context = readRoutingContext(document);
const root = createRoot(document.getElementById('root')!);
if (!context) {
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
