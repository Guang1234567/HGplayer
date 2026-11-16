<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue';
import {
  api,
  ApiException,
  type ArchiveFavorite,
  type ArchiveProgress,
  type EpisodeBrief,
  type PlayInfo,
  type RankCategory,
  type RankItem,
  type SeriesCard,
  type SeriesDetail,
} from './api/client';

// —— 视图 ——
type View = 'catalog' | 'search' | 'series' | 'player' | 'history' | 'favorites' | 'rank';

const view = ref<View>('catalog');
const previousView = ref<View>('catalog');

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
const searchedKeyword = ref('');

// —— 详情 ——
const series = ref<SeriesDetail | null>(null);
const seriesLoading = ref(false);

// —— 播放器 ——
const currentEpisode = ref<EpisodeBrief | null>(null);
const playInfo = ref<PlayInfo | null>(null);
const videoSrc = ref('');
const playError = ref('');
const videoEl = ref<HTMLVideoElement | null>(null);
const playing = ref(false);
const currentTime = ref(0);
const duration = ref(0);
const bufferedEnd = ref(0);
const speed = ref(1);
const volume = ref(1);
const muted = ref(false);
const descExpanded = ref(false);
const showEpisodes = ref(false);
const showSpeed = ref(false);
const controlsVisible = ref(true);

// —— 历史 / 收藏 ——
const history = ref<ArchiveProgress[]>([]);
const favorites = ref<ArchiveFavorite[]>([]);
const listLoading = ref(false);

// —— 排行榜 ——
const rankCategories = ref<RankCategory[]>([]);
const rankID = ref('');
const rankSubID = ref('');
const rankPanelID = ref('');
const rankItems = ref<RankItem[]>([]);
const rankCursor = ref('');
const rankHasMore = ref(false);
const rankBusy = ref(false);
const rankError = ref('');
const rankPanelOpen = ref(false);
let rankSeq = 0;

const rankCategory = computed(
  () => rankCategories.value.find((c) => c.id === rankID.value) ?? null,
);
const rankSubs = computed(() => rankCategory.value?.subs ?? []);
const rankSub = computed(() => rankSubs.value.find((s) => s.id === rankSubID.value) ?? null);
const rankGroups = computed(() => rankSub.value?.panel ?? []);
const rankPanelLabel = computed(() => {
  for (const group of rankGroups.value) {
    for (const option of group.options) {
      if (option.id === rankPanelID.value) return option.name;
    }
  }
  return '总榜';
});

const speeds = [1, 1.25, 1.5, 2];

const episodeCount = computed(() => series.value?.episodes.length ?? 0);

const definitionLabel = computed(() => {
  const meta = playInfo.value?.video_meta;
  if (!meta) return '';
  // 短剧多为竖屏：清晰度按短边判定，长边只是画幅。
  const shortSide = Math.min(meta.vwidth || 0, meta.vheight || 0) || meta.vheight || meta.vwidth;
  if (shortSide >= 2160) return '4K';
  if (shortSide >= 1440) return '2K';
  if (shortSide >= 1080) return '1080P';
  if (shortSide >= 720) return '720P';
  if (shortSide > 0) return `${shortSide}P`;
  return meta.definition || '';
});

const progressPercent = computed(() =>
  duration.value > 0 ? Math.min(100, (currentTime.value / duration.value) * 100) : 0,
);

const bufferPercent = computed(() =>
  duration.value > 0 ? Math.min(100, (bufferedEnd.value / duration.value) * 100) : 0,
);

const isFavorite = computed(() => series.value?.is_favorite ?? false);

// 简介是否溢出（只有需要时才给「展开」）
const descOverflows = ref(false);
const descEl = ref<HTMLElement | null>(null);

// —— 初始化 ——
onMounted(async () => {
  await Promise.all([loadVersion(), loadAccount(), loadRecentSearches(), loadCatalog(true)]);
  window.addEventListener('keydown', onKeydown);
});

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown);
});

