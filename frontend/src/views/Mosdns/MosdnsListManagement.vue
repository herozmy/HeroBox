<script setup>
import { ref, reactive, computed, onMounted, inject } from 'vue';
import { LIST_DEFINITIONS } from '../../api.js';
import BaseModal from '../../components/BaseModal.vue';

const setBanner = inject('setBanner');
const { getListContent, saveListContent, getSwitchValue, setSwitchValue } = inject('listApi');

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

const infoModalOpen = ref(false);
const infoItems = [
  { title: '白/黑/灰名单', desc: '支持 `full:domain`、通配符等 mosdns 原生语法。' },
  { title: 'DDNS 列表', desc: '建议仅填 root 域名，实时走本地 forward。' },
  { title: '客户端 IP', desc: '使用 CIDR 表达式限制访问，例如 `192.168.1.0/24`。' },
];

const modeOptions = [
  { label: '兼容模式', value: 'compatible' },
  { label: '安全模式', value: 'secure' },
];

const featureDefinitions = [
  { key: 'requestShield', label: '请求屏蔽开关', desc: '对无解析结果的请求进行屏蔽', icon: '🛡️' },
  { key: 'typeShield', label: '类型屏蔽开关', desc: '屏蔽 SOA、PTR、HTTPS 等请求类型', icon: '⚙️' },
  { key: 'ipv6Shield', label: 'IPV6屏蔽开关', desc: '屏蔽 AAAA 请求类型', icon: '🌐' },
  { key: 'clientLimit', label: '指定 Client 开关', desc: '对特定客户端 IP 应用分流策略', icon: '👥' },
  { key: 'lazyCache', label: '过期缓存开关', desc: '启用 Lazy Cache（乐观缓存）', icon: '♻️' },
];

const featureState = reactive({
  mode: 'compatible',
  toggles: {
    requestShield: true,
    typeShield: true,
    ipv6Shield: false,
    clientLimit: false,
    lazyCache: true,
  },
});

const switchBindings = [
  { key: 'requestShield', tag: 'switch1', onValue: 'A', offValue: 'B' },
  { key: 'clientLimit', tag: 'switch2', onValue: 'A', offValue: 'B' },
  { key: 'lazyCache', tag: 'switch4', onValue: 'A', offValue: 'B' },
  { key: 'typeShield', tag: 'switch5', onValue: 'A', offValue: 'B' },
  { key: 'ipv6Shield', tag: 'switch6', onValue: 'A', offValue: 'B' },
];

const modeBinding = { key: 'mode', tag: 'switch3', compatibleValue: 'A', secureValue: 'B' };

