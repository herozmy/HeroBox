<script setup>
import { defineProps, defineEmits } from 'vue';

const props = defineProps({
  title: String,
  modelValue: Boolean, // For v-model
  size: {
    type: String,
    default: 'default', // 'default', 'logs', 'large' etc.
  },
});

const emit = defineEmits(['update:modelValue', 'close']);

const closeModal = () => {
  emit('update:modelValue', false);
  emit('close');
};
</script>

<template>
  <div v-if="modelValue" class="modal-backdrop" @click.self="closeModal">
    <div class="modal" :class="`modal--${size}`">
      <div class="modal__header" v-if="title || $slots.header">
        <h3 v-if="title">{{ title }}</h3>
        <slot name="header">
          <button class="alert__close" @click="closeModal">×</button>
        </slot>
      </div>
      <slot></slot>
      <div class="button-row modal__actions" v-if="$slots.actions">
        <slot name="actions"></slot>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Scoped styles specific to BaseModal if needed, otherwise global.css */
</style>
