<script setup>
import { ref, reactive, computed, onMounted, inject } from 'vue';
import { LIST_DEFINITIONS } from '../../api.js';

const setBanner = inject('setBanner');
const { getListContent, saveListContent, updateAdguardRules } = inject('listApi');

const listManager = reactive({
  entries: LIST_DEFINITIONS,
  current: LIST_DEFINITIONS[2].tag, // Default to 'greylist'
  content: '',
  loading: false,
  saving: false,
  info: '尚未加载',
  error: '',
  lines: 0,
  changed: false,
  lastSaved: '',
  newEntry: '',
  initialized: {},
});

const currentListMeta = computed(() => {
  return listManager.entries.find((item) => item.tag === listManager.current) || listManager.entries[0];
});

const setActiveList = (tag) => {
  if (listManager.current === tag) return;
  listManager.current = tag;
  listManager.content = '';
  listManager.info = '正在加载…';
  listManager.error = '';
  listManager.lastSaved = '';
  listManager.newEntry = '';
  ensureListLoaded(tag, true);
};

const ensureListLoaded = async (tag, force = false) => {
  const initMap = listManager.initialized || {};
  if (!force && initMap[tag]) return;
  await loadList(tag);
};

const loadList = async (tag) => {
  listManager.loading = true;
  listManager.error = '';
  try {
    const text = await getListContent(tag);
    const normalized = typeof text === 'string' ? text : (text == null ? '' : String(text));
    listManager.content = normalized.replace(/\r\n/g, '\n').replace(/\s+$/, '');
    updateListStats(false);
    listManager.initialized = {
      ...listManager.initialized,
      [tag]: true,
    };
    listManager.changed = false;
    listManager.lastSaved = '';
  } catch (err) {
    listManager.error = err.message || '加载失败';
    setBanner('error', `${tag} 读取失败：${err.message}`);
  } finally {
    listManager.loading = false;
  }
};

const handleListInput = (event) => {
  listManager.content = event.target.value;
  updateListStats(true);
};

const updateListStats = (markChanged) => {
  const lines = (listManager.content || '')
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
  listManager.lines = lines.length;
  listManager.info = lines.length ? `共 ${lines.length} 行` : '暂无条目';
  if (markChanged) {
    listManager.changed = true;
  }
};

const appendListEntry = () => {
  const value = (listManager.newEntry || '').trim();
  if (!value) {
    setBanner('error', '请输入要添加的条目');
    return;
  }
  const next = listManager.content ? `${listManager.content}\n${value}` : value;
  listManager.content = next;
  listManager.newEntry = '';
  updateListStats(true);
};

const buildListPayload = () => {
  const values = (listManager.content || '')
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
  return { values };
};

const saveCurrentList = async () => {
  if (listManager.saving) return;
  const payload = buildListPayload();
  listManager.saving = true;
  listManager.error = '';
  try {
    await saveListContent(listManager.current, payload.values);
    listManager.changed = false;
    listManager.lastSaved = new Date().toLocaleString('zh-CN', { hour12: false }); // Use locale string directly
    setBanner('success', `${currentListMeta.value.name} 已保存`);
    updateListStats(false);
  } catch (err) {
    listManager.error = err.message || '保存失败';
    setBanner('error', `${currentListMeta.value.name} 保存失败：${err.message}`);
  } finally {
    listManager.saving = false;
  }
};

const handleUpdateAdguard = async () => {
  try {
    await updateAdguardRules();
    setBanner('success', 'AdGuard 规则更新任务已触发');
  } catch (err) {
    setBanner('error', `AdGuard 更新失败：${err.message}`);
  }
};

onMounted(() => {
  // Ensure the default list is loaded when component is mounted
  ensureListLoaded(listManager.current);
});
</script>

<template>
  <div class="cards cards--lists">
    <article class="card card--lists">
      <div class="card__header">
        <h2>名单管理</h2>
        <div class="card__actions">
          <button class="btn" @click="loadList(listManager.current)" :disabled="listManager.loading">
            {{ listManager.loading ? '读取中…' : '重新读取' }}
          </button>
          <button class="btn" type="button" @click="handleUpdateAdguard">更新 AdGuard</button>
          <button class="btn primary" @click="saveCurrentList" :disabled="listManager.saving || listManager.loading">
            {{ listManager.saving ? '保存中…' : '保存' }}
          </button>
        </div>
      </div>
      <div class="list-tabs">
        <button
          v-for="entry in listManager.entries"
          :key="entry.tag"
          class="list-tab"
          :class="{ active: entry.tag === listManager.current }"
          type="button"
          @click="setActiveList(entry.tag)"
        >{{ entry.name }}</button>
      </div>
      <p class="muted">使用 mosdns `/plugins/{{ listManager.current }}/*` 接口实时读取与写入名单。</p>
      <div class="greylist-toolbar">
        <input
          type="text"
          class="greylist-input"
          v-model="listManager.newEntry"
          @keyup.enter="appendListEntry"
          :placeholder="currentListMeta.placeholder"
          :disabled="listManager.loading"
        />
        <button class="btn" type="button" @click="appendListEntry" :disabled="listManager.loading">
          添加一行
        </button>
        <span class="muted">{{ listManager.info }}</span>
      </div>
      <textarea
        class="list-textarea"
        :value="listManager.content"
        @input="handleListInput"
        :disabled="listManager.loading"
        placeholder="每行一个条目，例如 full:domain"
      ></textarea>
      <p class="muted" v-if="listManager.error">{{ listManager.error }}</p>
      <p class="muted" v-else>
        <span v-if="listManager.lastSaved">最近保存：{{ listManager.lastSaved }}。</span>
        <span v-if="listManager.changed">存在未保存的修改。</span>
      </p>
    </article>

    <article class="card card--lists">
      <div class="card__header">
        <h2>说明</h2>
      </div>
      <ul class="dashboard-list">
        <li>
          <span>白/黑/灰名单</span>
          <span>支持 `full:domain`、通配符等 mosdns 原生语法。</span>
        </li>
        <li>
          <span>DDNS 列表</span>
          <span>建议仅填 root 域名，实时走本地 forward。</span>
        </li>
        <li>
          <span>客户端 IP</span>
          <span>使用 CIDR 表达式限制访问，例如 `192.168.1.0/24`。</span>
        </li>
        <li>
          <span>AdGuard 更新</span>
          <span>调用 `/plugins/adguard/update` 立即刷新规则。</span>
        </li>
      </ul>
      <p class="muted">若需要新增名单类型，可在 mosdns 子配置追加插件后再扩展前端。</p>
    </article>
  </div>
</template>

<style scoped>
/* Scoped styles for MosdnsListManagement.vue */
.dashboard-list { /* Assuming this class is used within this component specifically */
  list-style: none;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.dashboard-list li {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.dashboard-list li span:first-child {
  font-weight: 600;
  color: #1f2b3a;
}
.dashboard-list li span:last-child {
  font-size: 13px;
  color: #6b7a90;
}
</style>
