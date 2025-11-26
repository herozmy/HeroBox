<script setup>
import { defineProps } from 'vue';

const props = defineProps({
  progress: {
    type: Number,
    default: 0, // 0-100
  },
});
</script>

<template>
  <div class="progress">
    <div class="progress__inner" :style="{ width: progress + '%' }"></div>
  </div>
</template>

<style scoped>
.progress {
  width: 100%;
  height: 6px;
  background: #edf1f7;
  border-radius: 999px;
  overflow: hidden;
  margin-top: 12px;
}

.progress__inner {
  height: 100%;
  background: var(--brand);
  border-radius: 999px;
  transition: width 0.2s ease;
}
</style>
