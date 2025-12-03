<script setup>
import { inject, computed, watch } from 'vue';
import MosdnsListManagement from './MosdnsListManagement.vue';

const state = inject('mosdnsState', null);
const setBanner = inject('setBanner', () => {});
const isReady = computed(() => (state?.mosdns?.status || '').toLowerCase() === 'running');

watch(
  () => state?.mosdns?.status,
  (status) => {
    if (status && status.toLowerCase() !== 'running') {
      setBanner && setBanner('error', 'mosdns 未运行，名单管理暂不可用');
    }
  },
);
</script>

<template>
  <header class="content__header">
    <div>
      <div class="muted">高级管理</div>
      <h1>Mosdns 高级功能</h1>
    </div>
  </header>
  <section v-if="isReady">
    <MosdnsListManagement />
  </section>
  <section v-else>
    <div class="empty-state">
      <h2>mosdns 未运行</h2>
      <p>请先在“总览”页启动 mosdns 后再使用高级管理功能。</p>
    </div>
  </section>
</template>