async function loadVersion() {
  try {
    version.value = (await api.version()).version;
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
  category.value = key;
  view.value = 'catalog';
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
  searchedKeyword.value = q;
  loading.value = true;
  notice.value = '';
  try {
    searchResults.value = (await api.search(q, 1)).results;
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
  previousView.value = view.value === 'series' ? previousView.value : view.value;
  view.value = 'series';
  series.value = null;
  seriesLoading.value = true;
  notice.value = '';
  try {
    series.value = await api.series(id);
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  } finally {
    seriesLoading.value = false;
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

// —— 历史 / 收藏 ——
async function openHistory() {
  view.value = 'history';
  listLoading.value = true;
  try {
    history.value = (await api.history()).history ?? [];
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  } finally {
    listLoading.value = false;
  }
}

async function openFavorites() {
  view.value = 'favorites';
  listLoading.value = true;
  try {
    favorites.value = (await api.favorites()).favorites ?? [];
  } catch (err) {
    notice.value = err instanceof ApiException ? err.message : String(err);
  } finally {
    listLoading.value = false;
  }
}

// —— 排行榜 ——
async function openRank() {
  view.value = 'rank';
  rankError.value = '';
  if (rankCategories.value.length === 0) {
    await loadRankPanel();
  }
  ensureRankSelection();
  if (rankID.value && rankSubID.value) {
    void loadRank(true);
  }
}

async function loadRankPanel() {
  rankBusy.value = true;
  try {
    rankCategories.value = (await api.rankPanel()).categories ?? [];
    resetRankSelection();
  } catch (err) {
    rankError.value = err instanceof ApiException ? err.message : String(err);
  } finally {
    rankBusy.value = false;
  }
}

/** 选择第一个分类与其第一个子榜单。 */
function resetRankSelection() {
  const first = rankCategories.value[0];
  rankID.value = first?.id ?? '';
  rankSubID.value = first?.subs[0]?.id ?? '';
  rankPanelID.value = '';
}

/** 修正失效的选择（分类或子榜单被上游调整时）。 */
function ensureRankSelection() {
  if (!rankCategories.value.some((c) => c.id === rankID.value)) {
    resetRankSelection();
    return;
  }
  if (!rankSubs.value.some((s) => s.id === rankSubID.value)) {
    rankSubID.value = rankSubs.value[0]?.id ?? '';
    rankPanelID.value = '';
  }
}

async function loadRank(reset = false) {
  if (!rankID.value || !rankSubID.value) return;
  const seq = ++rankSeq;
  rankBusy.value = true;
  rankError.value = '';
  if (reset) {
    rankItems.value = [];
    rankCursor.value = '';
    rankHasMore.value = false;
  }
  try {
    const res = await api.rank({
      rank: rankID.value,
      rank_sub: rankSubID.value,
      rank_panel: rankPanelID.value || undefined,
      cursor: reset ? undefined : rankCursor.value || undefined,
    });
    if (seq !== rankSeq) return;
    const seen = new Set(rankItems.value.map((item) => item.id));
    rankItems.value = reset
      ? res.items
      : [...rankItems.value, ...res.items.filter((item) => !seen.has(item.id))];
    rankCursor.value = res.cursor;
    rankHasMore.value = res.has_more;
  } catch (err) {
    if (seq === rankSeq) {
      rankError.value = err instanceof ApiException ? err.message : String(err);
    }
  } finally {
    if (seq === rankSeq) rankBusy.value = false;
  }
}

function selectRankCategory(id: string) {
  if (id === rankID.value) return;
  rankID.value = id;
  ensureRankSelection();
  rankPanelOpen.value = false;
  void loadRank(true);
}

function selectRankSub(id: string) {
  if (id === rankSubID.value) return;
  rankSubID.value = id;
  rankPanelID.value = '';
  rankPanelOpen.value = false;
  void loadRank(true);
}

function selectRankPanel(id: string) {
  rankPanelID.value = rankPanelID.value === id ? '' : id;
  rankPanelOpen.value = false;
  void loadRank(true);
}

function loadMoreRank() {
  if (!rankHasMore.value || rankBusy.value) return;
  void loadRank(false);
}

function openRankItem(item: RankItem) {
  if (item.person) {
    keyword.value = item.title;
    void doSearch();
    return;
  }
  if (item.id) void openSeries(item.id);
}

// —— 播放 ——
async function playEpisode(ep: EpisodeBrief) {
  if (!series.value) return;
  previousView.value = view.value === 'player' ? previousView.value : view.value;
  view.value = 'player';
  currentEpisode.value = ep;
  playInfo.value = null;
  videoSrc.value = '';
  playError.value = '';
  descExpanded.value = false;
  showEpisodes.value = false;
  showSpeed.value = false;
  currentTime.value = 0;
  duration.value = 0;
  bufferedEnd.value = 0;
  try {
    playInfo.value = await api.play(ep.vid);
    // 加密片源由服务端解密后按 Range 供给，前端只需同源地址。
    videoSrc.value = `/media/${encodeURIComponent(ep.vid)}`;
    await nextTick();
    measureDesc();
    startAtSavedPosition(ep);
  } catch (err) {
    playError.value = err instanceof ApiException ? err.message : String(err);
  }
}

/** 测量简介是否溢出容器（决定是否显示「展开」）。 */
function measureDesc() {
  const el = descEl.value;
  if (!el) {
    descOverflows.value = false;
    return;
  }
  // max-height 钳制时 scrollHeight 反映完整文本高度，可直接比较。
  descOverflows.value = el.scrollHeight - el.clientHeight > 2;
}

/** 若本集有保存进度，则从该位置续播。 */
function startAtSavedPosition(ep: EpisodeBrief) {
  const el = videoEl.value;
  const saved = series.value?.saved_progress;
  if (!el || !saved || saved.episode_index !== ep.episode_index) return;
  if (saved.position_sec <= 0) return;
  const seek = () => {
    el.currentTime = Math.max(0, saved.position_sec - 2);
    el.removeEventListener('loadedmetadata', seek);
  };
  if (el.readyState >= 1) {
    seek();
  } else {
    el.addEventListener('loadedmetadata', seek);
  }
}

function toggleEpisodes() {
  showEpisodes.value = !showEpisodes.value;
  if (showEpisodes.value) showSpeed.value = false;
}

function toggleSpeed() {
  showSpeed.value = !showSpeed.value;
  if (showSpeed.value) showEpisodes.value = false;
}

function backFromPlayer() {
  view.value = series.value ? 'series' : previousView.value;
  videoSrc.value = '';
  playInfo.value = null;
  playing.value = false;
}

async function togglePlay() {
  const el = videoEl.value;
  if (!el) return;
  if (el.paused) {
    try {
      await el.play();
    } catch {
      /* 自动播放被拦时忽略 */
    }
  } else {
    el.pause();
  }
}

function onLoadedMetadata() {
  const el = videoEl.value;
  if (!el) return;
  duration.value = el.duration || 0;
  el.playbackRate = speed.value;
  el.volume = volume.value;
  void el.play().catch(() => {
    /* 自动播放策略可能拦截，用户点播放即可 */
  });
}

function onProgress() {
  const el = videoEl.value;
  if (!el) return;
  const ranges = el.buffered;
  if (ranges.length > 0) {
    bufferedEnd.value = ranges.end(ranges.length - 1);
  }
}

function seekTo(ratio: number) {
  const el = videoEl.value;
  if (!el || !duration.value) return;
  el.currentTime = Math.max(0, Math.min(1, ratio)) * duration.value;
}

function onProgressClick(e: MouseEvent) {
  const box = (e.currentTarget as HTMLElement).getBoundingClientRect();
  seekTo((e.clientX - box.left) / box.width);
}

/** 倍速标签：整数保留一位小数，其余按真实值显示（避免 1.25 被四舍五入成 1.3）。 */
function speedLabel(value: number): string {
  return `${Number.isInteger(value) ? value.toFixed(1) : value}x`;
}

function setSpeed(value: number) {
  speed.value = value;
  if (videoEl.value) videoEl.value.playbackRate = value;
  showSpeed.value = false;
}

function toggleMute() {
  const el = videoEl.value;
  if (!el) return;
  el.muted = !el.muted;
  muted.value = el.muted;
}

function onVolumeInput(e: Event) {
  const value = Number((e.target as HTMLInputElement).value);
  volume.value = value;
  const el = videoEl.value;
  if (el) {
    el.volume = value;
    el.muted = value === 0;
    muted.value = el.muted;
  }
}

async function toggleFullscreen() {
  if (document.fullscreenElement) {
    await document.exitFullscreen();
    return;
  }
  await document.documentElement.requestFullscreen().catch(() => {});
}

async function copyShareLink() {
  try {
    await navigator.clipboard.writeText(`${series.value?.series_title ?? ''} ${window.location.href}`);
    notice.value = '播放页地址已复制';
    window.setTimeout(() => {
      if (notice.value === '播放页地址已复制') notice.value = '';
    }, 2200);
  } catch {
    notice.value = '复制失败，请手动复制地址栏';
  }
}

function onKeydown(e: KeyboardEvent) {
  if (view.value !== 'player') return;
  if ((e.target as HTMLElement)?.tagName === 'INPUT') return;
  const el = videoEl.value;
  if (!el) return;
  if (e.code === 'Space') {
    e.preventDefault();
    void togglePlay();
    return;
  }
  if (e.code === 'ArrowRight') el.currentTime = Math.min(el.duration || 0, el.currentTime + 5);
  if (e.code === 'ArrowLeft') el.currentTime = Math.max(0, el.currentTime - 5);
  if (e.code === 'Escape') backFromPlayer();
}

let lastSaved = 0;
async function onTimeUpdate(e: Event) {
  const el = e.target as HTMLVideoElement;
  currentTime.value = el.currentTime;
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

// —— 格式化 ——
function formatTime(sec: number): string {
  if (!Number.isFinite(sec) || sec < 0) return '00:00';
  const total = Math.floor(sec);
  const m = Math.floor(total / 60);
  const s = total % 60;
  if (m >= 60) {
    const h = Math.floor(m / 60);
    return `${h}:${String(m % 60).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  }
  return `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
}

function relativeTime(ts: number): string {
  if (!ts) return '';
  const diff = Date.now() / 1000 - ts;
  if (diff < 60) return '刚刚';
  if (diff < 3600) return `${Math.floor(diff / 60)} 分钟前`;
  if (diff < 86400) return `${Math.floor(diff / 3600)} 小时前`;
  if (diff < 86400 * 30) return `${Math.floor(diff / 86400)} 天前`;
  const d = new Date(ts * 1000);
  return `${d.getMonth() + 1} 月 ${d.getDate()} 日`;
}

function initialOf(name: string): string {
  return (name || '游').trim().slice(0, 1).toUpperCase();
}

function openHistoryItem(item: ArchiveProgress) {
  void openSeries(item.series_id);
}
</script>

<template>
  <!-- 播放页：全屏沉浸，无侧栏 -->
  <div v-if="view === 'player'" class="player">
    <video
      v-if="videoSrc"
      ref="videoEl"
      :src="videoSrc"
      playsinline
      @loadedmetadata="onLoadedMetadata"
      @timeupdate="onTimeUpdate"
      @progress="onProgress"
      @play="playing = true"
      @pause="playing = false"
      @click="togglePlay"
      @mousemove="controlsVisible = true"
    />
    <div v-else class="player-fallback">
      <template v-if="playError">
        <span>{{ playError }}</span>
        <button class="pill-btn" @click="backFromPlayer">返回</button>
      </template>
      <template v-else>
        <span>正在解析片源…</span>
      </template>
    </div>

    <div class="player-top">
      <button class="back" @click="backFromPlayer">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4">
          <path d="M15 5l-7 7 7 7" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
        返回
      </button>
      <div class="player-heading">
        <div class="player-title">{{ series?.series_title ?? '' }}</div>
        <div class="player-ep">第 {{ currentEpisode?.episode_index ?? 1 }} 集</div>
      </div>
    </div>

    <div class="player-rail">
      <button class="rail-btn" :class="{ active: isFavorite }" title="收藏" @click="toggleFavorite">
        <svg width="20" height="20" viewBox="0 0 24 24" :fill="isFavorite ? 'currentColor' : 'none'" stroke="currentColor" stroke-width="1.8">
          <path d="M12 20.5l-1.6-1.45C5.4 14.4 2 11.3 2 7.6 2 4.9 4.1 2.8 6.8 2.8c1.5 0 3 .7 4 1.9 1-1.2 2.5-1.9 4-1.9 2.7 0 4.8 2.1 4.8 4.8 0 3.7-3.4 6.8-8.4 11.45z" />
        </svg>
      </button>
      <div class="rail-label">收藏</div>

      <button class="rail-btn" title="分享" @click="copyShareLink">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <path d="M4 12v7a2 2 0 002 2h12a2 2 0 002-2v-7" stroke-linecap="round" />
          <path d="M12 3v12M12 3l-4 4M12 3l4 4" stroke-linecap="round" stroke-linejoin="round" />
        </svg>
      </button>
      <div class="rail-label">分享</div>

      <button class="rail-btn" title="选集" @click="toggleEpisodes">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
          <rect x="3" y="4" width="7" height="7" rx="1.6" />
          <rect x="14" y="4" width="7" height="7" rx="1.6" />
          <rect x="3" y="14" width="7" height="7" rx="1.6" />
          <rect x="14" y="14" width="7" height="7" rx="1.6" />
        </svg>
      </button>
      <div class="rail-label">选集</div>
    </div>

    <div class="player-meta" :class="{ hide: !controlsVisible }">
      <div class="player-meta-name">@{{ series?.series_title ?? '' }}</div>
      <div class="player-meta-ep">第 {{ currentEpisode?.episode_index ?? 1 }} 集</div>
      <p ref="descEl" class="player-desc" :class="{ expanded: descExpanded }">
        {{ series?.description || '暂无简介' }}
      </p>
      <button v-if="descOverflows" class="desc-toggle" @click="descExpanded = !descExpanded">
        {{ descExpanded ? '收起' : '展开' }}
      </button>
    </div>

    <!-- 选集浮层 -->
    <div v-if="showEpisodes" class="pop" style="right: 76px; width: 380px">
      <h4>选集 · 共 {{ episodeCount }} 集</h4>
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
    </div>

    <!-- 倍速浮层 -->
    <div v-if="showSpeed" class="pop pop-menu">
      <button v-for="s in speeds" :key="s" :class="{ on: speed === s }" @click="setSpeed(s)">
        <span>{{ speedLabel(s) }}</span>
        <span v-if="speed === s">✓</span>
      </button>
    </div>

    <div class="controls" :class="{ hide: !controlsVisible }">
      <div class="progress" @click="onProgressClick">
        <div class="progress-track">
          <div class="progress-buffer" :style="{ width: bufferPercent + '%' }" />
          <div class="progress-played" :style="{ width: progressPercent + '%' }" />
        </div>
        <div class="progress-knob" :style="{ left: progressPercent + '%' }" />
      </div>

      <div class="controls-row">
        <button class="ctrl" :title="playing ? '暂停' : '播放'" @click="togglePlay">
          <svg v-if="playing" width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
            <rect x="6" y="4" width="4" height="16" rx="1.2" />
            <rect x="14" y="4" width="4" height="16" rx="1.2" />
          </svg>
          <svg v-else width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
            <path d="M7 4.5l12 7.5-12 7.5z" />
          </svg>
        </button>

        <span class="time">{{ formatTime(currentTime) }} / {{ formatTime(duration) }}</span>

        <button class="ctrl" :title="muted ? '取消静音' : '静音'" @click="toggleMute">
          <svg v-if="muted || volume === 0" width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
            <path d="M4 9v6h3l5 4V5L7 9H4z" stroke-linejoin="round" />
            <path d="M16 9l5 6M21 9l-5 6" stroke-linecap="round" />
          </svg>
          <svg v-else width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
            <path d="M4 9v6h3l5 4V5L7 9H4z" stroke-linejoin="round" />
            <path d="M16 8.5a5 5 0 010 7" stroke-linecap="round" />
          </svg>
        </button>
        <input
          class="volume-slider"
          type="range"
          min="0"
          max="1"
          step="0.05"
          :value="volume"
          style="width: 68px; accent-color: #ff4d2e"
          @input="onVolumeInput"
        />

        <div class="controls-spacer" />

        <button class="pill-btn plain" @click="toggleEpisodes">
          选集 · 全 {{ episodeCount }} 集
        </button>
        <span v-if="definitionLabel" class="pill-btn plain" style="cursor: default">
          {{ definitionLabel }}
        </span>
        <button class="pill-btn plain" @click="toggleSpeed">{{ speedLabel(speed) }}</button>
        <button class="ctrl" title="全屏" @click="toggleFullscreen">
          <svg width="17" height="17" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8">
            <path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </button>
      </div>
    </div>
  </div>

  <!-- 主界面 -->
  <div v-else class="shell">
    <aside class="sidebar">
      <div class="brand" @click="switchCategory(category)">
        <div class="brand-mark" />
        红果短剧
      </div>

      <div class="nav-group">发现</div>
      <button
        v-for="c in categories"
        :key="c.key"
        class="nav-item"
        :class="{ active: view === 'catalog' && category === c.key }"
        @click="switchCategory(c.key)"
      >
        <span class="nav-icon">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <rect x="3" y="5" width="18" height="14" rx="2.4" />
            <path d="M10 9.5l5 2.5-5 2.5z" fill="currentColor" stroke="none" />
          </svg>
        </span>
        {{ c.label }}
      </button>

      <button class="nav-item" :class="{ active: view === 'rank' }" @click="openRank">
        <span class="nav-icon">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <path d="M5 20V10M12 20V4M19 20v-7" stroke-linecap="round" />
          </svg>
        </span>
        排行榜
      </button>

      <div class="nav-group">我的</div>
      <button class="nav-item" :class="{ active: view === 'history' }" @click="openHistory">
        <span class="nav-icon">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <circle cx="12" cy="12" r="8.4" />
            <path d="M12 7.5V12l3 2" stroke-linecap="round" />
          </svg>
        </span>
        播放历史
      </button>
      <button class="nav-item" :class="{ active: view === 'favorites' }" @click="openFavorites">
        <span class="nav-icon">
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7">
            <path d="M12 20l-1.5-1.35C5.6 14.2 2.5 11.3 2.5 7.8 2.5 5.2 4.5 3.2 7.1 3.2c1.4 0 2.8.65 3.7 1.75.9-1.1 2.3-1.75 3.7-1.75 2.6 0 4.6 2 4.6 4.6 0 3.5-3.1 6.4-8 11.05z" />
          </svg>
        </span>
        我的收藏
      </button>

      <div class="side-foot">
        <div class="account-box">
          <div class="avatar">{{ initialOf(account.display_name || account.account_id) }}</div>
          <div style="min-width: 0">
            <div class="account-name">
              {{ account.logged_in ? account.display_name || account.account_id : '未登录' }}
            </div>
            <div class="account-sub">{{ account.logged_in ? '已登录' : '本地档案包可用' }}</div>
          </div>
        </div>
        <button v-if="account.logged_in" class="btn-ghost" @click="doLogout">退出登录</button>
        <button v-else class="btn-ghost" @click="showLogin = true">登录 / 注册</button>
        <div class="account-sub" style="text-align: center">
          v{{ version || '…' }}<span v-if="versionError"> · {{ versionError }}</span>
        </div>
      </div>
    </aside>

    <div class="main">
      <div class="announce">
        <span class="announce-tag">公告</span>
        <span class="announce-text">
          本客户端为开源实现，仅聚合公开短剧信息；播放数据经本地解密代理直取上游，不做任何上传。
        </span>
        <button class="icon-btn" title="关闭" @click="(notice = ''), (versionError = '')">
          <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2">
            <path d="M6 6l12 12M18 6L6 18" stroke-linecap="round" />
          </svg>
        </button>
      </div>

      <header class="page-head">
        <div class="page-head-row">
          <h1 class="page-title">
            {{
              view === 'catalog'
                ? '找剧'
                : view === 'search'
                  ? '搜索'
                  : view === 'history'
                    ? '播放历史'
                    : view === 'favorites'
                      ? '我的收藏'
                      : view === 'rank'
                        ? '排行榜'
                        : '剧集详情'
            }}
          </h1>
          <div class="search">
            <input
              v-model="keyword"
              placeholder="输入剧名 / 关键词"
              @keyup.enter="doSearch"
            />
            <button class="btn-accent" :disabled="loading" @click="doSearch">搜索</button>
          </div>
        </div>

        <div v-if="view === 'catalog'" class="filter-row">
          <span>筛选：</span>
          <div class="chip-row">
            <button
              v-for="c in categories"
              :key="c.key"
              class="chip"
              :class="{ active: category === c.key }"
              @click="switchCategory(c.key)"
            >
              {{ c.label }}
            </button>
          </div>
        </div>
      </header>

      <main class="body">
        <div v-if="notice" class="notice" style="margin: 0 0 12px">{{ notice }}</div>

        <!-- 目录 -->
        <template v-if="view === 'catalog'">
          <div v-if="loading && cards.length === 0" class="poster-grid">
            <div v-for="i in 12" :key="i" class="poster skeleton">
              <div class="poster-frame" />
              <div class="poster-title" style="height: 14px; border-radius: 4px" />
            </div>
          </div>

          <div v-else-if="cards.length === 0" class="empty">暂无内容，换个分类看看</div>

          <div v-else class="poster-grid">
            <article
              v-for="card in cards"
              :key="card.series_id"
              class="poster"
              @click="openSeries(card.series_id)"
            >
              <div class="poster-frame">
                <img v-if="card.cover" :src="card.cover" :alt="card.series_title" loading="lazy" />
                <div v-else class="poster-empty">无封面</div>
                <div class="poster-badges">
                  <span class="badge episodes">共 {{ card.episode_count }} 集</span>
                </div>
              </div>
              <div class="poster-title">{{ card.series_title }}</div>
              <div class="poster-sub">
                {{ card.tags.length ? card.tags.join(' · ') : card.description || '—' }}
              </div>
            </article>
          </div>

          <div v-if="hasMore" class="loading-more">
            <button class="btn-ghost" :disabled="loading" @click="loadMore">
              {{ loading ? '加载中…' : '加载更多' }}
            </button>
          </div>
        </template>

        <!-- 搜索 -->
        <template v-else-if="view === 'search'">
          <section v-if="recentSearches.length" style="margin-bottom: 18px">
            <div class="filter-row" style="margin: 0 0 10px">
              <span>最近搜索</span>
              <button class="chip" @click="clearRecent">清空</button>
            </div>
            <div class="chip-row">
              <button v-for="q in recentSearches" :key="q" class="chip" @click="useRecent(q)">{{ q }}</button>
            </div>
          </section>

          <div v-if="loading" class="empty">搜索中…</div>
          <div v-else-if="searchResults.length === 0" class="empty">
            「{{ searchedKeyword }}」没有找到相关剧集
          </div>

          <div v-else class="poster-grid">
            <article
              v-for="card in searchResults"
              :key="card.series_id"
              class="poster"
              @click="openSeries(card.series_id)"
            >
              <div class="poster-frame">
                <img v-if="card.cover" :src="card.cover" :alt="card.series_title" loading="lazy" />
                <div v-else class="poster-empty">无封面</div>
                <div class="poster-badges">
                  <span class="badge episodes">共 {{ card.episode_count }} 集</span>
                </div>
              </div>
              <div class="poster-title">{{ card.series_title }}</div>
              <div class="poster-sub">
                {{ card.tags.length ? card.tags.join(' · ') : card.description || '—' }}
              </div>
            </article>
          </div>
        </template>

        <!-- 详情 -->
        <template v-else-if="view === 'series'">
          <div v-if="seriesLoading" class="empty">加载中…</div>
          <div v-else-if="!series" class="empty">剧集不存在或加载失败</div>
          <template v-else>
            <section class="detail">
              <img v-if="series.cover" :src="series.cover" class="detail-cover" :alt="series.series_title" />
              <div v-else class="detail-cover poster-empty">无封面</div>
              <div class="detail-body">
                <h2 class="detail-title">{{ series.series_title }}</h2>
                <div class="detail-meta">
                  <span class="chip" style="cursor: default">共 {{ episodeCount }} 集</span>
                  <span v-if="series.saved_progress" class="chip" style="cursor: default">
                    上次看到第 {{ series.saved_progress.episode_index }} 集
                  </span>
                  <span v-if="series.is_favorite" class="chip active" style="cursor: default">已收藏</span>
                </div>
                <p class="detail-desc">{{ series.description || '暂无简介' }}</p>
                <div class="detail-actions">
                  <button
                    v-if="series.episodes.length"
                    class="btn-accent"
                    @click="
                      playEpisode(
                        series.saved_progress
                          ? (series.episodes.find(
                              (e) => e.episode_index === series!.saved_progress!.episode_index,
                            ) ?? series.episodes[0])
                          : series.episodes[0],
                      )
                    "
                  >
                    {{ series.saved_progress ? '继续播放' : '开始播放' }}
                  </button>
                  <button class="btn-ghost" @click="toggleFavorite">
                    {{ series.is_favorite ? '★ 已收藏' : '☆ 收藏' }}
                  </button>
                </div>
                <p v-if="series.saved_progress" class="progress-hint">
                  第 {{ series.saved_progress.episode_index }} 集 · 看到
                  {{ formatTime(series.saved_progress.position_sec) }}
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
        </template>

        <!-- 历史 -->
        <template v-else-if="view === 'history'">
          <div v-if="listLoading" class="empty">加载中…</div>
          <div v-else-if="history.length === 0" class="empty">还没有观看记录</div>
          <div v-else class="list">
            <button
              v-for="item in history"
              :key="item.series_id"
              class="list-row"
              @click="openHistoryItem(item)"
            >
              <img v-if="item.cover" class="list-cover" :src="item.cover" :alt="item.series_title" />
              <div v-else class="list-cover placeholder">无图</div>
              <div class="list-main">
                <div class="list-title">{{ item.series_title || item.series_id }}</div>
                <div class="list-sub">
                  第 {{ item.episode_index }} 集 · 看到 {{ formatTime(item.position_sec) }}
                </div>
              </div>
              <span class="list-time">{{ relativeTime(item.updated_at) }}</span>
            </button>
          </div>
        </template>

        <!-- 收藏 -->
        <template v-else-if="view === 'favorites'">
          <div v-if="listLoading" class="empty">加载中…</div>
          <div v-else-if="favorites.length === 0" class="empty">还没有收藏</div>
          <div v-else class="poster-grid">
            <article
              v-for="item in favorites"
              :key="item.series_id"
              class="poster"
              @click="openSeries(item.series_id)"
            >
              <div class="poster-frame">
                <img v-if="item.cover" :src="item.cover" :alt="item.series_title" loading="lazy" />
                <div v-else class="poster-empty">无封面</div>
              </div>
              <div class="poster-title">{{ item.series_title || item.series_id }}</div>
              <div class="poster-sub">{{ relativeTime(item.updated_at) }}</div>
            </article>
          </div>
        </template>

        <!-- 排行榜 -->
        <template v-else-if="view === 'rank'">
          <div v-if="rankCategories.length" class="rank-tabs">
            <button
              v-for="c in rankCategories"
              :key="c.id"
              class="chip"
              :class="{ active: c.id === rankID }"
              @click="selectRankCategory(c.id)"
            >
              {{ c.name }}
            </button>
          </div>

          <div v-if="rankSubs.length" class="rank-subs">
            <button
              v-for="s in rankSubs"
              :key="s.id"
              class="rank-sub"
              :class="{ active: s.id === rankSubID }"
              @click="selectRankSub(s.id)"
            >
              {{ s.name }}
            </button>
          </div>

          <div v-if="rankSub" class="rank-head">
            <div>
              <h2 class="rank-title">{{ rankSub.name }}</h2>
              <p v-if="rankSub.desc" class="rank-desc">{{ rankSub.desc }}</p>
            </div>
            <div v-if="rankGroups.length" class="rank-panel-wrap">
              <button class="btn-ghost" @click="rankPanelOpen = !rankPanelOpen">
                {{ rankPanelLabel }} ▾
              </button>
              <div v-if="rankPanelOpen" class="rank-panel">
                <button class="chip" :class="{ active: !rankPanelID }" @click="selectRankPanel('')">
                  总榜
                </button>
                <div v-for="g in rankGroups" :key="g.key" class="rank-group">
                  <span class="rank-group-name">{{ g.name }}</span>
                  <button
                    v-for="o in g.options"
                    :key="o.id"
                    class="chip"
                    :class="{ active: rankPanelID === o.id }"
                    @click="selectRankPanel(o.id)"
                  >
                    {{ o.name }}
                  </button>
                </div>
              </div>
            </div>
          </div>

          <div v-if="rankError" class="empty">{{ rankError }}</div>
          <div v-else-if="rankBusy && rankItems.length === 0" class="empty">加载榜单…</div>
          <div v-else-if="rankItems.length === 0" class="empty">这个榜单暂时没有内容</div>
          <div v-else class="rank-list">
            <article
              v-for="(item, idx) in rankItems"
              :key="item.id"
              class="rank-item"
              @click="openRankItem(item)"
            >
              <span class="rank-no" :class="{ top: idx < 3 }">{{ idx + 1 }}</span>
              <img
                v-if="item.cover"
                class="rank-cover"
                :class="{ round: item.person }"
                :src="item.cover"
                :alt="item.title"
                loading="lazy"
              />
              <div v-else class="rank-cover placeholder">无图</div>
              <div class="rank-main">
                <div class="rank-line">
                  <span class="rank-name">{{ item.title }}</span>
                  <span v-if="item.badge" class="badge">{{ item.badge }}</span>
                </div>
                <div class="rank-meta">
                  <span v-if="item.status">{{ item.status }}</span>
                  <span v-if="item.score > 0 && !item.person" class="rank-score">
                    {{ item.score }}分
                  </span>
                  <span v-for="t in (item.tags || []).slice(0, 3)" :key="t" class="chip mini">
                    {{ t }}
                  </span>
                </div>
                <div v-if="item.cast?.length" class="rank-subline">
                  主演: {{ item.cast.join(' / ') }}
                </div>
                <div v-if="item.intro" class="rank-intro">{{ item.intro }}</div>
                <div class="rank-value">
                  <span v-if="item.rank_text" class="rank-heat">{{ item.rank_text }}</span>
                  <span v-if="item.sub_info" class="rank-subline">{{ item.sub_info }}</span>
                </div>
              </div>
              <span v-if="item.upcoming" class="chip" style="cursor: default">待播</span>
            </article>
          </div>

          <div v-if="rankHasMore" class="loading-more">
            <button class="btn-ghost" :disabled="rankBusy" @click="loadMoreRank">
              {{ rankBusy ? '加载中…' : '加载更多' }}
            </button>
          </div>
        </template>
      </main>
    </div>

    <!-- 登录浮层 -->
    <div v-if="showLogin" class="modal-mask" @click.self="showLogin = false">
      <div class="modal">
        <h3>登录</h3>
        <p class="account-sub" style="margin: 0">本地账号只写入档案包，不会上传。</p>
        <input v-model="loginForm.id" placeholder="账号标识" />
        <input v-model="loginForm.name" placeholder="展示名称" />
        <div class="modal-actions">
          <button class="btn-accent" @click="doLogin">登录</button>
          <button class="btn-ghost" @click="showLogin = false">取消</button>
        </div>
      </div>
    </div>
  </div>
</template>