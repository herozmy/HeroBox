const createApp = Vue.createApp;
const LATEST_VERSION_CACHE_KEY = 'herobox.mosdns.latestVersion';
const DEFAULT_FAKEIP_RANGE = 'f2b0::/18';
const DEFAULT_DOMESTIC_DNS = '114.114.114.114';
const DEFAULT_SOCKS5_ENABLED = true;
const DEFAULT_SOCKS5_ADDRESS = '127.0.0.1:7891';
const DEFAULT_PROXY_INBOUND = '127.0.0.1:7874';
const DEFAULT_FORWARD_ECS = '2408:8214:213::1';

const SETTINGS_FIELDS = Object.freeze({
  fakeIpRange: DEFAULT_FAKEIP_RANGE,
  domesticDns: DEFAULT_DOMESTIC_DNS,
  socks5Address: DEFAULT_SOCKS5_ADDRESS,
  proxyInboundAddress: DEFAULT_PROXY_INBOUND,
  forwardEcsAddress: DEFAULT_FORWARD_ECS,
});

const createSettingsState = (overrides = {}) => ({
  ...SETTINGS_FIELDS,
  enableSocks5: DEFAULT_SOCKS5_ENABLED,
  ...overrides,
});

const MOSDNS_API_ENDPOINTS = Object.freeze([
  {
    label: '最新发布 API',
    url: 'https://api.github.com/repos/yyysuo/mosdns/releases/latest',
  },
  {
    label: '指定版本 API',
    url: 'https://api.github.com/repos/yyysuo/mosdns/releases/tags/v5-ph-srs',
  },
  {
    label: '发布页面',
    url: 'https://github.com/yyysuo/mosdns/releases/tag/v5-ph-srs',
  },
  {
    label: '资源清单页',
    url: 'https://github.com/yyysuo/mosdns/releases/expanded_assets/v5-ph-srs',
  },
  {
    label: '默认重启端点',
    url: 'http://127.0.0.1:9099/api/v1/system/restart',
  },
]);

const LIST_DEFINITIONS = Object.freeze([
  { tag: 'whitelist', name: '白名单', placeholder: '例如 example.com 或 full:example.com' },
  { tag: 'blocklist', name: '黑名单', placeholder: '支持 full:domain 或通配符' },
  { tag: 'greylist', name: '灰名单', placeholder: '例如 grey.domain.com' },
  { tag: 'ddnslist', name: 'DDNS 域名', placeholder: '每行一个 DDNS 域名' },
  
  { tag: 'client_ip', name: '客户端 IP', placeholder: 'CIDR 或单个 IP，例如 192.168.1.2' },
]);

const createPreferencesDraft = (settings = createSettingsState()) => ({
  fakeIpRange: settings.fakeIpRange,
  domesticDns: settings.domesticDns,
  socks5Address: settings.socks5Address,
  proxyInboundAddress: settings.proxyInboundAddress,
  forwardEcsAddress: settings.forwardEcsAddress,
});

const normalizeTextValue = (value) => {
  if (value == null) return '';
  return String(value).trim();
};

