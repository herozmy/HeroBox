import { createRouter, createWebHistory } from 'vue-router';
import Dashboard from '../views/Dashboard.vue';
import MosdnsPage from '../views/Mosdns/MosdnsPage.vue';

const routes = [
  {
    path: '/',
    name: 'Dashboard',
    component: Dashboard,
  },
  {
    path: '/mosdns',
    name: 'Mosdns',
    component: MosdnsPage,
    // MosdnsPage internally manages 'overview' and 'lists' sections
  },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
