// API 客户端：封装同源 HTTP 调用，统一处理两类 401 语义。

/** 后端错误响应结构。 */
export interface ApiError {
  code?: string;
  error: string;
}

/** 401 语义码，与后端保持一致。 */
export const ErrorCode = {
  /** 本地会话未建立：需从首页重新建立本地会话。 */
  LOCAL_SESSION: 'LOCAL_SESSION',
  /** 上游账号凭证过期：需重新登录。 */
  ACCOUNT_TOKEN: 'ACCOUNT_TOKEN',
} as const;

/** 抛出的 API 错误，携带语义码。 */
export class ApiException extends Error {
  readonly code: string;
  readonly status: number;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = 'ApiException';
    this.status = status;
    this.code = code;
  }

  get isLocalSession(): boolean {
    return this.code === ErrorCode.LOCAL_SESSION;
  }

  get isAccountToken(): boolean {
    return this.code === ErrorCode.ACCOUNT_TOKEN;
  }
}

/** 统一请求封装。 */
async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      credentials: 'same-origin',
      ...init,
      headers: {
        Accept: 'application/json',
        ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
        ...(init?.headers ?? {}),
      },
    });
  } catch (err) {
    throw new ApiException(0, 'NETWORK', String(err));
  }

  if (!res.ok) {
    let body: ApiError = { error: `请求失败（HTTP ${res.status}）` };
    try {
      body = await res.json();
    } catch {
      /* 非 JSON 错误体，保留默认提示 */
    }
    throw new ApiException(res.status, body.code ?? 'UNKNOWN', body.error);
  }

  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

// —— 类型 ——

/** 目录卡片。 */
export interface SeriesCard {
  series_id: string;
  series_title: string;
  cover: string;
  episode_count: number;
  description: string;
  tags: string[];
}

/** 目录浏览结果。 */
export interface BrowseResult {
  results: SeriesCard[];
  page: number;
  hasMore: boolean;
}

/** 搜索结果。 */
export interface SearchResult {
  results: SeriesCard[];
  page: number;
}

/** 分集简要信息。 */
export interface EpisodeBrief {
  vid: string;
  title: string;
  episode_index: number;
  position_sec: number;
  duration_sec: number;
}

/** 本地保存的进度。 */
export interface SavedProgress {
  episode_index: number;
  position_sec: number;
  duration_sec: number;
}

/** 剧集详情。 */
export interface SeriesDetail {
  series_id: string;
  series_title: string;
  cover: string;
  description: string;
  episodes: EpisodeBrief[];
  saved_progress?: SavedProgress;
  is_favorite: boolean;
}

/** 视频元信息。 */
export interface VideoMeta {
  definition: string;
  vwidth: number;
  vheight: number;
  bitrate: number;
  codec_type: string;
}

/** 单集播放信息。 */
export interface PlayInfo {
  vid: string;
  url: string;
  backup_url?: string;
  codec: string;
  encrypted: boolean;
  content_key?: string;
  video_meta?: VideoMeta;
  definition?: string;
}

/** 榜单筛选项。 */
export interface RankFilterOption {
  id: string;
  name: string;
}

/** 榜单筛选项分组。 */
export interface RankFilterGroup {
  key: string;
  name: string;
  options: RankFilterOption[];
}

/** 子榜单。 */
export interface RankSub {
  id: string;
  name: string;
  desc: string;
  panel: RankFilterGroup[];
}

/** 榜单分类。 */
export interface RankCategory {
  id: string;
  name: string;
  subs: RankSub[];
}

/** 榜单条目。 */
export interface RankItem {
  id: string;
  title: string;
  cover: string;
  episode_count: number;
  score: number;
  status: string;
  badge?: string;
  rank_text?: string;
  tags: string[];
  cast?: string[];
  intro?: string;
  sub_info?: string;
  person?: boolean;
  upcoming?: boolean;
  reserved?: boolean;
}

/** 榜单分页。 */
export interface RankPage {
  items: RankItem[];
  cursor: string;
  has_more: boolean;
}

/** 观看进度（档案包）。 */
export interface ArchiveProgress {
  series_id: string;
  series_title?: string;
  cover?: string;
  episode_index: number;
  position_sec: number;
  duration_sec?: number;
  updated_at: number;
}

/** 收藏项（档案包）。 */
export interface ArchiveFavorite {
  series_id: string;
  series_title?: string;
  cover?: string;
  updated_at: number;
}

// —— 接口 ——

/** 系统接口。 */
export const api = {
  version: () => request<{ version: string }>('/api/system/version'),

  // 账号
  account: () =>
    request<{ logged_in: boolean; account_id: string; display_name: string }>('/api/account'),
  login: (accountId: string, displayName: string) =>
    request<{ success: boolean }>('/api/account/login', {
      method: 'POST',
      body: JSON.stringify({ account_id: accountId, display_name: displayName }),
    }),
  logout: () => request<{ success: boolean }>('/api/account/logout', { method: 'POST' }),

  // 协议：目录 / 搜索 / 详情 / 取址
  catalog: (category: string, genre: string, page: number) =>
    request<BrowseResult>(
      `/api/catalog?category=${encodeURIComponent(category)}&genre=${encodeURIComponent(genre)}&page=${page}`,
    ),
  search: (keyword: string, page: number) =>
    request<SearchResult>(`/api/search?q=${encodeURIComponent(keyword)}&page=${page}`),
  series: (id: string) => request<SeriesDetail>(`/api/series/${encodeURIComponent(id)}`),
  play: (vid: string) => request<PlayInfo>(`/api/play/${encodeURIComponent(vid)}`),

  // 排行榜：分类选择器 + 榜单分页（cursor 由后端生成，原样回传）
  rankPanel: () => request<{ categories: RankCategory[] }>('/api/rank/panel'),
  rank: (selection: { rank: string; rank_sub: string; rank_panel?: string; cursor?: string }) => {
    const params = new URLSearchParams({
      rank: selection.rank,
      rank_sub: selection.rank_sub,
    });
    if (selection.rank_panel) params.set('rank_panel', selection.rank_panel);
    if (selection.cursor) params.set('cursor', selection.cursor);
    return request<RankPage>(`/api/rank?${params.toString()}`);
  },

  // 档案包
  history: () => request<{ history: ArchiveProgress[] }>('/api/archive/history'),
  favorites: () => request<{ favorites: ArchiveFavorite[] }>('/api/archive/favorites'),
  saveProgress: (p: {
    series_id: string;
    series_title?: string;
    cover?: string;
    episode_index: number;
    position_sec: number;
    duration_sec?: number;
  }) =>
    request<{ success: boolean }>('/api/archive/progress', {
      method: 'POST',
      body: JSON.stringify(p),
    }),
  toggleFavorite: (f: { series_id: string; series_title?: string; cover?: string }) =>
    request<{ favorited: boolean }>('/api/archive/favorites/toggle', {
      method: 'POST',
      body: JSON.stringify(f),
    }),
  searchHistory: () => request<{ history: string[] }>('/api/archive/search-history'),
  updateSearchHistory: (action: 'add' | 'delete' | 'clear', value: string) =>
    request<{ history: string[] }>('/api/archive/search-history', {
      method: 'POST',
      body: JSON.stringify({ action, value }),
    }),
};
