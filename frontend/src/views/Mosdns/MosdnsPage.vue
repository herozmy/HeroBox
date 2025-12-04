<script setup>
import { ref, reactive, computed, onMounted, onUnmounted, watch, provide, inject } from 'vue';
import { RouterView } from 'vue-router';
import {
  apiRequest,
  getServiceStatus,
  getMosdnsLogs,
  getMosdnsConfigStatus,
  getSettings,
  saveSettings,
  startMosdns,
  stopMosdns,
  restartMosdns,
  getLatestMosdnsKernel,
  updateMosdnsKernel,
  downloadMosdnsConfig,
  updateConfigPath,
  getMosdnsConfigContent,
  saveMosdnsConfigFile,
  getListContent,
  saveListContent,
  getSwitchValue,
  setSwitchValue,
  LATEST_VERSION_CACHE_KEY,
  DEFAULT_FAKEIP_RANGE,
  DEFAULT_DOMESTIC_DNS,
  DEFAULT_SOCKS5_ADDRESS,
  DEFAULT_PROXY_INBOUND,
  DEFAULT_FORWARD_ECS,
  createSettingsState,
  createPreferencesDraft,
  normalizeTextValue,
  normalizeSettingsPayload,
  normalizeBool,
  formatTime,
  normalizeTag,
  LIST_DEFINITIONS,
  MOSDNS_API_ENDPOINTS,
} from '../../api.js';

import BaseModal from '../../components/BaseModal.vue';
import ProgressBar from '../../components/ProgressBar.vue';
const setBanner = inject('setBanner');

// --- Mosdns Section State ---
// Layout now serves as provider for sub-routes; no section switching needed

// --- Global Modals State ---
const showInfoModal = reactive({
  open: false,
  title: '',
  message: '',
});
const logsModalOpen = ref(false);
const configEditModalOpen = ref(false);
const downloadConfirmOpen = ref(false);
const guideLogModalOpen = ref(false);
const preferencesModalOpen = ref(false);
const previewModalOpen = ref(false);

const openModal = (title, message) => {
  showInfoModal.title = title || '提示';
  showInfoModal.message = message || '';
  showInfoModal.open = true;
};
const closeModal = () => {
  showInfoModal.open = false;
};

// --- General Component State & Methods (moved from original mosdns.js) ---
const mosdns = reactive({
  status: 'unknown',
  running: false,
  lastUpdated: '-',
  version: '-',
  latestVersion: '-',
  checking: false,
});
const config = reactive({
  path: '/etc/herobox/mosdns/config.yaml',
  lastSynced: '-',
  exists: true,
});
const logsEntries = ref([]);
const uiSettings = reactive({
  autoRefreshLogs: true,
});
const settingsForm = reactive(createSettingsState());
const preferencesDraft = reactive(createPreferencesDraft());

const actionPending = ref(false);
const pendingAction = ref('');
const updatePending = ref(false);
const stateLoading = ref(false);
const logsLoading = ref(false);
const configEditValue = ref('');
const configSaving = ref(false);
const settingsLoading = ref(false);
const settingsSaving = ref(false);
const autoRefreshTimer = ref(null);
const previewTree = ref([]);
const previewFlatList = ref([]);
const previewExpanded = reactive({});
const previewActiveFile = ref('');
const previewEditingContent = ref('');
const previewDir = ref('');
const previewLoading = ref(false);
const previewError = ref('');
const previewSaving = ref(false);
const previewSaveProgress = ref(0);
const configDownloading = ref(false);
const configDownloadProgress = ref(0);
const guideHistory = ref([]);
const settingsSaveProgress = ref(0);

const progressTickers = reactive({});

const startProgressTicker = (key, options = {}) => {
  const { initial = 10, step = 5, max = 90, interval = 300 } = options;
  stopProgressTicker(key);
  progressTickers[`${key}Value`] = initial; // Store value on a different key
  progressTickers[key] = setInterval(() => {
    if (typeof progressTickers[`${key}Value`] !== 'number') {
      progressTickers[`${key}Value`] = initial;
      return;
    }
    if (progressTickers[`${key}Value`] < max) {
      progressTickers[`${key}Value`] += step;
    }
  }, interval);
};

const stopProgressTicker = (key, finalValue = 0) => {
  if (typeof finalValue === 'number') {
    progressTickers[`${key}Value`] = finalValue; // Update the value key
  }
  if (progressTickers[key]) {
    clearInterval(progressTickers[key]);
    delete progressTickers[key];
  }
};

