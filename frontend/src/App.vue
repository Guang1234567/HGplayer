<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import {
  api,
  ApiException,
  type SeriesCard,
  type SeriesDetail,
  type EpisodeBrief,
} from './api/client';

// —— 视图状态 ——
type View = 'catalog' | 'search' | 'series' | 'player';
const view = ref<View>('catalog');

// —— 全局 ——
const version = ref('');
const versionError = ref('');
const notice = ref('');

// —— 账号 ——
const account = ref({ logged_in: false, account_id: '', display_name: '' });
const loginForm = ref({ id: '', name: '' });
const showLogin = ref(false);

// —— 目录 ——
const categories = [
  { key: 'real-drama', label: '真人短剧' },
  { key: 'ai-drama', label: 'AI 短剧' },
  { key: 'comic-drama', label: '漫剧' },
  { key: 'hot-drama', label: '热播榜' },
];
const category = ref('real-drama');
const cards = ref<SeriesCard[]>([]);
const page = ref(1);
const hasMore = ref(false);
const loading = ref(false);

// —— 搜索 ——
const keyword = ref('');
const searchResults = ref<SeriesCard[]>([]);
const recentSearches = ref<string[]>([]);
const searched = ref(false);

// —— 详情与播放 ——
const series = ref<SeriesDetail | null>(null);
const currentEpisode = ref<EpisodeBrief | null>(null);
const videoSrc = ref('');
const playError = ref('');

// —— 初始化 ——
onMounted(async () => {
  await Promise.all([loadVersion(), loadAccount(), loadRecentSearches(), loadCatalog(true)]);
});

async function loadVersion() {
  try {
    const res = await api.version();
    version.value = res.version;
  } catch (err) {
    versionError.value = err instanceof ApiException ? err.message : String(err);
  }
}

async function loadAccount() {
  try {
    account.value = await api.account();
  } catch {
    account.value = { logged_in: false, account_id: '', display_name: '' };
  }
}

async function loadRecentSearches() {
  try {
    recentSearches.value = (await api.searchHistory()).history ?? [];
  } catch {
    recentSearches.value = [];
  }
}

// —— 目录 ——
async function loadCatalog(reset = false) {
  if (loading.value) return;
  loading.value = true;
  notice.value = '';
  try {
    if (reset) {
      page.value = 1;
      cards.value = [];
    }
    const res = await api.catalog(category.value, '', page.value);
    cards.value = reset ? res.results : [...cards.value, ...res.results];
    hasMore.value = res.hasMore;
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  } finally {
    loading.value = false;
  }
}

function switchCategory(key: string) {
  if (category.value === key) return;
  category.value = key;
  loadCatalog(true);
}

function loadMore() {
  if (!hasMore.value || loading.value) return;
  page.value += 1;
  loadCatalog(false);
}

// —— 搜索 ——
async function doSearch() {
  const q = keyword.value.trim();
  if (!q) return;
  view.value = 'search';
  loading.value = true;
  notice.value = '';
  try {
    const res = await api.search(q, 1);
    searchResults.value = res.results;
    searched.value = true;
    recentSearches.value = (await api.searchHistory()).history ?? [];
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
    searchResults.value = [];
  } finally {
    loading.value = false;
  }
}

async function useRecent(q: string) {
  keyword.value = q;
  await doSearch();
}

async function clearRecent() {
  try {
    recentSearches.value = (await api.updateSearchHistory('clear', '')).history ?? [];
  } catch {
    /* 忽略 */
  }
}

// —— 详情 ——
async function openSeries(id: string) {
  view.value = 'series';
  series.value = null;
  notice.value = '';
  try {
    series.value = await api.series(id);
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  }
}

async function toggleFavorite() {
  if (!series.value) return;
  try {
    const res = await api.toggleFavorite({
      series_id: series.value.series_id,
      series_title: series.value.series_title,
      cover: series.value.cover,
    });
    series.value.is_favorite = res.favorited;
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  }
}

