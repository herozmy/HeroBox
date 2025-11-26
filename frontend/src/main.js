import { createApp, reactive, provide } from 'vue';
import App from './App.vue';
import router from './router';

// Global state for banners, can be provided to all components
const globalState = reactive({
  banner: null,
});

const app = createApp(App);

app.use(router);

// Provide global state to all components
app.provide('globalState', globalState);

app.mount('#app');
