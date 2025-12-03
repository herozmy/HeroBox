<script setup>
import { inject, computed } from 'vue';
import MosdnsOverview from './MosdnsOverview.vue';

const state = inject('mosdnsState', null);
const actions = inject('mosdnsActions', {});

const configDownloadProgress = computed(() => state?.progressTickers?.configDownloadProgressValue || 0);
const settingsSaveProgress = computed(() => state?.progressTickers?.settingsSaveProgressValue || 0);

const actionPending = computed(() => state?.actionPending?.value || false);
const pendingAction = computed(() => state?.pendingAction?.value || '');
const updatePending = computed(() => state?.updatePending?.value || false);
const configDownloading = computed(() => state?.configDownloading?.value || false);
</script>

<template>
  <header class="content__header">
    <div>
      <div class="muted">基础信息</div>
      <h1>Mosdns</h1>
    </div>
  </header>
  <section v-if="state">
    <MosdnsOverview
      :mosdns="state.mosdns"
      :config="state.config"
      :ui-settings="state.uiSettings"
      :settings-form="state.settingsForm"
      :action-pending="actionPending"
      :pending-action="pendingAction"
      :update-pending="updatePending"
      :config-downloading="configDownloading"
      :config-download-progress="configDownloadProgress"
      :guide-history="state.guideHistory"
      :settings-save-progress="settingsSaveProgress"
      @start-or-restart="actions.handleStartOrRestart"
      @toggle-mosdns="actions.toggleMosdns"
      @refresh-version="actions.refreshVersion"
      @apply-latest="actions.applyLatest"
      @open-logs-modal="actions.openLogsModal"
      @preview-config="actions.previewConfig"
      @download-config="actions.handleDownloadConfig"
      @open-guide-log="actions.openGuideLog"
      @open-preferences-modal="actions.openPreferencesModal"
      @open-config-editor="actions.openConfigEditor"
      @toggle-auto-refresh="actions.handleAutoRefreshToggle"
    />
  </section>
  <section v-else>
    <div class="empty-state">
      <h2>正在加载 mosdns 信息</h2>
      <p>稍候片刻即可显示 Mosdns 状态。</p>
    </div>
  </section>
</template>
