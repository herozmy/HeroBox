<script setup>
import { defineProps, defineEmits } from 'vue';

const props = defineProps({
  type: {
    type: String,
    default: 'info', // 'success', 'error', 'info'
  },
  message: {
    type: String,
    required: true,
  },
});

const emit = defineEmits(['close']);

const onClose = () => {
  emit('close');
};
</script>

<template>
  <div class="alert" :class="`alert--${type}`">
    <span>{{ message }}</span>
    <button class="alert__close" @click="onClose">×</button>
  </div>
</template>

<style scoped>
/* No specific scoped styles needed if base styles are in global.css */
</style>
