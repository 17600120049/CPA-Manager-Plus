import { describe, expect, it, vi } from 'vitest';
import {
  PLUGIN_HOST_THEME_MESSAGE,
  PLUGIN_HOST_THEME_PROTOCOL_VERSION,
  buildPluginHostStyle,
  buildPluginThemeMessage,
  createPluginHostStyleBridge,
} from './pluginHostStyle';

const createFrame = () => {
  const postMessage = vi.fn();
  const iframe = {
    contentWindow: { postMessage },
    parentElement: null,
  } as unknown as HTMLIFrameElement;
  return { iframe, postMessage };
};

describe('plugin host theme over postMessage', () => {
  it('addresses an opaque-origin frame without naming an origin', () => {
    const { iframe, postMessage } = createFrame();
    const bridge = createPluginHostStyleBridge(iframe, { theme: 'light' });

    expect(bridge).not.toBeNull();
    expect(postMessage).toHaveBeenCalledTimes(1);
    const [payload, targetOrigin] = postMessage.mock.calls[0];
    // A sandboxed plugin page has origin "null"; only '*' can reach it.
    expect(targetOrigin).toBe('*');
    expect(payload).toMatchObject({
      type: PLUGIN_HOST_THEME_MESSAGE,
      version: PLUGIN_HOST_THEME_PROTOCOL_VERSION,
      theme: 'light',
      themeAttribute: 'white',
    });
    expect(payload.variables['--primary-color']).toBeTruthy();
  });

  it('never reads the plugin document and never listens for plugin messages', () => {
    const postMessage = vi.fn();
    const frame = {
      contentWindow: { postMessage },
      parentElement: null,
    } as unknown as Record<string, unknown>;
    // A sandboxed document is cross-origin: any property read would throw.
    frame.contentDocument = new Proxy(
      {},
      {
        get: (_target, property) => {
          throw new Error(`opaque origin document read: ${String(property)}`);
        },
      }
    );
    const addSpy = vi.fn();
    const removeSpy = vi.fn();
    const originalWindow = globalThis.window;
    vi.stubGlobal('document', { documentElement: {} });
    vi.stubGlobal('window', {
      addEventListener: addSpy,
      removeEventListener: removeSpy,
      setTimeout: globalThis.setTimeout,
      clearTimeout: globalThis.clearTimeout,
      getComputedStyle: () => ({ length: 0, item: () => '' }),
    });

    try {
      const bridge = createPluginHostStyleBridge(frame as unknown as HTMLIFrameElement, {
        theme: 'light',
      });
      expect(bridge).not.toBeNull();
      expect(postMessage).toHaveBeenCalledTimes(1);
      bridge?.disconnect();
      // The host only sends. Theme adoption is the plugin's own decision, so
      // the admin document never subscribes to plugin-authored messages.
      const messageCalls = [...addSpy.mock.calls, ...removeSpy.mock.calls].filter(
        ([type]) => type === 'message'
      );
      expect(messageCalls).toEqual([]);
    } finally {
      vi.stubGlobal('window', originalWindow);
      vi.unstubAllGlobals();
    }
  });

  it('re-posts the current theme when the host theme changes', () => {
    const { iframe, postMessage } = createFrame();
    const bridge = createPluginHostStyleBridge(iframe, { theme: 'light' });

    expect(bridge?.refresh({ theme: 'dark' })).toBe(true);
    expect(postMessage).toHaveBeenCalledTimes(2);
    expect(postMessage.mock.calls[1][0]).toMatchObject({
      theme: 'dark',
      themeAttribute: 'dark',
    });
  });

  it('never touches the plugin document', () => {
    const { iframe } = createFrame();
    const created: string[] = [];
    const frame = iframe as unknown as Record<string, unknown>;
    frame.contentDocument = new Proxy(
      {},
      {
        get: (_target, property) => {
          created.push(String(property));
          throw new Error('opaque origin document must not be read');
        },
      }
    );

    const bridge = createPluginHostStyleBridge(iframe, { theme: 'dark' });
    expect(bridge).not.toBeNull();
    expect(created).toEqual([]);
  });

  it('reports failure when the frame has no window to message', () => {
    const iframe = { contentWindow: null, parentElement: null } as unknown as HTMLIFrameElement;
    expect(createPluginHostStyleBridge(iframe, { theme: 'light' })).toBeNull();
  });

  it('renders the documented plugin-side stylesheet from the same variables', () => {
    const message = buildPluginThemeMessage('dark');
    const style = buildPluginHostStyle('dark');

    expect(style).toContain('color-scheme: dark;');
    for (const [name, value] of Object.entries(message.variables)) {
      expect(style).toContain(`${name}: ${value};`);
    }
  });
});