const stopAllProgressTickers = () => {
  Object.keys(progressTickers).forEach((key) => {
    if (key.endsWith('Value')) return; // Ignore value refs
    stopProgressTicker(key);
  });
};

const restoreLatestVersion = () => {
  try {
    const cached = window.localStorage.getItem(LATEST_VERSION_CACHE_KEY);
    if (cached) {
      mosdns.latestVersion = cached;
    }
  } catch (err) {
    console.error("Local storage error:", err);
  }
};

const persistLatestVersion = (tag) => {
  if (!tag) return;
  try {
    window.localStorage.setItem(LATEST_VERSION_CACHE_KEY, tag);
  } catch (err) {
    console.error("Local storage error:", err);
  }
};

const loadServiceStatus = async () => {
  stateLoading.value = true;
  try {
    const snap = await getServiceStatus();
    consumeSnapshot(snap);
  } catch (err) {
    setBanner('error', `加载服务状态失败：${err.message}`);
  } finally {
    stateLoading.value = false;
  }
};

const loadLogs = async () => {
  logsLoading.value = true;
  try {
    const payload = await getMosdnsLogs();
    const entries = Array.isArray(payload.entries) ? payload.entries : [];
    logsEntries.value = entries
      .map((item) => {
        if (typeof item === 'string') {
          return { timestamp: Date.now(), message: item };
        }
        const ts = item.timestamp || item.time || Date.now();
        const message = typeof item.message === 'string' ? item.message : '';
        return { timestamp: ts, message };
      })
      .filter((item) => item.message);
  } catch (err) {
    setBanner('error', `获取日志失败：${err.message}`);
  } finally {
    logsLoading.value = false;
  }
};

const openLogsModal = () => {
  logsModalOpen.value = true;
  loadLogs();
};

const closeLogsModal = () => {
  logsModalOpen.value = false;
};

const loadConfigStatus = async () => {
  try {
    const status = await getMosdnsConfigStatus();
    if (status.path) {
      config.path = status.path;
    }
    config.exists = Boolean(status.exists);
    if (status.modTime) {
      config.lastSynced = formatTime(status.modTime);
    }
    if (!config.exists) {
      setBanner('error', '未检测到 mosdns 配置，请先下载配置文件。');
      openModal('缺少配置文件', '尚未找到 mosdns 配置，请下载官方模板并放置到指定路径后重试。');
    }
  } catch (err) {
    setBanner('error', `检测配置失败：${err.message}`);
  }
};

const loadSettings = async () => {
  settingsLoading.value = true;
  try {
    const resp = await getSettings();
    if (resp.settings) {
      applySettingsFromServer(resp.settings);
    }
    applySettings();
  } catch (err) {
    setBanner('error', `加载前端设置失败：${err.message}`);
  } finally {
    settingsLoading.value = false;
  }
};

const applySettingsFromServer = (serverSettings) => {
  const normalizedUi = { ...uiSettings };
  if (Object.prototype.hasOwnProperty.call(serverSettings, 'autoRefreshLogs')) {
    normalizedUi.autoRefreshLogs = normalizeBool(
      serverSettings.autoRefreshLogs,
      normalizedUi.autoRefreshLogs,
    );
  }
  uiSettings.autoRefreshLogs = normalizedUi.autoRefreshLogs; // Update reactive object
  const normalizedForm = normalizeSettingsPayload(serverSettings);
  Object.assign(settingsForm, normalizedForm); // Update reactive object
  Object.assign(preferencesDraft, createPreferencesDraft(normalizedForm)); // Update reactive object
};

const applySettings = () => {
  const enabled = normalizeBool(uiSettings.autoRefreshLogs, true);
  uiSettings.autoRefreshLogs = enabled;
  if (enabled) {
    startAutoLogRefresh();
  } else {
    stopAutoLogRefresh();
  }
};

const saveComponentSettings = async (partial) => {
  if (!partial || Object.keys(partial).length === 0) {
    return;
  }
  settingsSaving.value = true;
  const payload = {};
  Object.entries(partial).forEach(([key, value]) => {
    if (typeof value === 'boolean') {
      payload[key] = value ? 'true' : 'false';
    } else if (value != null) {
      payload[key] = String(value);
    }
  });
  try {
    await saveSettings(payload);
  } catch (err) {
    setBanner('error', `保存设置失败：${err.message}`);
  } finally {
    settingsSaving.value = false;
  }
};