const normalizeSettingsPayload = (source = {}) => {
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

document.addEventListener('DOMContentLoaded', () => {
  createApp({
    data() {
      return {
        mosdns: {
          status: 'unknown',
          running: false,
          lastUpdated: '-',
          version: '-',
          latestVersion: '-',
          checking: false,
        },
        config: {
          path: '/etc/herobox/mosdns/config.yaml',
          lastSynced: '-',
          exists: true,
        },
        logsEntries: [],
        uiSettings: {
          autoRefreshLogs: true,
        },
        settingsForm: createSettingsState(),
        banner: null,
        actionPending: false,
        pendingAction: '',
        updatePending: false,
        stateLoading: false,
        modal: null,
        logsLoading: false,
        logsModalOpen: false,
        configEditModalOpen: false,
        configEditValue: '',
        configSaving: false,
        settingsLoading: false,
        settingsSaving: false,
        autoRefreshTimer: null,
        previewModalOpen: false,
        previewTree: [],
        previewFlatList: [],
        previewExpanded: {},
        previewActiveFile: '',
        previewEditingContent: '',
        previewDir: '',
        previewLoading: false,
        previewError: '',
        previewSaving: false,
        previewSaveProgress: 0,
        progressTickers: Object.create(null),
        configDownloading: false,
        configDownloadProgress: 0,
        downloadConfirmOpen: false,
        guideHistory: [],
        guideLogModalOpen: false,
        preferencesModalOpen: false,
        mosdnsSection: 'overview',
        mosdnsNavExpanded: false,
        preferencesDraft: createPreferencesDraft(),
        settingsSaveProgress: 0,
        apiEndpoints: MOSDNS_API_ENDPOINTS,
        listManager: {
          entries: LIST_DEFINITIONS,
          current: LIST_DEFINITIONS[2].tag,
          content: '',
          loading: false,
          saving: false,
          info: '尚未加载',
          error: '',
          lines: 0,
          changed: false,
          lastSaved: '',
          newEntry: '',
          initialized: {},
        },
      };
    },
    computed: {
      updateAvailable() {
        if (!this.mosdns.version || !this.mosdns.latestVersion) return false;
        return this.mosdns.version !== this.mosdns.latestVersion;
      },
      isMissing() {
        return this.mosdns.status === 'missing';
      },
      isRunning() {
        return this.mosdns.status === 'running';
      },
      canStart() {
        return !this.isMissing && !this.isRunning && this.config.exists;
      },
      startButtonLabel() {
        if (this.actionPending && this.pendingAction === 'start') return '启动中…';
        if (this.actionPending && this.pendingAction === 'restart') return '重启中…';
        if (this.isRunning) return '重启';
        return '启动';
      },
      startButtonDisabled() {
        if (this.actionPending) return true;
        if (this.isRunning) return false;
        return !this.canStart;
      },
      canStop() {
        return this.isRunning;
      },
      statusClass() {
        if (this.isRunning) return 'online';
        if (this.isMissing) return 'missing';
        return 'offline';
      },
      statusLabel() {
        if (this.isRunning) return '正在运行';
        if (this.isMissing) return '未安装';
        return '已停止';
      },
      previewDisplayedContent() {
        return this.previewEditingContent;
      },
      previewDirLabel() {
        if (this.previewDir) return this.previewDir;
        if (this.config.path) {
          const parts = this.config.path.split('/');
          parts.pop();
          return parts.join('/') || '/';
        }
        return '未知目录';
      },
      usingDefaultPreferences() {
        const fakeDefault = (this.settingsForm.fakeIpRange || '').trim() === DEFAULT_FAKEIP_RANGE;
        const dnsDefault = (this.settingsForm.domesticDns || '').trim() === DEFAULT_DOMESTIC_DNS;
        const addrDefault = (this.settingsForm.socks5Address || '').trim() === DEFAULT_SOCKS5_ADDRESS;
        const proxyDefault =
          (this.settingsForm.proxyInboundAddress || '').trim() === DEFAULT_PROXY_INBOUND;
        const forwardDefault =
          (this.settingsForm.forwardEcsAddress || '').trim() === DEFAULT_FORWARD_ECS;
        return fakeDefault && dnsDefault && addrDefault && proxyDefault && forwardDefault;
      },
      hasGuideHistory() {
        return Array.isArray(this.guideHistory) && this.guideHistory.length > 0;
      },
      currentListMeta() {
        return this.listManager.entries.find((item) => item.tag === this.listManager.current) || this.listManager.entries[0];
      },
    },
    methods: {
      restoreLatestVersion() {
        try {
          const cached = window.localStorage.getItem(LATEST_VERSION_CACHE_KEY);
          if (cached) {
            this.mosdns.latestVersion = cached;
          }
        } catch (err) {
          // 忽略本地存储异常
        }
      },
      persistLatestVersion(tag) {
        if (!tag) return;
        try {
          window.localStorage.setItem(LATEST_VERSION_CACHE_KEY, tag);
        } catch (err) {
          // 忽略本地存储异常
        }
      },
      async loadServiceStatus() {
        this.stateLoading = true;
        try {
          const snap = await this.apiRequest('/api/services/mosdns');
          this.consumeSnapshot(snap);
        } catch (err) {
          this.setBanner('error', `加载服务状态失败：${err.message}`);
        } finally {
          this.stateLoading = false;
        }
      },
      async loadLogs() {
        this.logsLoading = true;
        try {
          const payload = await this.apiRequest('/api/mosdns/logs');
          const entries = Array.isArray(payload.entries) ? payload.entries : [];
          this.logsEntries = entries.filter((item) =>
            typeof item.message === 'string' && item.message.includes('[mosdns]'),
          );
        } catch (err) {
          this.setBanner('error', `获取日志失败：${err.message}`);
        } finally {
          this.logsLoading = false;
        }
      },
      openLogsModal() {
        this.logsModalOpen = true;
        this.loadLogs();
      },
      closeLogsModal() {
        this.logsModalOpen = false;
      },
      async loadConfigStatus() {
        try {
          const status = await this.apiRequest('/api/mosdns/config');
          if (status.path) {
            this.config.path = status.path;
          }
          this.config.exists = Boolean(status.exists);
          if (status.modTime) {
            this.config.lastSynced = this.formatTime(status.modTime);
          }
          if (!this.config.exists) {
            this.setBanner('error', '未检测到 mosdns 配置，请先下载配置文件。');
            this.openModal('缺少配置文件', '尚未找到 mosdns 配置，请下载官方模板并放置到指定路径后重试。');
          }
        } catch (err) {
          this.setBanner('error', `检测配置失败：${err.message}`);
        }
      },
      async loadSettings() {
        this.settingsLoading = true;
        try {
          const resp = await this.apiRequest('/api/settings');
          if (resp.settings) {
            this.applySettingsFromServer(resp.settings);
          }
          this.applySettings();
        } catch (err) {
          this.setBanner('error', `加载前端设置失败：${err.message}`);
        } finally {
          this.settingsLoading = false;
        }
      },
      applySettingsFromServer(serverSettings) {
        const normalizedUi = { ...this.uiSettings };
        if (Object.prototype.hasOwnProperty.call(serverSettings, 'autoRefreshLogs')) {
          normalizedUi.autoRefreshLogs = this.normalizeBool(
            serverSettings.autoRefreshLogs,
            normalizedUi.autoRefreshLogs,
          );
        }
        this.uiSettings = normalizedUi;
        const normalizedForm = normalizeSettingsPayload(serverSettings);
        this.settingsForm = normalizedForm;
        this.preferencesDraft = createPreferencesDraft(normalizedForm);
      },
      applySettings() {
        const enabled = this.normalizeBool(this.uiSettings.autoRefreshLogs, true);
        this.uiSettings.autoRefreshLogs = enabled;
        if (enabled) {
          this.startAutoLogRefresh();
        } else {
          this.stopAutoLogRefresh();
        }
      },
      async saveSettings(partial) {
        if (!partial || Object.keys(partial).length === 0) {
          return;
        }
        this.settingsSaving = true;
        const payload = {};
        Object.entries(partial).forEach(([key, value]) => {
          if (typeof value === 'boolean') {
            payload[key] = value ? 'true' : 'false';
          } else if (value != null) {
            payload[key] = String(value);
          }
        });
        try {
          await this.apiRequest('/api/settings', {
            method: 'PUT',
            body: JSON.stringify(payload),
          });
        } catch (err) {
          this.setBanner('error', `保存设置失败：${err.message}`);
        } finally {
          this.settingsSaving = false;
        }
      },
      consumeSnapshot(snap) {
        if (!snap) return;
        const status = (snap.status || 'unknown').toLowerCase();
        this.mosdns.status = status;
        this.mosdns.running = status === 'running';
        this.mosdns.lastUpdated = this.formatTime(snap.lastUpdated);
        this.config.lastSynced = this.mosdns.lastUpdated;
        if (typeof snap.version === 'string') {
          const normalizedVersion = snap.version.trim();
          if (normalizedVersion) {
            this.mosdns.version = normalizedVersion;
          }
        }
        if (status === 'missing') {
          this.mosdns.version = '未安装';
          this.setBanner('error', '未检测到 mosdns 核心，请先执行“检查更新”或“一键更新”。');
        }
      },
      async toggleMosdns(state) {
        if ((state && !this.canStart) || (!state && !this.canStop)) {
          return;
        }
        this.actionPending = true;
        this.pendingAction = state ? 'start' : 'stop';
        try {
          const snap = await this.apiRequest(`/api/services/mosdns/${state ? 'start' : 'stop'}`, {
            method: 'POST',
          });
          this.consumeSnapshot(snap);
          this.setBanner('success', `mosdns 已${state ? '启动' : '停止'}`);
          this.loadLogs();
        } catch (err) {
          this.setBanner('error', err.message);
        } finally {
          this.actionPending = false;
          this.pendingAction = '';
        }
      },
      async handleStartOrRestart() {
        if (this.isRunning) {
          await this.performAction('restart');
        } else {
          await this.performAction('start');
        }
      },
      async performAction(action) {
        if (action === 'start' && !this.canStart) {
          return;
        }
        if (action === 'restart' && !this.isRunning) {
          return;
        }
        this.actionPending = true;
        this.pendingAction = action;
        try {
          const endpoint = action === 'restart' ? '/api/services/mosdns/restart' : '/api/services/mosdns/start';
          const snap = await this.apiRequest(endpoint, { method: 'POST' });
          this.consumeSnapshot(snap);
          this.setBanner('success', `mosdns 已${action === 'restart' ? '重启' : '启动'}`);
          await this.loadLogs();
        } catch (err) {
          this.setBanner('error', err.message);
        } finally {
          this.actionPending = false;
          this.pendingAction = '';
        }
      },
      async refreshVersion(silent = false) {
        if (this.mosdns.checking) return;
        this.mosdns.checking = true;
        if (!silent) {
          this.setBanner('info', '正在检测 mosdns 最新版本…');
        }
        try {
          const release = await this.apiRequest('/api/mosdns/kernel/latest');
          const tag = this.normalizeTag(release);
          if (tag) {
            this.persistLatestVersion(tag);
            this.mosdns.latestVersion = tag;
          }
          if (!silent) {
            this.setBanner('success', '已获取最新版本信息');
          }
        } catch (err) {
          if (!silent) {
            this.setBanner('error', `检测失败：${err.message}`);
          }
        } finally {
          this.mosdns.checking = false;
        }
      },
      async applyLatest() {
        if (this.updatePending) return;
        if (!this.updateAvailable && !this.isMissing) return;
        this.updatePending = true;
        this.setBanner('info', '正在下载并更新 mosdns 内核…');
        try {
          const payload = await this.apiRequest('/api/mosdns/kernel/update', {
            method: 'POST',
          });
          const tag = this.normalizeTag(payload.release);
          if (tag) {
            this.mosdns.latestVersion = tag;
            this.persistLatestVersion(tag);
          }
          if (payload.binary) {
            this.setBanner('success', `更新完成，已写入 ${payload.binary}`);
            this.openModal('内核更新完成', `mosdns 已安装到 ${payload.binary}，请视需要启动服务。`);
          } else {
            this.setBanner('success', '更新完成');
            this.openModal('内核更新完成', 'mosdns 已成功更新。');
          }
          this.mosdns.status = 'stopped';
          this.mosdns.running = false;
          this.mosdns.lastUpdated = this.formatTime(new Date());
          await this.loadServiceStatus();
          await this.loadConfigStatus();
          await this.loadLogs();
        } catch (err) {
          this.setBanner('error', `更新失败：${err.message}`);
        } finally {
          this.updatePending = false;
        }
      },
      async apiRequest(path, options = {}) {
        const headers = Object.assign({}, options.headers);
        if (!(options.body instanceof FormData)) {
          headers['Content-Type'] = headers['Content-Type'] || 'application/json';
        }
        const resp = await fetch(path, {
          credentials: 'same-origin',
          ...options,
          headers,
        });
        let payload = null;
        try {
          payload = await resp.json();
        } catch (err) {
          payload = null;
        }
        if (!resp.ok) {
          const message = (payload && (payload.error || payload.message)) || resp.statusText;
          throw new Error(message || '请求失败');
        }
        return payload;
      },
      setBanner(type, text) {
        if (!text) return;
        this.banner = { type, text, ts: Date.now() };
      },
      startProgressTicker(key, options = {}) {
        const { initial = 10, step = 5, max = 90, interval = 300 } = options;
        this.stopProgressTicker(key);
        this[key] = initial;
        this.progressTickers[key] = setInterval(() => {
          if (typeof this[key] !== 'number') {
            this[key] = initial;
            return;
          }
          if (this[key] < max) {
            this[key] += step;
          }
        }, interval);
      },
      stopProgressTicker(key, finalValue = 0) {
        if (typeof finalValue === 'number') {
          this[key] = finalValue;
        }
        if (this.progressTickers[key]) {
          clearInterval(this.progressTickers[key]);
          delete this.progressTickers[key];
        }
      },
      stopAllProgressTickers() {
        Object.keys(this.progressTickers).forEach((key) => this.stopProgressTicker(key));
      },
      previewConfig() {
        this.openPreviewModal();
        this.loadPreviewContent();
      },
      async performDownloadConfig() {
        if (this.configDownloading) return;
        this.configDownloading = true;
        this.startProgressTicker('configDownloadProgress', { initial: 5, step: 5, interval: 400 });
        this.setBanner('info', '正在下载官方 mosdns 配置…');
        try {
          const status = await this.apiRequest('/api/mosdns/config/download', {
            method: 'POST',
          });
          this.stopProgressTicker('configDownloadProgress', 100);
          this.setBanner('success', '配置下载完成并已解压');
          if (status && status.path) {
            this.openModal('配置下载完成', `mosdns 配置已写入 ${status.path}，可继续编辑或启动服务。`);
          }
          const guideSteps = Array.isArray(status?.guideSteps) ? status.guideSteps : [];
          this.appendGuideHistory(guideSteps);
          await this.loadConfigStatus();
        } catch (err) {
          this.stopProgressTicker('configDownloadProgress', 0);
          this.setBanner('error', `下载失败：${err.message}`);
        } finally {
          setTimeout(() => {
            this.configDownloading = false;
            this.configDownloadProgress = 0;
          }, 600);
        }
      },
      handleDownloadConfig() {
        if (this.configDownloading) return;
        if (this.usingDefaultPreferences) {
          this.downloadConfirmOpen = true;
          return;
        }
        this.performDownloadConfig();
      },
      cancelDownloadConfirm() {
        this.downloadConfirmOpen = false;
      },
      proceedDownloadWithDefaults() {
        this.downloadConfirmOpen = false;
        this.performDownloadConfig();
      },
      modifyPreferencesInstead() {
        this.downloadConfirmOpen = false;
        this.openPreferencesModal();
      },
      appendGuideHistory(steps) {
        if (!Array.isArray(steps) || steps.length === 0) {
          return;
        }
        const entry = {
          ts: Date.now(),
          steps: steps.map((step) => ({
            title: step.title,
            detail: step.detail,
            success: Boolean(step.success),
          })),
        };
        this.guideHistory = [entry, ...this.guideHistory].slice(0, 10);
      },
      openGuideLog() {
        if (!this.hasGuideHistory) return;
        this.guideLogModalOpen = true;
      },
      closeGuideLog() {
        this.guideLogModalOpen = false;
      },
      openPreferencesModal() {
        this.preferencesDraft = createPreferencesDraft(this.settingsForm);
        this.preferencesModalOpen = true;
      },
      closePreferencesModal() {
        this.preferencesModalOpen = false;
      },
      async savePreferencesFromModal() {
        const success = await this.saveConfigPreferences(this.preferencesDraft);
        if (success) {
          setTimeout(() => {
            if (this.preferencesModalOpen) {
              this.closePreferencesModal();
            }
          }, 600);
        }
      },
      openConfigEditor() {
        this.configEditValue = this.config.path || '';
        this.configEditModalOpen = true;
      },
      closeConfigEditor() {
        this.configEditModalOpen = false;
      },
      async saveConfigPath() {
        const value = (this.configEditValue || '').trim();
        if (!value) {
          this.setBanner('error', '配置路径不能为空');
          return;
        }
        this.configSaving = true;
        try {
          await this.apiRequest('/api/mosdns/config', {
            method: 'PUT',
            body: JSON.stringify({ path: value }),
          });
          this.setBanner('success', '配置路径已更新');
          this.closeConfigEditor();
          await this.loadConfigStatus();
        } catch (err) {
          this.setBanner('error', `更新配置路径失败：${err.message}`);
        } finally {
          this.configSaving = false;
        }
      },
      handleAutoRefreshToggle() {
        this.applySettings();
        this.saveSettings({ autoRefreshLogs: this.uiSettings.autoRefreshLogs });
      },
      async saveConfigPreferences(values) {
        const normalized = normalizeSettingsPayload(values || this.settingsForm);
        this.settingsForm = normalized;
        this.settingsSaving = true;
        this.startProgressTicker('settingsSaveProgress', { initial: 12, step: 6, interval: 220 });
        let success = false;
        try {
          await this.saveSettings({
            fakeIpRange: normalized.fakeIpRange,
            domesticDns: normalized.domesticDns,
            forwardEcsAddress: normalized.forwardEcsAddress,
            proxyInboundAddress: normalized.proxyInboundAddress,
            enableSocks5: normalized.enableSocks5,
            socks5Address: normalized.socks5Address,
          });
          this.stopProgressTicker('settingsSaveProgress', 100);
          this.preferencesDraft = createPreferencesDraft(normalized);
          this.setBanner('success', '配置偏好已保存，下一次下载将自动应用。');
          success = true;
        } catch (err) {
          this.stopProgressTicker('settingsSaveProgress', 0);
          this.setBanner('error', `保存偏好失败：${err.message}`);
        } finally {
          setTimeout(() => {
            this.settingsSaveProgress = 0;
          }, 800);
          this.settingsSaving = false;
        }
        return success;
      },
      openModal(title, message) {
        this.modal = { title: title || '提示', message: message || '' };
      },
      closeModal() {
        this.modal = null;
      },
      openPreviewModal() {
        this.previewModalOpen = true;
        this.previewLoading = true;
        this.previewError = '';
        this.previewSaving = false;
        this.previewExpanded = {};
        this.previewTree = [];
        this.previewFlatList = [];
        this.previewActiveFile = '';
        this.previewEditingContent = '';
        this.previewDir = '';
      },
      closePreviewModal() {
        this.previewModalOpen = false;
      },
      async loadPreviewContent() {
        this.previewLoading = true;
        this.previewError = '';
        try {
          const payload = await this.apiRequest('/api/mosdns/config/content');
          this.previewTree = Array.isArray(payload.tree) ? payload.tree : [];
          this.previewDir = payload.dir || '';
          this.previewExpanded = {};
          this.updatePreviewList();
          const firstFile = this.previewFlatList.find((item) => !item.isDir);
          if (firstFile) {
            this.previewActiveFile = firstFile.path;
            this.previewEditingContent = firstFile.content || '';
          } else {
            this.previewActiveFile = '';
            this.previewEditingContent = '';
          }
        } catch (err) {
          this.previewError = err.message;
          this.previewTree = [];
          this.previewFlatList = [];
          this.previewActiveFile = '';
          this.previewEditingContent = '';
        } finally {
          this.previewLoading = false;
        }
      },
      updatePreviewList() {
        this.previewFlatList = this.flattenPreviewNodes(this.previewTree, 0);
      },
      flattenPreviewNodes(nodes, level) {
        if (!nodes) return [];
        const list = [];
        nodes.forEach((node) => {
          const key = this.previewNodeKey(node);
          const isExpanded = this.previewExpanded[key] !== false;
          const item = {
            name: node.name,
            path: node.path,
            isDir: !!node.isDir,
            content: node.content,
            level,
            key,
            children: node.children || [],
            expanded: isExpanded,
          };
          list.push(item);
          if (item.isDir && isExpanded) {
            list.push(...this.flattenPreviewNodes(item.children, level + 1));
          }
        });
        return list;
      },
      previewNodeKey(node) {
        return node.path || node.name || '';
      },
      handlePreviewNodeClick(item) {
        if (item.isDir) {
          this.previewExpanded[item.key] = !item.expanded;
          this.updatePreviewList();
        } else {
          this.previewActiveFile = item.path;
          this.previewEditingContent = item.content || '';
        }
      },
      handlePreviewInput(event) {
        this.previewEditingContent = event.target.value;
      },
      setMosdnsSection(section) {
        if (this.mosdnsSection === section) return;
        this.mosdnsSection = section;
        if (section === 'lists') {
          this.ensureListLoaded(this.listManager.current);
        }
      },
      toggleMosdnsNav() {
        this.mosdnsNavExpanded = !this.mosdnsNavExpanded;
      },
      setActiveList(tag) {
        if (this.listManager.current === tag) return;
        this.listManager.current = tag;
        this.listManager.content = '';
        this.listManager.info = '正在加载…';
        this.listManager.error = '';
        this.listManager.lastSaved = '';
        this.listManager.newEntry = '';
        this.ensureListLoaded(tag, true);
      },
      async ensureListLoaded(tag, force = false) {
        const initMap = this.listManager.initialized || {};
        if (!force && initMap[tag]) return;
        await this.loadList(tag);
      },
      async loadList(tag) {
        this.listManager.loading = true;
        this.listManager.error = '';
        try {
          const text = await this.requestList(`/api/mosdns/lists/${tag}`);
          this.listManager.content = text.replace(/\r\n/g, '\n').replace(/\s+$/, '');
          this.updateListStats(false);
          this.listManager.initialized = {
            ...this.listManager.initialized,
            [tag]: true,
          };
          this.listManager.changed = false;
          this.listManager.lastSaved = '';
        } catch (err) {
          this.listManager.error = err.message || '加载失败';
          this.setBanner('error', `${tag} 读取失败：${err.message}`);
        } finally {
          this.listManager.loading = false;
        }
      },
      handleListInput(event) {
        this.listManager.content = event.target.value;
        this.updateListStats(true);
      },
      updateListStats(markChanged) {
        const lines = (this.listManager.content || '')
          .split(/\r?\n/)
          .map((line) => line.trim())
          .filter((line) => line.length > 0);
        this.listManager.lines = lines.length;
        this.listManager.info = lines.length ? `共 ${lines.length} 行` : '暂无条目';
        if (markChanged) {
          this.listManager.changed = true;
        }
      },
      appendListEntry() {
        const value = (this.listManager.newEntry || '').trim();
        if (!value) {
          this.setBanner('error', '请输入要添加的条目');
          return;
        }
        const next = this.listManager.content ? `${this.listManager.content}\n${value}` : value;
        this.listManager.content = next;
        this.listManager.newEntry = '';
        this.updateListStats(true);
      },
      buildListPayload() {
        const values = (this.listManager.content || '')
          .split(/\r?\n/)
          .map((line) => line.trim())
          .filter((line) => line.length > 0);
        return { values };
      },
      async saveCurrentList() {
        if (this.listManager.saving) return;
        const payload = this.buildListPayload();
        this.listManager.saving = true;
        this.listManager.error = '';
        try {
          await this.requestList(`/api/mosdns/lists/${this.listManager.current}`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          });
          this.listManager.changed = false;
          this.listManager.lastSaved = this.formatTime(new Date());
          this.setBanner('success', `${this.currentListMeta.name} 已保存`);
          this.updateListStats(false);
        } catch (err) {
          this.listManager.error = err.message || '保存失败';
          this.setBanner('error', `${this.currentListMeta.name} 保存失败：${err.message}`);
        } finally {
          this.listManager.saving = false;
        }
      },
      async requestList(path, options = {}) {
        const resp = await fetch(path, options);
        const text = await resp.text();
        if (!resp.ok) {
          throw new Error(text || resp.statusText);
        }
        return text;
      },
      async updateAdguard() {
        try {
          const resp = await fetch('/api/mosdns/adguard/update', { method: 'POST' });
          if (!resp.ok) {
            const text = await resp.text();
            throw new Error(text || resp.statusText);
          }
          this.setBanner('success', 'AdGuard 规则更新任务已触发');
        } catch (err) {
          this.setBanner('error', `AdGuard 更新失败：${err.message}`);
        }
      },
      async savePreviewFile() {
        if (!this.previewActiveFile) return;
        this.previewSaving = true;
        this.startProgressTicker('previewSaveProgress', { initial: 15, step: 7, interval: 200 });
        try {
          await this.apiRequest(`/api/mosdns/config/file?file=${encodeURIComponent(this.previewActiveFile)}`, {
            method: 'PUT',
            body: JSON.stringify({ path: this.previewActiveFile, content: this.previewEditingContent }),
          });
          this.updateTreeContent(this.previewActiveFile, this.previewEditingContent);
          this.setBanner('success', `${this.previewActiveFile} 已保存`);
          this.stopProgressTicker('previewSaveProgress', 100);
        } catch (err) {
          this.stopProgressTicker('previewSaveProgress', 0);
          this.setBanner('error', `保存失败：${err.message}`);
        } finally {
          this.previewSaving = false;
          setTimeout(() => {
            this.previewSaveProgress = 0;
          }, 800);
        }
      },
      updateTreeContent(path, content) {
        const update = (nodes) => {
          if (!nodes) return;
          nodes.forEach((node) => {
            if (node.path === path && !node.isDir) {
              node.content = content;
            }
            if (node.children) {
              update(node.children);
            }
          });
        };
        update(this.previewTree);
        this.updatePreviewList();
      },
      reloadPreview() {
        this.loadPreviewContent();
      },
      traceAction(message) {
        this.touchUpdate(message || '记录操作');
        this.setBanner('info', message || '已记录操作');
      },
      touchUpdate(message, timestamp) {
        const stamp = this.formatTime(timestamp || new Date());
        this.mosdns.lastUpdated = stamp;
      },
      normalizeTag(release) {
        if (!release) return '';
        return release.tag_name || release.tagName || release.name || '';
      },
      formatTime(value) {
        if (!value) return '-';
        const date = value instanceof Date ? value : new Date(value);
        if (Number.isNaN(date.getTime())) return '-';
        return date.toLocaleString('zh-CN', { hour12: false });
      },
      normalizeBool(value, fallback) {
        if (typeof value === 'boolean') return value;
        if (typeof value === 'string') {
          const lowered = value.toLowerCase();
          if (lowered === 'true') return true;
          if (lowered === 'false') return false;
        }
        return fallback;
      },
      startAutoLogRefresh() {
        this.stopAutoLogRefresh();
        this.autoRefreshTimer = setInterval(() => {
          this.loadLogs();
        }, 8000);
      },
      stopAutoLogRefresh() {
        if (this.autoRefreshTimer) {
          clearInterval(this.autoRefreshTimer);
          this.autoRefreshTimer = null;
        }
      },
    },
    mounted() {
      this.restoreLatestVersion();
      this.loadServiceStatus();
      this.loadConfigStatus();
      this.loadSettings();
      this.loadLogs();
      this.applySettings();
    },
    beforeUnmount() {
      this.stopAutoLogRefresh();
      this.stopAllProgressTickers();
      this.closeGuideLog();
      this.closePreferencesModal();
    },
  }).mount('#app');
});