// —— 播放 ——
async function playEpisode(ep: EpisodeBrief) {
  if (!series.value) return;
  view.value = 'player';
  currentEpisode.value = ep;
  videoSrc.value = '';
  playError.value = '';
  try {
    await api.play(ep.vid);
    // 加密片源（CENC）由服务端解密后再按 Range 供给，前端只需同源地址。
    videoSrc.value = `/media/${encodeURIComponent(ep.vid)}`;
  } catch (err) {
    playError.value = err instanceof ApiException ? err.message : String(err);
  }
}

let lastSaved = 0;
async function onTimeUpdate(e: Event) {
  const el = e.target as HTMLVideoElement;
  if (!series.value || !currentEpisode.value) return;
  const now = Date.now();
  if (now - lastSaved < 5000) return;
  lastSaved = now;
  try {
    await api.saveProgress({
      series_id: series.value.series_id,
      series_title: series.value.series_title,
      cover: series.value.cover,
      episode_index: currentEpisode.value.episode_index,
      position_sec: Math.floor(el.currentTime),
      duration_sec: Math.floor(el.duration || 0),
    });
  } catch {
    /* 进度保存失败不影响播放 */
  }
}

// —— 账号操作 ——
async function doLogin() {
  notice.value = '';
  try {
    await api.login(loginForm.value.id.trim(), loginForm.value.name.trim());
    await loadAccount();
    showLogin.value = false;
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  }
}

async function doLogout() {
  try {
    await api.logout();
    await loadAccount();
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  }
}

const episodeCount = computed(() => series.value?.episodes.length ?? 0);
</script>

