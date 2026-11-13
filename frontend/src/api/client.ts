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

  /** 是否为本地会话未建立。 */
  get isLocalSession(): boolean {
    return this.code === ErrorCode.LOCAL_SESSION;
  }

  /** 是否为账号凭证过期。 */
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
      // 非 JSON 错误体，保留默认提示。
    }
    throw new ApiException(res.status, body.code ?? 'UNKNOWN', body.error);
  }

  if (res.status === 204) {
    return undefined as T;
  }
  return (await res.json()) as T;
}

/** 系统接口。 */
export const api = {
  version: () => request<{ version: string }>('/api/system/version'),

  /** 读取当前登录态。 */
  account: () => request<{ logged_in: boolean; account_id: string; display_name: string }>('/api/account'),

  /** 登录。 */
  login: (accountId: string, displayName: string) =>
    request<{ success: boolean }>('/api/account/login', {
      method: 'POST',
      body: JSON.stringify({ account_id: accountId, display_name: displayName }),
    }),

  /** 登出。 */
  logout: () => request<{ success: boolean }>('/api/account/logout', { method: 'POST' }),

  /** 本地档案包：观看历史。 */
  history: () => request<{ history: ArchiveProgress[] }>('/api/archive/history'),

  /** 本地档案包：收藏。 */
  favorites: () => request<{ favorites: ArchiveFavorite[] }>('/api/archive/favorites'),
};

/** 观看进度。 */
export interface ArchiveProgress {
  series_id: string;
  series_title?: string;
  cover?: string;
  episode_index: number;
  position_sec: number;
  duration_sec?: number;
  updated_at: number;
}

/** 收藏项。 */
export interface ArchiveFavorite {
  series_id: string;
  series_title?: string;
  cover?: string;
  updated_at: number;
}
