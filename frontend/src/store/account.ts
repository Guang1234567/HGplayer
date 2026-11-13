// 账号态与本地档案包的状态管理。
import { reactive } from 'vue';
import { api, ApiException, type ArchiveProgress, type ArchiveFavorite } from '@/api/client';

interface AccountState {
  loggedIn: boolean;
  accountId: string;
  displayName: string;
}

interface ArchiveState {
  history: ArchiveProgress[];
  favorites: ArchiveFavorite[];
  loaded: boolean;
  error: string;
}

/** 账号态状态（内部可变，供 store 与测试使用）。 */
export const accountState = reactive<AccountState>({
  loggedIn: false,
  accountId: '',
  displayName: '',
});

/** 本地档案包状态（内部可变，供 store 与测试使用）。 */
export const archiveState = reactive<ArchiveState>({
  history: [],
  favorites: [],
  loaded: false,
  error: '',
});

/** 账号态（只读视图，供组件使用）。 */
export const account = accountState;

/** 本地档案包状态（只读视图，供组件使用）。 */
export const archive = archiveState;

/** 重置状态，用于测试。 */
export function resetState(): void {
  accountState.loggedIn = false;
  accountState.accountId = '';
  accountState.displayName = '';
  archiveState.history = [];
  archiveState.favorites = [];
  archiveState.loaded = false;
  archiveState.error = '';
}

/** 刷新登录态。 */
export async function refreshAccount(): Promise<void> {
  try {
    const res = await api.account();
    accountState.loggedIn = res.logged_in;
    accountState.accountId = res.account_id ?? '';
    accountState.displayName = res.display_name ?? '';
  } catch (err) {
    accountState.loggedIn = false;
    accountState.accountId = '';
    accountState.displayName = '';
    if (err instanceof ApiException && err.isAccountToken) {
      // 凭证过期：按未登录处理，避免沿用失效身份。
      accountState.loggedIn = false;
    }
  }
}

/** 登录。 */
export async function login(accountId: string, displayName: string): Promise<void> {
  await api.login(accountId, displayName);
  await refreshAccount();
}

/** 登出。 */
export async function logout(): Promise<void> {
  await api.logout();
  accountState.loggedIn = false;
  accountState.accountId = '';
  accountState.displayName = '';
}

/** 加载本地档案包。 */
export async function loadArchive(): Promise<void> {
  try {
    const [h, f] = await Promise.all([api.history(), api.favorites()]);
    archiveState.history = h.history ?? [];
    archiveState.favorites = f.favorites ?? [];
    archiveState.error = '';
    archiveState.loaded = true;
  } catch (err) {
    archiveState.error = String(err);
    archiveState.loaded = false;
  }
}
