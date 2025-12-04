const API_BASE_URL = ''; // Assume same origin or configure proxy in vite.config.js

export async function apiRequest(path, options = {}) {
  const headers = Object.assign({}, options.headers);
  if (!(options.body instanceof FormData)) {
    headers['Content-Type'] = headers['Content-Type'] || 'application/json';
  }

  const resp = await fetch(`${API_BASE_URL}${path}`, {
    credentials: 'same-origin',
    ...options,
    headers,
  });

  const contentType = resp.headers.get('content-type') || '';
  const readJSON = async () => {
    try {
      return await resp.clone().json();
    } catch (err) {
      return null;
    }
  };
  const readText = async () => {
    try {
      return await resp.clone().text();
    } catch (err) {
      return null;
    }
  };

  let payload = null;
  if (contentType.includes('application/json')) {
    payload = await readJSON();
    if (payload == null) {
      payload = await readText();
    }
  } else {
    payload = await readText();
    if (payload == null) {
      payload = await readJSON();
    }
  }

  if (!resp.ok) {
    const message = (payload && (payload.error || payload.message || payload)) || resp.statusText;
    throw new Error(message || '请求失败');
  }
  return payload;
}

// Specific API functions
export const getServiceStatus = () => apiRequest('/api/services/mosdns');
export const getMosdnsLogs = () => apiRequest('/api/mosdns/logs');
export const getMosdnsConfigStatus = () => apiRequest('/api/mosdns/config');
export const getSettings = () => apiRequest('/api/settings');
export const saveSettings = (settings) => apiRequest('/api/settings', {
  method: 'PUT',
  body: JSON.stringify(settings),
});
export const startMosdns = () => apiRequest('/api/services/mosdns/start', { method: 'POST' });
export const stopMosdns = () => apiRequest('/api/services/mosdns/stop', { method: 'POST' });
export const restartMosdns = () => apiRequest('/api/services/mosdns/restart', { method: 'POST' });
export const getLatestMosdnsKernel = () => apiRequest('/api/mosdns/kernel/latest');
export const updateMosdnsKernel = () => apiRequest('/api/mosdns/kernel/update', { method: 'POST' });
export const downloadMosdnsConfig = () => apiRequest('/api/mosdns/config/download', { method: 'POST' });
export const updateConfigPath = (path) => apiRequest('/api/mosdns/config', {
  method: 'PUT',
  body: JSON.stringify({ path }),
});
export const getMosdnsConfigContent = () => apiRequest('/api/mosdns/config/content');
export const saveMosdnsConfigFile = (file, content) => apiRequest(`/api/mosdns/config/file?file=${encodeURIComponent(file)}`, {
  method: 'PUT',
  body: JSON.stringify({ path: file, content }),
});
export const getListContent = (tag) => apiRequest(`/api/mosdns/lists/${tag}`);
export const saveListContent = (tag, values) => apiRequest(`/api/mosdns/lists/${tag}`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ values }),
});
export const getSwitchValue = (tag) => apiRequest(`/api/mosdns/switches/${tag}`);
export const setSwitchValue = (tag, value) => apiRequest(`/api/mosdns/switches/${tag}`, {
  method: 'POST',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ value }),
});

// Utility functions from original mosdns.js
export const LATEST_VERSION_CACHE_KEY = 'herobox.mosdns.latestVersion';

export const DEFAULT_FAKEIP_RANGE = 'f2b0::/18';
export const DEFAULT_DOMESTIC_DNS = '114.114.114.114';
export const DEFAULT_SOCKS5_ADDRESS = '127.0.0.1:7891';
export const DEFAULT_PROXY_INBOUND = '127.0.0.1:7874';
export const DEFAULT_FORWARD_ECS = '2408:8214:213::1';
export const DEFAULT_DOMESTIC_FAKE_DNS = 'udp://127.0.0.1:7874';
export const DEFAULT_LISTEN_7777 = ':7777';
export const DEFAULT_LISTEN_8888 = ':8888';

