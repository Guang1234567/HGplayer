<script setup lang="ts">
import { ref, onMounted, computed } from 'vue';
import { account, archive, refreshAccount, login, logout, loadArchive } from './store/account';
import { ApiException } from './api/client';

// 版本信息
const version = ref('');
const versionError = ref('');

// 媒体播放：使用同源 /media/{id} 相对路径，验证 Range 与拖拽 seek
const mediaId = ref('sample.mp4');
const mediaUrl = computed(() => `/media/${mediaId.value}`);
const videoEl = ref<HTMLVideoElement | null>(null);
const seekInfo = ref('未请求');

// 登录表单
const loginId = ref('');
const loginName = ref('');
const loginError = ref('');
const loginBusy = ref(false);

// 401 拦截：区分本地会话与账号凭证
const sessionNotice = ref('');

onMounted(async () => {
  await Promise.all([
    loadVersion(),
    refreshAccount(),
    loadArchive(),
  ]);
});

async function loadVersion() {
  try {
    const res = await fetch('/api/system/version');
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    version.value = (await res.json()).version;
  } catch (err) {
    versionError.value = String(err);
  }
}

// 401 拦截器：统一处理两类语义
function handleAuthError(err: unknown): void {
  if (err instanceof ApiException) {
    if (err.isLocalSession) {
      sessionNotice.value = '本地会话未建立，请刷新页面重试';
    } else if (err.isAccountToken) {
      sessionNotice.value = '账号凭证已过期，请重新登录';
      account.loggedIn = false;
    }
  }
}

async function onLogin() {
  loginBusy.value = true;
  loginError.value = '';
  sessionNotice.value = '';
  try {
    await login(loginId.value.trim(), loginName.value.trim());
  } catch (err) {
    if (err instanceof ApiException) {
      handleAuthError(err);
      loginError.value = err.message;
    } else {
      loginError.value = String(err);
    }
  } finally {
    loginBusy.value = false;
  }
}

async function onLogout() {
  try {
    await logout();
  } catch (err) {
    handleAuthError(err);
  }
}

// 监听 range 请求，确认 206 语义生效
function onLoadStart() {
  seekInfo.value = '开始加载（Range 请求）';
}
function onSeeked() {
  seekInfo.value = `已 seek 至 ${videoEl.value?.currentTime.toFixed(2)}s`;
}
function onProgress() {
  if (videoEl.value && !videoEl.value.seeking) {
    seekInfo.value = `已缓冲 ${Math.round(videoEl.value.buffered.length ? videoEl.value.buffered.end(videoEl.value.buffered.length - 1) : 0)}s`;
  }
}
</script>

<template>
  <div class="page">
    <header class="topbar">
      <div class="brand">红果短剧 <span class="tag">Go / Wails</span></div>
      <div class="topbar-right">
        <div class="version">
          版本：<code>{{ version || '加载中…' }}</code>
          <span v-if="versionError" class="err">{{ versionError }}</span>
        </div>
        <div class="account">
          <template v-if="account.loggedIn">
            <span class="who">{{ account.displayName }}（{{ account.accountId }}）</span>
            <button class="btn" @click="onLogout">登出</button>
          </template>
          <template v-else>
            <span class="who muted">未登录</span>
          </template>
        </div>
      </div>
    </header>

    <div v-if="sessionNotice" class="notice">{{ sessionNotice }}</div>

    <main class="stage">
      <div class="video-wrap">
        <video
          ref="videoEl"
          :src="mediaUrl"
          class="video"
          controls
          playsinline
          preload="metadata"
          @loadstart="onLoadStart"
          @seeked="onSeeked"
          @progress="onProgress"
        />
        <div class="seek-status">{{ seekInfo }}</div>

        <section class="panel">
          <h2>登录</h2>
          <p class="hint">登录为可选增强；本地档案包不依赖账号，登录与否都可用。</p>
          <div class="row">
            <input v-model="loginId" class="input" placeholder="账号标识" />
            <input v-model="loginName" class="input" placeholder="展示名称" />
            <button class="btn primary" :disabled="loginBusy" @click="onLogin">
              {{ loginBusy ? '登录中…' : '登录' }}
            </button>
          </div>
          <p v-if="loginError" class="err">{{ loginError }}</p>
        </section>

        <section class="panel">
          <h2>本地档案包</h2>
          <p v-if="archive.error" class="err">{{ archive.error }}</p>
          <p v-else-if="!archive.loaded" class="muted">加载中…</p>
          <template v-else>
            <h3>观看历史（{{ archive.history.length }}）</h3>
            <ul class="list">
              <li v-for="item in archive.history" :key="item.series_id">
                {{ item.series_title || item.series_id }} · 第 {{ item.episode_index }} 集 · {{ item.position_sec.toFixed(0) }}s
              </li>
              <li v-if="archive.history.length === 0" class="muted">暂无</li>
            </ul>
            <h3>收藏（{{ archive.favorites.length }}）</h3>
            <ul class="list">
              <li v-for="item in archive.favorites" :key="item.series_id">
                {{ item.series_title || item.series_id }}
              </li>
              <li v-if="archive.favorites.length === 0" class="muted">暂无</li>
            </ul>
          </template>
        </section>
      </div>

      <aside class="side">
        <h2>验证项</h2>
        <ul class="checks">
          <li>同源 <code>/api/system/version</code></li>
          <li>同源 <code>/media/{id}</code> 流式</li>
          <li>Range → 206 + Content-Range</li>
          <li><code>&lt;video&gt;</code> 拖拽 seek</li>
          <li>登录 / 登出 + 缓存失效</li>
          <li>两类 401 语义分离</li>
          <li>已消除 <code>unsafe-eval</code></li>
        </ul>
      </aside>
    </main>
  </div>
</template>
