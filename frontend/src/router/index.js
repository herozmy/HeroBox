import { createRouter, createWebHistory } from 'vue-router';
import Dashboard from '../views/Dashboard.vue';
import MosdnsLayout from '../views/Mosdns/MosdnsPage.vue';
import MosdnsOverviewPage from '../views/Mosdns/MosdnsOverviewPage.vue';
import MosdnsAdvancedPage from '../views/Mosdns/MosdnsAdvancedPage.vue';

const routes = [
  {
    path: '/',
    name: 'Dashboard',
    component: Dashboard,
  },
  {
    path: '/mosdns',
    component: MosdnsLayout,
    children: [
      {
        path: '',
        name: 'MosdnsOverview',
        component: MosdnsOverviewPage,
      },
      {
        path: 'advanced',
        name: 'MosdnsAdvanced',
        component: MosdnsAdvancedPage,
      },
    ],
  },
];

const router = createRouter({
  history: createWebHistory(),
  routes,
});

export default router;