export const SETTINGS_FIELDS = Object.freeze({
  fakeIpRange: DEFAULT_FAKEIP_RANGE,
  domesticDns: DEFAULT_DOMESTIC_DNS,
  socks5Address: DEFAULT_SOCKS5_ADDRESS,
  proxyInboundAddress: DEFAULT_PROXY_INBOUND,
  forwardEcsAddress: DEFAULT_FORWARD_ECS,
  domesticFakeDnsAddress: DEFAULT_DOMESTIC_FAKE_DNS,
  listenAddress7777: DEFAULT_LISTEN_7777,
  listenAddress8888: DEFAULT_LISTEN_8888,
  aliyunDohEcsIp: '',
  aliyunDohId: '',
  aliyunDohKeyId: '',
  aliyunDohKeySecret: '',
});

export const createSettingsState = (overrides = {}) => ({
  ...SETTINGS_FIELDS,
  enableSocks5: true, // DEFAULT_SOCKS5_ENABLED
  ...overrides,
});

export const createPreferencesDraft = (settings = createSettingsState()) => ({
  fakeIpRange: settings.fakeIpRange,
  domesticDns: settings.domesticDns,
  socks5Address: settings.socks5Address,
  proxyInboundAddress: settings.proxyInboundAddress,
  forwardEcsAddress: settings.forwardEcsAddress,
  domesticFakeDnsAddress: settings.domesticFakeDnsAddress,
  listenAddress7777: settings.listenAddress7777,
  listenAddress8888: settings.listenAddress8888,
  aliyunDohEcsIp: settings.aliyunDohEcsIp,
  aliyunDohId: settings.aliyunDohId,
  aliyunDohKeyId: settings.aliyunDohKeyId,
  aliyunDohKeySecret: settings.aliyunDohKeySecret,
});

export const normalizeTextValue = (value) => {
  if (value == null) return '';
  return String(value).trim();
};

export const normalizeSettingsPayload = (source = {}) => {
  const normalized = createSettingsState();
  Object.keys(SETTINGS_FIELDS).forEach((key) => {
    if (!Object.prototype.hasOwnProperty.call(source, key)) return;
    const text = normalizeTextValue(source[key]);
    if (key === 'socks5Address') {
      normalized[key] = text;
    } else {
      normalized[key] = text || SETTINGS_FIELDS[key];
    }
  });
  const socks5Address = normalizeTextValue(normalized.socks5Address);
  normalized.enableSocks5 = Boolean(socks5Address);
  normalized.socks5Address = normalized.enableSocks5 ? socks5Address : '';
  return normalized;
};

export const normalizeBool = (value, fallback) => {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'string') {
    const lowered = value.toLowerCase();
    if (lowered === 'true') return true;
    if (lowered === 'false') return false;
  }
  return fallback;
};

export const formatTime = (value) => {
  if (!value) return '-';
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
};

export const normalizeTag = (release) => {
  if (!release) return '';
  return release.tag_name || release.tagName || release.name || '';
};

export const LIST_DEFINITIONS = Object.freeze([
  { tag: 'whitelist', name: '白名单', placeholder: '例如 example.com 或 full:example.com' },
  { tag: 'blocklist', name: '黑名单', placeholder: '支持 full:domain 或通配符' },
  { tag: 'greylist', name: '灰名单', placeholder: '例如 grey.domain.com' },
  { tag: 'ddnslist', name: 'DDNS 域名', placeholder: '每行一个 DDNS 域名' },
  { tag: 'client_ip', name: '客户端 IP', placeholder: 'CIDR 或单个 IP，例如 192.168.1.2' },
]);

export const MOSDNS_API_ENDPOINTS = Object.freeze([
  { label: '最新发布 API', url: 'https://api.github.com/repos/yyysuo/mosdns/releases/latest' },
  { label: '指定版本 API', url: 'https://api.github.com/repos/yyysuo/mosdns/releases/tags/v5-ph-srs' },
  { label: '发布页面', url: 'https://github.com/yyysuo/mosdns/releases/tag/v5-ph-srs' },
  { label: '资源清单页', url: 'https://github.com/yyysuo/mosdns/releases/expanded_assets/v5-ph-srs' },
  { label: '默认重启端点', url: 'http://127.0.0.1:9099/api/v1/system/restart' },
]);