const consumeSnapshot = (snap) => {
  if (!snap) return;
  const status = (snap.status || 'unknown').toLowerCase();
  mosdns.status = status;
  mosdns.running = status === 'running';
  mosdns.lastUpdated = formatTime(snap.lastUpdated);
  config.lastSynced = mosdns.lastUpdated;
  if (typeof snap.version === 'string') {
    const normalizedVersion = snap.version.trim();
    if (normalizedVersion) {
      mosdns.version = normalizedVersion;
    }
  }
  if (status === 'missing') {
    mosdns.version = '未安装';
    setBanner('error', '未检测到 mosdns 核心，请先执行“检查更新”或“一键更新”。');
  }
};

const toggleMosdns = async (state) => {
  if ((state && !canStart.value) || (!state && !canStop.value)) {
    return;
  }
  actionPending.value = true;
  pendingAction.value = state ? 'start' : 'stop';
  try {
    const snap = await (state ? startMosdns() : stopMosdns());
    consumeSnapshot(snap);
    setBanner('success', `mosdns 已${state ? '启动' : '停止'}`);
    loadLogs();
  } catch (err) {
    setBanner('error', err.message);
  } finally {
    actionPending.value = false;
    pendingAction.value = '';
  }
};

const handleStartOrRestart = async () => {
  if (isRunning.value) {
    await performAction('restart');
  } else {
    await performAction('start');
  }
};

const performAction = async (action) => {
  if (action === 'start' && !canStart.value) {
    return;
  }
  if (action === 'restart' && !isRunning.value) {
    return;
  }
  actionPending.value = true;
  pendingAction.value = action;
  try {
    const snap = await (action === 'restart' ? restartMosdns() : startMosdns());
    consumeSnapshot(snap);
    setBanner('success', `mosdns 已${action === 'restart' ? '重启' : '启动'}`);
    await loadLogs();
  } catch (err) {
    setBanner('error', err.message);
  } finally {
    actionPending.value = false;
    pendingAction.value = '';
  }
};

const refreshVersion = async (silent = false) => {
  if (mosdns.checking) return;
  mosdns.checking = true;
  if (!silent) {
    setBanner('info', '正在检测 mosdns 最新版本…');
  }
  try {
    const release = await getLatestMosdnsKernel();
    const tag = normalizeTag(release);
    if (tag) {
      persistLatestVersion(tag);
      mosdns.latestVersion = tag;
    }
    if (!silent) {
      setBanner('success', '已获取最新版本信息');
    }
  } catch (err) {
    if (!silent) {
      setBanner('error', `检测失败：${err.message}`);
    }
  } finally {
    mosdns.checking = false;
  }
};

const applyLatest = async () => {
  if (updatePending.value) return;
  if (!updateAvailable.value && !isMissing.value) return;
  updatePending.value = true;
  setBanner('info', '正在下载并更新 mosdns 内核…');
  try {
    const payload = await updateMosdnsKernel();
    const tag = normalizeTag(payload.release);
    if (tag) {
      mosdns.latestVersion = tag;
      persistLatestVersion(tag);
    }
    if (payload.binary) {
      setBanner('success', `更新完成，已写入 ${payload.binary}`);
      openModal('内核更新完成', `mosdns 已安装到 ${payload.binary}，请视需要启动服务。`);
    } else {
      setBanner('success', '更新完成');
      openModal('内核更新完成', 'mosdns 已成功更新。');
    }
    mosdns.status = 'stopped';
    mosdns.running = false;
    mosdns.lastUpdated = formatTime(new Date());
    await loadServiceStatus();
    await loadConfigStatus();
    await loadLogs();
  } catch (err) {
    setBanner('error', `更新失败：${err.message}`);
  } finally {
    updatePending.value = false;
  }
};

const previewConfig = () => {
  openPreviewModal();
  loadPreviewContent();
};

