<script setup>
import { defineProps, defineEmits, computed } from 'vue';
import ProgressBar from '../../components/ProgressBar.vue';
import { DEFAULT_FAKEIP_RANGE, DEFAULT_DOMESTIC_DNS, DEFAULT_SOCKS5_ADDRESS, DEFAULT_PROXY_INBOUND, DEFAULT_FORWARD_ECS } from '../../api.js';

const props = defineProps({
  mosdns: Object,
  config: Object,
  uiSettings: Object,
  settingsForm: Object,
  actionPending: Boolean,
  pendingAction: String,
  updatePending: Boolean,
  configDownloading: Boolean,
  configDownloadProgress: Number, // Value from the parent
  guideHistory: Array,
  settingsSaveProgress: Number, // Value from the parent
});

const emit = defineEmits([
  'start-or-restart',
  'toggle-mosdns',
  'refresh-version',
  'apply-latest',
  'open-logs-modal',
  'preview-config',
  'download-config',
  'open-guide-log',
  'open-preferences-modal',
  'open-config-editor',
  'toggle-auto-refresh',
]);

const updateAvailable = computed(() => {
  if (!props.mosdns.version || !props.mosdns.latestVersion) return false;
  return props.mosdns.version !== props.mosdns.latestVersion;
});
const isMissing = computed(() => props.mosdns.status === 'missing');
const isRunning = computed(() => props.mosdns.status === 'running');
const canStart = computed(() => !isMissing.value && !isRunning.value && props.config.exists);
const startButtonLabel = computed(() => {
  if (props.actionPending && props.pendingAction === 'start') return '启动中…';
  if (props.actionPending && props.pendingAction === 'restart') return '重启中…';
  if (isRunning.value) return '重启';
  return '启动';
});
const startButtonDisabled = computed(() => {
  if (props.actionPending) return true;
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

const usingDefaultPreferences = computed(() => {
  const fakeDefault = (props.settingsForm.fakeIpRange || '').trim() === DEFAULT_FAKEIP_RANGE;
  const dnsDefault = (props.settingsForm.domesticDns || '').trim() === DEFAULT_DOMESTIC_DNS;
  const addrDefault = (props.settingsForm.socks5Address || '').trim() === DEFAULT_SOCKS5_ADDRESS;
  const proxyDefault =
    (props.settingsForm.proxyInboundAddress || '').trim() === DEFAULT_PROXY_INBOUND;
  const forwardDefault =
    (props.settingsForm.forwardEcsAddress || '').trim() === DEFAULT_FORWARD_ECS;
  return fakeDefault && dnsDefault && addrDefault && proxyDefault && forwardDefault;
});
const hasGuideHistory = computed(() => Array.isArray(props.guideHistory) && props.guideHistory.length > 0);
</script>

<template>
  <div class="cards">
    <article class="card">
      <h2>运行状态</h2>
      <div class="status" :class="statusClass">
        <span class="status-dot"></span>
        {{ mosdns.lastUpdated === '-' ? statusLabel : mosdns.status === 'running' ? '正在运行' : '已停止'}}
      </div>
      <div class="muted">最近更新：{{ mosdns.lastUpdated }}</div>
      <div class="muted" v-if="isMissing">未检测到 mosdns 核心，请先更新/安装内核。</div>
      <ul class="status-meta">
        <li>
          <span class="status-meta__label">当前配置</span>
          <span class="status-meta__value" :title="config.path">{{ config.path }}</span>
        </li>
        <li>
          <span class="status-meta__label">配置状态</span>
          <span class="status-meta__value">{{ config.exists ? '已就绪' : '缺失' }}</span>
        </li>
        <li>
          <span class="status-meta__label">日志刷新</span>
          <span class="status-meta__value">{{ uiSettings.autoRefreshLogs ? '自动' : '手动' }}</span>
        </li>
        <li>
          <span class="status-meta__label">上次同步</span>
          <span class="status-meta__value">{{ config.lastSynced }}</span>
        </li>
      </ul>
      <div class="button-row">
        <button
          class="btn primary"
          @click="emit('start-or-restart')"
          :disabled="startButtonDisabled"
        >
          {{ startButtonLabel }}
        </button>
        <button class="btn" @click="emit('toggle-mosdns', false)" :disabled="!canStop || actionPending">
          {{ actionPending && pendingAction === 'stop' ? '停止中…' : '停止' }}
        </button>
      </div>
    </article>

    <article class="card">
      <h2>mosdns 版本号</h2>
      <div class="version-tag">{{ mosdns.version }}</div>
      <div class="muted">最新稳定版：{{ mosdns.latestVersion }}</div>
      <div class="button-row">
        <button class="btn primary" @click="emit('refresh-version', false)" :disabled="mosdns.checking">
          {{ mosdns.checking ? '检测中...' : '检查更新' }}
        </button>
        <button class="btn" @click="emit('apply-latest')" :disabled="(!updateAvailable && !isMissing) || updatePending">
          {{ updatePending ? '更新中…' : '一键更新' }}
        </button>
      </div>
    </article>

    <article class="card">
      <div class="card__header">
        <h2>配置管理</h2>
        <div class="card__actions">
          <button class="btn" @click="emit('open-logs-modal')">查看日志</button>
        </div>
      </div>
      <p class="muted">当前配置：{{ config.path }}</p>
      <div class="muted">最近同步：{{ config.lastSynced }}</div>
      <div class="muted" v-if="!config.exists">
        未检测到配置文件，请先下载模板并放置到上述路径后再启动 mosdns。
      </div>
      <div class="setting-row">
        <label class="switch">
          <input type="checkbox" v-model="uiSettings.autoRefreshLogs" @change="emit('toggle-auto-refresh')" />
          自动刷新日志
        </label>
        <span class="muted">{{ uiSettings.autoRefreshLogs ? '日志将在后台自动刷新' : '关闭自动刷新以减少请求' }}</span>
      </div>
      <div class="button-row">
        <button class="btn" @click="emit('preview-config')">配置编辑</button>
        <button
          class="btn primary"
          @click="emit('download-config')"
          :disabled="configDownloading"
        >{{ configDownloading ? '下载中…' : (config.exists ? '重新下载' : '下载配置') }}</button>
        <button
          class="btn"
          v-if="hasGuideHistory"
          @click="emit('open-guide-log')"
        >向导日志</button>
      </div>
      <ProgressBar v-if="configDownloading" :progress="configDownloadProgress" />
      <p class="muted" v-if="configDownloading">下载进度：{{ Math.min(100, Math.round(configDownloadProgress)) }}%</p>
      <p class="muted" v-if="hasGuideHistory">
        最新向导结果已记录，可通过“向导日志”查看详细步骤。
      </p>
    </article>

    <article class="card">
      <h2>自定义设置</h2>
      <p class="muted">FakeIP IPv6 段：{{ settingsForm.fakeIpRange }}</p>
      <p class="muted">国内 DNS：{{ settingsForm.domesticDns }}</p>
      <p class="muted">SOCKS5 代理：{{ settingsForm.enableSocks5 ? '启用' : '关闭' }}</p>
      <p class="muted">Proxy 入站地址：{{ settingsForm.proxyInboundAddress }}</p>
      <details class="preferences-collapse">
        <summary>展开更多字段</summary>
        <p class="muted">SOCKS5 地址：{{ settingsForm.socks5Address || '未设置' }}</p>
        <p class="muted">forward_nocn_ecs：{{ settingsForm.forwardEcsAddress }}</p>
        <p class="muted">国内 FakeDNS：{{ settingsForm.domesticFakeDnsAddress || '未设置' }}</p>
        <p class="muted">7777 监听：{{ settingsForm.listenAddress7777 || '未设置' }}</p>
        <p class="muted">8888 监听：{{ settingsForm.listenAddress8888 || '未设置' }}</p>
        <p class="muted">阿里 DoH ECS IPv4：{{ settingsForm.aliyunDohEcsIp || '未配置' }}</p>
        <p class="muted">阿里 DoH ID：{{ settingsForm.aliyunDohId || '未配置' }}</p>
        <p class="muted">阿里 DoH Key ID：{{ settingsForm.aliyunDohKeyId || '未配置' }}</p>
      </details>
      <p class="muted">阿里 DoH 密钥：{{ settingsForm.aliyunDohKeySecret ? '已配置' : '未配置' }}</p>
      <p class="muted" v-if="usingDefaultPreferences">当前仍为默认值，下载前建议调整。</p>
      <div class="button-row">
        <button class="btn" @click="emit('open-config-editor')">修改路径</button>
        <button class="btn primary" @click="emit('open-preferences-modal')">编辑设置</button>
      </div>
    </article>
  </div>
</template>

<style scoped>
/* Scoped styles for MosdnsOverview.vue */
details.preferences-collapse {
  margin-top: 0.5rem;
}

details.preferences-collapse summary {
  cursor: pointer;
  color: #409eff;
  font-weight: 500;
  margin-bottom: 0.4rem;
}
</style>