<template>
  <div class="page">
    <header class="topbar">
      <div class="brand" @click="view = 'catalog'">红果短剧 <span class="tag">Go / Wails</span></div>

      <div class="searchbar">
        <input
          v-model="keyword"
          class="input"
          placeholder="搜索剧名 / 演员 / 题材"
          @keyup.enter="doSearch"
        />
        <button class="btn primary" @click="doSearch">搜索</button>
      </div>

      <div class="topbar-right">
        <span class="version">v{{ version || '…' }}<span v-if="versionError" class="err"> {{ versionError }}</span></span>
        <template v-if="account.logged_in">
          <span class="who">{{ account.display_name || account.account_id }}</span>
          <button class="btn" @click="doLogout">登出</button>
        </template>
        <button v-else class="btn" @click="showLogin = !showLogin">登录</button>
      </div>
    </header>

    <div v-if="showLogin && !account.logged_in" class="loginbar">
      <input v-model="loginForm.id" class="input" placeholder="账号标识" />
      <input v-model="loginForm.name" class="input" placeholder="展示名称" />
      <button class="btn primary" @click="doLogin">登录</button>
    </div>

    <div v-if="notice" class="notice">{{ notice }}</div>

    <!-- 目录页 -->
    <main v-if="view === 'catalog'" class="content">
      <nav class="tabs">
        <button
          v-for="c in categories"
          :key="c.key"
          class="tab"
          :class="{ active: category === c.key }"
          @click="switchCategory(c.key)"
        >
          {{ c.label }}
        </button>
      </nav>

      <div v-if="loading && cards.length === 0" class="muted">加载中…</div>
      <div v-else-if="cards.length === 0" class="muted">暂无内容</div>

      <div class="grid">
        <article v-for="card in cards" :key="card.series_id" class="card" @click="openSeries(card.series_id)">
          <img v-if="card.cover" :src="card.cover" class="cover" :alt="card.series_title" />
          <div v-else class="cover placeholder">无封面</div>
          <div class="card-body">
            <h3>{{ card.series_title }}</h3>
            <p class="muted small">{{ card.episode_count }} 集</p>
            <p v-if="card.tags.length" class="tags">
              <span v-for="t in card.tags" :key="t" class="tag-chip">{{ t }}</span>
            </p>
          </div>
        </article>
      </div>

      <div v-if="hasMore" class="more">
        <button class="btn" :disabled="loading" @click="loadMore">
          {{ loading ? '加载中…' : '加载更多' }}
        </button>
      </div>
    </main>

    <!-- 搜索页 -->
    <main v-else-if="view === 'search'" class="content">
      <section v-if="recentSearches.length" class="recent">
        <div class="recent-head">
          <span class="muted small">最近搜索</span>
          <button class="link" @click="clearRecent">清空</button>
        </div>
        <div class="chips">
          <button v-for="q in recentSearches" :key="q" class="chip" @click="useRecent(q)">{{ q }}</button>
        </div>
      </section>

      <div v-if="loading" class="muted">搜索中…</div>
      <div v-else-if="searched && searchResults.length === 0" class="muted">没有找到相关剧集</div>

      <div class="grid">
        <article v-for="card in searchResults" :key="card.series_id" class="card" @click="openSeries(card.series_id)">
          <img v-if="card.cover" :src="card.cover" class="cover" :alt="card.series_title" />
          <div v-else class="cover placeholder">无封面</div>
          <div class="card-body">
            <h3>{{ card.series_title }}</h3>
            <p class="muted small">{{ card.episode_count }} 集</p>
          </div>
        </article>
      </div>
    </main>

    <!-- 详情页 -->
    <main v-else-if="view === 'series'" class="content">
      <div v-if="!series" class="muted">加载中…</div>
      <template v-else>
        <section class="detail">
          <img v-if="series.cover" :src="series.cover" class="detail-cover" :alt="series.series_title" />
          <div class="detail-body">
            <h2>{{ series.series_title }}</h2>
            <p class="muted small">{{ episodeCount }} 集</p>
            <p class="desc">{{ series.description }}</p>
            <div class="actions">
              <button class="btn" @click="toggleFavorite">
                {{ series.is_favorite ? '★ 已收藏' : '☆ 收藏' }}
              </button>
              <button
                v-if="series.episodes.length"
                class="btn primary"
                @click="playEpisode(series.episodes[0])"
              >
                {{ series.saved_progress ? '继续播放' : '开始播放' }}
              </button>
            </div>
            <p v-if="series.saved_progress" class="muted small">
              上次看到第 {{ series.saved_progress.episode_index }} 集 {{ series.saved_progress.position_sec }}s
            </p>
          </div>
        </section>

        <h3 class="section-title">选集</h3>
        <div class="episodes">
          <button
            v-for="ep in series.episodes"
            :key="ep.vid"
            class="episode"
            :class="{ current: currentEpisode?.vid === ep.vid }"
            @click="playEpisode(ep)"
          >
            {{ ep.episode_index }}
          </button>
        </div>
      </template>
    </main>

    <!-- 播放页 -->
    <main v-else class="content player">
      <div class="video-wrap">
        <video
          v-if="videoSrc"
          :src="videoSrc"
          class="video"
          controls
          autoplay
          playsinline
          @timeupdate="onTimeUpdate"
        />
        <div v-else class="video placeholder-video">
          <span>{{ playError || '未选择分集' }}</span>
        </div>
        <div v-if="playError && videoSrc" class="err">{{ playError }}</div>
        <div class="play-meta">
          <strong>{{ series?.series_title }}</strong>
          <span v-if="currentEpisode" class="muted">
            第 {{ currentEpisode.episode_index }} 集
          </span>
        </div>
      </div>

      <aside class="episode-side">
        <h3>选集</h3>
        <div class="episodes">
          <button
            v-for="ep in series?.episodes ?? []"
            :key="ep.vid"
            class="episode"
            :class="{ current: currentEpisode?.vid === ep.vid }"
            @click="playEpisode(ep)"
          >
            {{ ep.episode_index }}
          </button>
        </div>
      </aside>
    </main>
  </div>
</template>