const performDownloadConfig = async () => {
  if (configDownloading.value) return;
  configDownloading.value = true;
  startProgressTicker('configDownloadProgress', { initial: 5, step: 5, interval: 400 });
  setBanner('info', '正在下载官方 mosdns 配置…');
  try {
    const status = await downloadMosdnsConfig();
    stopProgressTicker('configDownloadProgress', 100);
    setBanner('success', '配置下载完成并已解压');
    if (status && status.path) {
      openModal('配置下载完成', `mosdns 配置已写入 ${status.path}，可继续编辑或启动服务。`);
    }
    const guideSteps = Array.isArray(status?.guideSteps) ? status.guideSteps : [];
    appendGuideHistory(guideSteps);
    await loadConfigStatus();
  } catch (err) {
    stopProgressTicker('configDownloadProgress', 0);
    setBanner('error', `下载失败：${err.message}`);
  } finally {
    setTimeout(() => {
      configDownloading.value = false;
      configDownloadProgress.value = 0; // Use value from progressTickers
    }, 600);
  }
};

const handleDownloadConfig = () => {
  if (configDownloading.value) return;
  if (usingDefaultPreferences.value) {
    downloadConfirmOpen.value = true;
    return;
  }
  performDownloadConfig();
};

const cancelDownloadConfirm = () => {
  downloadConfirmOpen.value = false;
};

const proceedDownloadWithDefaults = () => {
  downloadConfirmOpen.value = false;
  performDownloadConfig();
};

const modifyPreferencesInstead = () => {
  downloadConfirmOpen.value = false;
  openPreferencesModal();
};

const appendGuideHistory = (steps) => {
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
  guideHistory.value = [entry, ...guideHistory.value].slice(0, 10);
};

const openGuideLog = () => {
  if (!hasGuideHistory.value) return;
  guideLogModalOpen.value = true;
};

const closeGuideLog = () => {
  guideLogModalOpen.value = false;
};

const openPreferencesModal = () => {
  Object.assign(preferencesDraft, createPreferencesDraft(settingsForm));
  preferencesModalOpen.value = true;
};

const closePreferencesModal = () => {
  preferencesModalOpen.value = false;
};

const savePreferencesFromModal = async () => {
  const success = await saveConfigPreferences(preferencesDraft);
  if (success) {
    setTimeout(() => {
      if (preferencesModalOpen.value) {
        closePreferencesModal();
      }
    }, 600);
  }
};

const openConfigEditor = () => {
  configEditValue.value = config.path || '';
  configEditModalOpen.value = true;
};

const closeConfigEditor = () => {
  configEditModalOpen.value = false;
};

const saveConfigPath = async () => {
  const value = (configEditValue.value || '').trim();
  if (!value) {
    setBanner('error', '配置路径不能为空');
    return;
  }
  configSaving.value = true;
  try {
    await updateConfigPath(value);
    setBanner('success', '配置路径已更新');
    closeConfigEditor();
    await loadConfigStatus();
  } catch (err) {
    setBanner('error', `更新配置路径失败：${err.message}`);
  } finally {
    configSaving.value = false;
  }
};

const handleAutoRefreshToggle = () => {
  applySettings();
  saveComponentSettings({ autoRefreshLogs: uiSettings.autoRefreshLogs });
};

const saveConfigPreferences = async (values) => {
  const normalized = normalizeSettingsPayload(values || settingsForm);
  Object.assign(settingsForm, normalized); // Update reactive object
  settingsSaving.value = true;
  startProgressTicker('settingsSaveProgress', { initial: 12, step: 6, interval: 220 });
  let success = false;
  try {
    await saveComponentSettings({
      fakeIpRange: normalized.fakeIpRange,
      domesticDns: normalized.domesticDns,
      forwardEcsAddress: normalized.forwardEcsAddress,
      proxyInboundAddress: normalized.proxyInboundAddress,
      enableSocks5: normalized.enableSocks5,
      socks5Address: normalized.socks5Address,
      domesticFakeDnsAddress: normalized.domesticFakeDnsAddress,
      listenAddress7777: normalized.listenAddress7777,
      listenAddress8888: normalized.listenAddress8888,
      aliyunDohEcsIp: normalized.aliyunDohEcsIp,
      aliyunDohId: normalized.aliyunDohId,
      aliyunDohKeyId: normalized.aliyunDohKeyId,
      aliyunDohKeySecret: normalized.aliyunDohKeySecret,
    });
    stopProgressTicker('settingsSaveProgress', 100);
    Object.assign(preferencesDraft, createPreferencesDraft(normalized)); // Update reactive object
    setBanner('success', '配置偏好已保存，config_overrides.json 已更新。');
    success = true;
  } catch (err) {
    stopProgressTicker('settingsSaveProgress', 0);
    setBanner('error', `保存偏好失败：${err.message}`);
  } finally {
    setTimeout(() => {
      settingsSaveProgress.value = 0; // Use value from progressTickers
    }, 800);
    settingsSaving.value = false;
  }
  return success;
};

