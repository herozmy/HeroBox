<script setup>
import { ref, watch, provide } from 'vue';
import { useRouter, useRoute } from 'vue-router';
import AlertBanner from './components/AlertBanner.vue';

const router = useRouter();
const route = useRoute();

const mosdnsNavExpanded = ref(false);

const banner = ref(null);
const setBanner = (type, text) => {
  if (!text) return;
  banner.value = { type, text, ts: Date.now() };
};
// Provide setBanner function to child components
provide('setBanner', setBanner);

// Clear banner after some time
watch(banner, (newVal) => {
  if (newVal) {
    setTimeout(() => {
      banner.value = null;
    }, 5000);
  }
});
</script>

<template>
  <div class="layout">
    <aside class="sidebar">
      <div class="logo">HeroBox</div>
      <nav>
        <router-link to="/" class="nav-btn" :class="{ active: route.path === '/' }">
          仪表盘
        </router-link>
        <div class="nav-group">
          <button
            class="nav-btn nav-btn--parent"
            :class="{ active: route.path.startsWith('/mosdns') }"
            type="button"
            @click="mosdnsNavExpanded = !mosdnsNavExpanded"
            :aria-expanded="mosdnsNavExpanded ? 'true' : 'false'"
          >
            <span>Mosdns</span>
            <span class="nav-caret" :style="{ transform: mosdnsNavExpanded ? 'rotate(90deg)' : 'rotate(0deg)' }">▸</span>
          </button>
          <div class="nav-sub" v-show="mosdnsNavExpanded || route.path.startsWith('/mosdns')">
            <router-link
              to="/mosdns?section=overview"
              class="nav-sub__btn"
              :class="{ active: route.path.startsWith('/mosdns') && route.query.section !== 'lists' }"
            >
              总览
            </router-link>
            <router-link
              to="/mosdns?section=lists"
              class="nav-sub__btn"
              :class="{ active: route.path.startsWith('/mosdns') && route.query.section === 'lists' }"
            >
              名单管理
            </router-link>
          </div>
        </div>
      </nav>
    </aside>

    <main class="content">
      <AlertBanner v-if="banner" :type="banner.type" :message="banner.text" @close="banner = null" />
      <router-view />
    </main>
  </div>
</template>

<style scoped>
/* Scoped styles for App.vue, if any.
   Most layout/base styles are in public/styles.css
*/
.nav-caret {
  transform: rotate(0deg); /* Default state */
}
.nav-btn--parent[aria-expanded="true"] .nav-caret {
  transform: rotate(90deg);
}

/* Example of adding some specific styles */
.nav-btn.active {
    background: rgba(255, 255, 255, 0.2); /* Slightly different active state for clarity */
}
</style>