const switchesState = reactive({
  loading: false,
  savingKey: '',
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

const parseSwitchValue = (resp) => {
  if (resp == null) return '';
  if (typeof resp === 'string') return resp.trim();
  if (typeof resp === 'object') {
    if (resp.value != null) return String(resp.value).trim();
    if (resp.data != null) return String(resp.data).trim();
  }
  return '';
};

const loadSwitchStates = async () => {
  if (!getSwitchValue) return;
  switchesState.loading = true;
  try {
    const toggleResults = await Promise.all(
      switchBindings.map(async (binding) => {
        const resp = await getSwitchValue(binding.tag);
        return { key: binding.key, value: parseSwitchValue(resp) };
      }),
    );
    toggleResults.forEach(({ key, value }) => {
      const binding = switchBindings.find((item) => item.key === key);
      if (!binding) return;
      featureState.toggles[key] = value === (binding.onValue || 'A');
    });
    const modeResp = await getSwitchValue(modeBinding.tag);
    const modeValue = parseSwitchValue(modeResp);
    featureState.mode = modeValue === modeBinding.secureValue ? 'secure' : 'compatible';
  } catch (err) {
    if (setBanner) {
      setBanner('error', `获取开关状态失败：${err.message}`);
    }
  } finally {
    switchesState.loading = false;
  }
};

const applySwitchChange = async (binding, value, onSuccess) => {
  if (!setSwitchValue) return;
  switchesState.savingKey = binding.key;
  try {
    await setSwitchValue(binding.tag, value);
    onSuccess();
    setBanner && setBanner('success', '已更新 mosdns 开关');
  } catch (err) {
    setBanner && setBanner('error', `更新开关失败：${err.message}`);
    await loadSwitchStates();
  } finally {
    switchesState.savingKey = '';
  }
};

const setFeatureMode = async (mode) => {
  if (featureState.mode === mode) return;
  const value = mode === 'secure' ? modeBinding.secureValue : modeBinding.compatibleValue;
  await applySwitchChange({ key: 'mode', tag: modeBinding.tag }, value, () => {
    featureState.mode = mode;
  });
};

const toggleFeature = async (key) => {
  const binding = switchBindings.find((item) => item.key === key);
  if (!binding) {
    featureState.toggles[key] = !featureState.toggles[key];
    return;
  }
  const current = featureState.toggles[key];
  const targetValue = current ? (binding.offValue || 'B') : (binding.onValue || 'A');
  await applySwitchChange(binding, targetValue, () => {
    featureState.toggles[key] = !current;
  });
};

onMounted(() => {
  // Ensure the default list is loaded when component is mounted
  ensureListLoaded(listManager.current);
  loadSwitchStates();
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
          <button class="btn primary" @click="saveCurrentList" :disabled="listManager.saving || listManager.loading">
            {{ listManager.saving ? '保存中…' : '保存' }}
          </button>
          <button class="btn" type="button" @click="infoModalOpen = true">查看说明</button>
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

    <article class="card card--advanced">
      <div class="card__header">
        <h2>高级功能</h2>
      </div>
      <div class="mode-tabs">
        <button
          v-for="mode in modeOptions"
          :key="mode.value"
          class="mode-tab"
          :class="{ active: featureState.mode === mode.value }"
          type="button"
          :disabled="switchesState.loading || switchesState.savingKey === 'mode'"
          @click="setFeatureMode(mode.value)"
        >{{ mode.label }}</button>
      </div>
      <p class="muted" v-if="switchesState.loading">正在同步开关状态…</p>
      <ul class="feature-switches">
        <li v-for="feature in featureDefinitions" :key="feature.key" class="feature-switch">
          <div>
            <strong>{{ feature.icon }} {{ feature.label }}</strong>
            <p class="muted">{{ feature.desc }}</p>
          </div>
          <label class="switch">
            <input
              type="checkbox"
              :checked="featureState.toggles[feature.key]"
              :disabled="switchesState.loading || switchesState.savingKey === feature.key"
              @change="toggleFeature(feature.key)"
            />
          </label>
        </li>
      </ul>
    </article>
  </div>

  <BaseModal v-model="infoModalOpen" title="名单说明">
    <ul class="dashboard-list">
      <li v-for="item in infoItems" :key="item.title">
        <span>{{ item.title }}</span>
        <span>{{ item.desc }}</span>
      </li>
    </ul>
    <p class="muted">若需要新增名单类型，可在 mosdns 子配置追加插件后再扩展前端。</p>
    <template #actions>
      <button class="btn primary" @click="infoModalOpen = false">关闭</button>
    </template>
  </BaseModal>

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
.mode-tabs {
  display: flex;
  gap: 12px;
  flex-wrap: wrap;
  margin: 12px 0 8px;
}
.mode-tab {
  flex: 1;
  min-width: 120px;
  border: 1px solid var(--border);
  padding: 10px;
  border-radius: 999px;
  background: #f5f7fb;
  cursor: pointer;
}
.mode-tab.active {
  background: var(--brand);
  color: #fff;
  border-color: var(--brand);
}
.feature-switches {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.feature-switch {
  display: flex;
  justify-content: space-between;
  align-items: center;
  border: 1px solid var(--border);
  border-radius: 12px;
  padding: 12px 16px;
}
.feature-switch strong {
  font-size: 15px;
  display: block;
  margin-bottom: 6px;
}
</style>