const openPreviewModal = () => {
  previewModalOpen.value = true;
  previewLoading.value = true;
  previewError.value = '';
  previewSaving.value = false;
  previewExpanded.value = {}; // Reset reactive object directly
  previewTree.value = [];
  previewFlatList.value = [];
  previewActiveFile.value = '';
  previewEditingContent.value = '';
  previewDir.value = '';
};

const closePreviewModal = () => {
  previewModalOpen.value = false;
};

const loadPreviewContent = async () => {
  previewLoading.value = true;
  previewError.value = '';
  try {
    const payload = await getMosdnsConfigContent();
    previewTree.value = Array.isArray(payload.tree) ? payload.tree : [];
    previewDir.value = payload.dir || '';
    previewExpanded.value = {};
    updatePreviewList();
    const firstFile = previewFlatList.value.find((item) => !item.isDir);
    if (firstFile) {
      previewActiveFile.value = firstFile.path;
      previewEditingContent.value = firstFile.content || '';
    } else {
      previewActiveFile.value = '';
      previewEditingContent.value = '';
    }
  } catch (err) {
    previewError.value = err.message;
    previewTree.value = [];
    previewFlatList.value = [];
    previewActiveFile.value = '';
    previewEditingContent.value = '';
  } finally {
    previewLoading.value = false;
  }
};

const updatePreviewList = () => {
  previewFlatList.value = flattenPreviewNodes(previewTree.value, 0);
};

const flattenPreviewNodes = (nodes, level) => {
  if (!nodes) return [];
  const list = [];
  nodes.forEach((node) => {
    const key = previewNodeKey(node);
    const isExpanded = previewExpanded.value[key] !== false;
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
      list.push(...flattenPreviewNodes(item.children, level + 1));
    }
  });
  return list;
};

const previewNodeKey = (node) => {
  return node.path || node.name || '';
};

const handlePreviewNodeClick = (item) => {
  if (item.isDir) {
    previewExpanded.value[item.key] = !item.expanded;
    updatePreviewList();
  } else {
    previewActiveFile.value = item.path;
    previewEditingContent.value = item.content || '';
  }
};

const handlePreviewInput = (event) => {
  previewEditingContent.value = event.target.value;
};

const savePreviewFile = async () => {
  if (!previewActiveFile.value) return;
  previewSaving.value = true;
  startProgressTicker('previewSaveProgress', { initial: 15, step: 7, interval: 200 });
  try {
    await saveMosdnsConfigFile(previewActiveFile.value, previewEditingContent.value);
    updateTreeContent(previewActiveFile.value, previewEditingContent.value);
    setBanner('success', `${previewActiveFile.value} 已保存`);
    stopProgressTicker('previewSaveProgress', 100);
  } catch (err) {
    stopProgressTicker('previewSaveProgress', 0);
    setBanner('error', `保存失败：${err.message}`);
  } finally {
    previewSaving.value = false;
    setTimeout(() => {
      previewSaveProgress.value = 0; // Use value from progressTickers
    }, 800);
  }
};

const updateTreeContent = (path, content) => {
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
  update(previewTree.value);
  updatePreviewList();
};

const reloadPreview = () => {
  loadPreviewContent();
};

const startAutoLogRefresh = () => {
  stopAutoLogRefresh();
  autoRefreshTimer.value = setInterval(() => {
    loadLogs();
  }, 8000);
};

const stopAutoLogRefresh = () => {
  if (autoRefreshTimer.value) {
    clearInterval(autoRefreshTimer.value);
    autoRefreshTimer.value = null;
  }
};

