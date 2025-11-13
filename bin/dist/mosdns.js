const { createApp } = Vue;

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
    },
    methods: {
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
        const normalized = { ...this.uiSettings };
        if (Object.prototype.hasOwnProperty.call(serverSettings, 'autoRefreshLogs')) {
          normalized.autoRefreshLogs = this.normalizeBool(
            serverSettings.autoRefreshLogs,
            normalized.autoRefreshLogs,
          );
        }
        this.uiSettings = normalized;
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
            this.mosdns.latestVersion = tag;
            if (this.mosdns.version === '-' || this.mosdns.version === '') {
              this.mosdns.version = tag;
            }
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
            this.mosdns.version = tag;
            this.mosdns.latestVersion = tag;
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
          this.mosdns.version = tag || this.mosdns.version;
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
      previewConfig() {
        this.setBanner('info', `当前配置位于 ${this.config.path}`);
        this.touchUpdate('查看配置');
      },
      syncConfig() {
        const stamp = new Date();
        this.config.lastSynced = this.formatTime(stamp);
        this.touchUpdate('同步配置', stamp);
        this.setBanner('success', '配置同步完成（示意）');
      },
      editConfig() {
        this.setBanner('info', '请在本地编辑配置文件后重新同步。');
        this.touchUpdate('进入配置编辑');
      },
      downloadConfig() {
        this.openModal('下载配置', '配置模板即将提供，请稍后在文档中下载最新配置文件。');
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
      openModal(title, message) {
        this.modal = { title: title || '提示', message: message || '' };
      },
      closeModal() {
        this.modal = null;
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
      this.loadServiceStatus();
      this.loadConfigStatus();
      this.loadSettings();
      this.refreshVersion(true);
      this.loadLogs();
      this.applySettings();
    },
    beforeUnmount() {
      this.stopAutoLogRefresh();
    },
  }).mount('#app');
});
