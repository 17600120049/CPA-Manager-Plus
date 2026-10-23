import { createInstance } from 'i18next';
import { fullweb as zhCN } from './locales/zh-CN.json';
import { fullweb as zhTW } from './locales/zh-TW.json';
import { fullweb as en } from './locales/en.json';
import { fullweb as ru } from './locales/ru.json';

// Full Web does not enter the legacy connection or stored-preference graph.
const i18n = createInstance();
void i18n.init({
  resources: {
    'zh-CN': { translation: zhCN },
    'zh-TW': { translation: zhTW },
    en: { translation: en },
    ru: { translation: ru },
  },
  lng: typeof navigator !== 'undefined' && navigator.language ? navigator.language : 'zh-CN',
  supportedLngs: ['zh-CN', 'zh-TW', 'en', 'ru'],
  fallbackLng: 'zh-CN',
  initAsync: false,
  interpolation: { escapeValue: false },
});
export const fullWebText = (key: keyof typeof en): string => i18n.t(key);
export function setFullWebLanguage(language: string) {
  void i18n.changeLanguage(language);
}
export const fullWebLanguage = () => i18n.resolvedLanguage || 'zh-CN';