// --- Computed Properties ---
const updateAvailable = computed(() => {
  if (!mosdns.version || !mosdns.latestVersion) return false;
  return mosdns.version !== mosdns.latestVersion;
});
const isMissing = computed(() => mosdns.status === 'missing');
const isRunning = computed(() => mosdns.status === 'running');
const canStart = computed(() => !isMissing.value && !isRunning.value && config.exists);
const startButtonLabel = computed(() => {
  if (actionPending.value && pendingAction.value === 'start') return '启动中…';
  if (actionPending.value && pendingAction.value === 'restart') return '重启中…';
  if (isRunning.value) return '重启';
  return '启动';
});
const startButtonDisabled = computed(() => {
  if (actionPending.value) return true;
  if (isRunning.value) return false;
  return !canStart.value;
});
const canStop = computed(() => isRunning.value);
const statusClass = computed(() => {
  if (isRunning.value) return 'online';
  if (isMissing.value) return 'missing';
  return 'offline';
});
const statusLabel = computed(() => {
  if (isRunning.value) return '正在运行';
  if (isMissing.value) return '未安装';
  return '已停止';
});
const previewDisplayedContent = computed(() => previewEditingContent.value);
const previewDirLabel = computed(() => {
  if (previewDir.value) return previewDir.value;
  if (config.path) {
    const parts = config.path.split('/');
    parts.pop();
    return parts.join('/') || '/';
  }
  return '未知目录';
});
const usingDefaultPreferences = computed(() => {
  const fakeDefault = (settingsForm.fakeIpRange || '').trim() === DEFAULT_FAKEIP_RANGE;
  const dnsDefault = (settingsForm.domesticDns || '').trim() === DEFAULT_DOMESTIC_DNS;
  const addrDefault = (settingsForm.socks5Address || '').trim() === DEFAULT_SOCKS5_ADDRESS;
  const proxyDefault =
    (settingsForm.proxyInboundAddress || '').trim() === DEFAULT_PROXY_INBOUND;
  const forwardDefault =
    (settingsForm.forwardEcsAddress || '').trim() === DEFAULT_FORWARD_ECS;
  return fakeDefault && dnsDefault && addrDefault && proxyDefault && forwardDefault;
});
const hasGuideHistory = computed(() => Array.isArray(guideHistory.value) && guideHistory.value.length > 0);

// Provide state/methods to child components (MosdnsOverview, MosdnsListManagement)
provide('mosdnsState', {
  mosdns,
  config,
  uiSettings,
  settingsForm,
  guideHistory,
  actionPending,
  pendingAction,
  updatePending,
  configDownloading,
  progressTickers,
});
provide('mosdnsActions', {
  loadServiceStatus, loadConfigStatus, loadLogs, saveComponentSettings,
  openModal, openLogsModal, handleStartOrRestart, toggleMosdns,
  refreshVersion, applyLatest, previewConfig, handleDownloadConfig,
  openGuideLog, openPreferencesModal, openConfigEditor, saveConfigPreferences,
  appendGuideHistory,
  handleAutoRefreshToggle,
});
provide('modalControls', {
  openModal, closeModal, openLogsModal, closeLogsModal,
  openConfigEditor, closeConfigEditor, openPreferencesModal, closePreferencesModal,
  openPreviewModal, closePreviewModal, openGuideLog, closeGuideLog,
});
// Special provides for list management
provide('listApi', { getListContent, saveListContent, getSwitchValue, setSwitchValue });

// --- Lifecycle Hooks ---
onMounted(() => {
  restoreLatestVersion();
  loadServiceStatus();
  loadConfigStatus();
  loadSettings();
  loadLogs(); // Initial log load
  applySettings(); // Start/stop auto log refresh based on settings
});

onUnmounted(() => {
  stopAutoLogRefresh();
  stopAllProgressTickers();
});
</script>

<template>
  <!-- Global Info Modal -->
  <BaseModal v-model="showInfoModal.open" :title="showInfoModal.title">
    <p>{{ showInfoModal.message }}</p>
    <template #actions>
      <button class="btn primary" @click="closeModal">我知道了</button>
    </template>
  </BaseModal>

  <!-- Logs Modal -->
  <BaseModal v-model="logsModalOpen" title="mosdns 运行日志" size="logs">
    <div class="log-list modal__logs" v-if="logsEntries.length">
      <div class="log-item" v-for="item in logsEntries" :key="item.timestamp + item.message">
        <time>{{ formatTime(item.timestamp) }}</time>
        <span>{{ item.message }}</span>
      </div>
    </div>
    <p class="muted" v-else>暂无 mosdns 日志。</p>
    <template #actions>
      <button class="btn" @click="loadLogs" :disabled="logsLoading">
        {{ logsLoading ? '刷新中…' : '刷新' }}
      </button>
      <button class="btn primary" @click="closeLogsModal">关闭</button>
    </template>
  </BaseModal>

  <!-- Config Edit Modal -->
  <BaseModal v-model="configEditModalOpen" title="修改配置路径">
    <input
      class="modal__input"
      type="text"
      v-model="configEditValue"
      placeholder="例如 /etc/herobox/mosdns/config.yaml"
    />
    <p class="muted">更新后将以新路径检测配置文件，并据此允许启动 mosdns。</p>
    <template #actions>
      <button class="btn" @click="closeConfigEditor">取消</button>
      <button class="btn primary" @click="saveConfigPath" :disabled="configSaving">
        {{ configSaving ? '保存中…' : '保存' }}
      </button>
    </template>
  </BaseModal>

  <!-- Download Confirm Modal -->
  <BaseModal v-model="downloadConfirmOpen" title="使用默认设置？">
    <p class="muted">当前 FakeIP/国内 DNS/SOCKS5 仍为默认值，下载配置时可能需要手动调整。</p>
    <p>如需立即修改，请点击“去修改”；若确认继续，将按当前设置写入配置。</p>
    <template #actions>
      <button class="btn" @click="modifyPreferencesInstead">去修改</button>
      <button class="btn primary" @click="proceedDownloadWithDefaults">继续下载</button>
    </template>
  </BaseModal>

  <!-- Guide Log Modal -->
  <BaseModal v-model="guideLogModalOpen" title="向导日志" size="logs">
    <div class="guide-log" v-if="hasGuideHistory">
      <div class="guide-log__entry" v-for="entry in guideHistory" :key="entry.ts">
        <h4>{{ formatTime(entry.ts) }}</h4>
        <ul class="guide-card__list">
          <li v-for="(step, idx) in entry.steps" :key="idx" class="guide-step">
            <span class="guide-step__status" :class="step.success ? 'success' : 'pending'">
              {{ step.success ? '✓' : '…' }}
            </span>
            <div class="guide-step__content">
              <strong>{{ step.title }}</strong>
              <p>{{ step.detail }}</p>
            </div>
          </li>
        </ul>
      </div>
    </div>
    <p class="muted" v-else>暂无历史记录。</p>
    <template #actions>
      <button class="btn primary" @click="closeGuideLog">关闭</button>
    </template>
  </BaseModal>

  <!-- Preferences Modal -->
  <BaseModal v-model="preferencesModalOpen" title="自定义设置">
    <p class="muted">填写 FakeIP IPv6 段与国内 DNS，下载配置时会自动替换相应字段。</p>
    <div class="config-preferences">
      <div class="config-preferences__row">
        <label>FakeIP IPv6 段</label>
        <input
          type="text"
          v-model="preferencesDraft.fakeIpRange"
          placeholder="例如 f2b0::/18"
        />
      </div>
      <div class="config-preferences__row">
        <label>国内 DNS</label>
        <input
          type="text"
          v-model="preferencesDraft.domesticDns"
          placeholder="例如 114.114.114.114"
        />
      </div>
      <div class="config-preferences__row">
        <label>SOCKS5 地址</label>
        <input
          type="text"
          v-model="preferencesDraft.socks5Address"
          placeholder="127.0.0.1:7891"
        />
        <span class="muted">填写 SOCKS5 地址后将自动启用相关规则，留空则视为关闭。</span>
      </div>
      <div class="config-preferences__row">
        <label>forward_nocn_ecs IPv6 /64</label>
        <input
          type="text"
          v-model="preferencesDraft.forwardEcsAddress"
          placeholder="2408:8214:213::1"
        />
        <span class="muted">可填公网 IPv6 /64 段或运营商下发的 DNS IP。</span>
      </div>
      <div class="config-preferences__row">
        <label>Proxy 入站地址</label>
        <input
          type="text"
          v-model="preferencesDraft.proxyInboundAddress"
          placeholder="127.0.0.1:7874"
        />
        <span class="muted">将替换配置中的 Proxy 入站监听地址，默认 127.0.0.1:7874。</span>
      </div>
      <details class="preferences-collapse">
        <summary>展开高级选项</summary>
        <div class="config-preferences__row">
          <label>国内 FakeDNS 输出</label>
          <input
            type="text"
            v-model="preferencesDraft.domesticFakeDnsAddress"
            placeholder="udp://127.0.0.1:7874"
          />
          <span class="muted">替换模板中的 udp://127.0.0.1:1053，指向本地 sing-box / mihomo FakeDNS。</span>
        </div>
        <div class="config-preferences__row">
          <label>7777 监听地址</label>
          <input
            type="text"
            v-model="preferencesDraft.listenAddress7777"
            placeholder=":7777"
          />
          <span class="muted">将 127.0.0.1:7777 改写为此值，默认为 :7777 以取消仅本地监听。</span>
        </div>
        <div class="config-preferences__row">
          <label>8888 监听地址</label>
          <input
            type="text"
            v-model="preferencesDraft.listenAddress8888"
            placeholder=":8888"
          />
          <span class="muted">将 127.0.0.1:8888 改写为此值。</span>
        </div>
        <p class="muted">阿里云私有 DoH（可选，如未启用可留空）</p>
        <div class="config-preferences__row">
          <label>阿里 DoH ECS IPv4</label>
          <input
            type="text"
            v-model="preferencesDraft.aliyunDohEcsIp"
            placeholder="例如 27.211.149.114"
          />
          <span class="muted">填 ipw.cn 查询的公网 IPv4，如不需要可留空。</span>
        </div>
        <div class="config-preferences__row">
          <label>阿里 DoH ID</label>
          <input
            type="text"
            v-model="preferencesDraft.aliyunDohId"
            placeholder="6 位账号编号"
          />
        </div>
        <div class="config-preferences__row">
          <label>阿里 DoH Key ID</label>
          <input
            type="text"
            v-model="preferencesDraft.aliyunDohKeyId"
            placeholder="24 位 Key ID"
          />
        </div>
        <div class="config-preferences__row">
          <label>阿里 DoH Key Secret</label>
          <input
            type="password"
            v-model="preferencesDraft.aliyunDohKeySecret"
            placeholder="32 位秘钥"
          />
        </div>
      </details>
    </div>
    <template #actions>
      <button class="btn" @click="closePreferencesModal">取消</button>
      <button class="btn primary" @click="savePreferencesFromModal" :disabled="settingsSaving">
        {{ settingsSaving ? '保存中…' : '保存' }}
      </button>
    </template>
    <ProgressBar v-if="settingsSaveProgress > 0" :progress="settingsSaveProgress" />
    <p class="muted" v-if="settingsSaving || settingsSaveProgress > 0">正在写入配置偏好，请稍候…</p>
  </BaseModal>

  <!-- Preview Modal -->
  <BaseModal v-model="previewModalOpen" title="配置预览" size="logs">
    <p class="muted">当前目录：{{ previewDirLabel }}</p>
    <div class="preview-layout" v-if="previewFlatList.length">
      <div class="preview-tree">
        <button
          v-for="node in previewFlatList"
          :key="node.key"
          class="preview-node"
          :class="{ active: !node.isDir && node.path === previewActiveFile }"
          :style="{ paddingLeft: (node.level * 16) + 'px' }"
          @click="handlePreviewNodeClick(node)"
        >
          <span class="preview-node__icon">
            <template v-if="node.isDir">{{ node.expanded ? '▼' : '▶' }}</template>
            <template v-else>•</template>
          </span>
          <span>{{ node.name }}</span>
        </button>
      </div>
      <div class="preview-editor">
        <textarea
          class="modal__textarea"
          :value="previewDisplayedContent"
          @input="handlePreviewInput"
          :readonly="!previewActiveFile"
          placeholder="选择左侧的配置文件以查看和编辑"
        ></textarea>
        <ProgressBar v-if="previewSaveProgress > 0" :progress="previewSaveProgress" />
        <p class="muted" v-if="previewSaving || previewSaveProgress > 0">
          正在写入配置，请稍候…
        </p>
      </div>
    </div>
    <p class="muted" v-else>目录为空或无可预览的配置文件。</p>
    <p class="muted" v-if="previewError">{{ previewError }}</p>
    <template #actions>
      <button class="btn" @click="reloadPreview" :disabled="previewLoading">
        {{ previewLoading ? '读取中…' : '重新加载' }}
      </button>
      <button class="btn" @click="savePreviewFile" :disabled="previewSaving || !previewActiveFile">
        {{ previewSaving ? '保存中…' : '保存' }}
      </button>
      <button class="btn primary" @click="closePreviewModal">关闭</button>
    </template>
  </BaseModal>

  <RouterView />
</template>

<style scoped>
.config-preferences {
  max-height: 60vh;
  overflow-y: auto;
  padding-right: 0.5rem;
}

/* Scoped styles specific to MosdnsPage if needed */
details.preferences-collapse {
  margin-top: 0.75rem;
}

details.preferences-collapse summary {
  cursor: pointer;
  color: #409eff;
  font-weight: 500;
  margin-bottom: 0.4rem;
}
</style>
