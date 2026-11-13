import { describe, it, expect, vi, beforeEach } from 'vitest';

// 用 mock 的 api 层测试 store 的登录态与档案包逻辑。
vi.mock('@/api/client', () => {
  class ApiException extends Error {
    code: string;
    status: number;
    constructor(status: number, code: string, message: string) {
      super(message);
      this.status = status;
      this.code = code;
    }
    get isLocalSession() { return this.code === 'LOCAL_SESSION'; }
    get isAccountToken() { return this.code === 'ACCOUNT_TOKEN'; }
  }
  return {
    ApiException,
    api: {
      account: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
      history: vi.fn(),
      favorites: vi.fn(),
    },
  };
});

import { account, archive, refreshAccount, login, logout, loadArchive, resetState } from './account';
import { api, ApiException } from '@/api/client';

const mockedApi = vi.mocked(api);

beforeEach(() => {
  vi.clearAllMocks();
  resetState();
});

describe('账号态', () => {
  it('刷新登录态：已登录时填充账号信息', async () => {
    mockedApi.account.mockResolvedValue({ logged_in: true, account_id: 'a1', display_name: '用户A' });
    await refreshAccount();
    expect(account.loggedIn).toBe(true);
    expect(account.accountId).toBe('a1');
    expect(account.displayName).toBe('用户A');
  });

  it('凭证过期时按未登录处理', async () => {
    mockedApi.account.mockRejectedValue(new ApiException(401, 'ACCOUNT_TOKEN', '凭证过期'));
    await refreshAccount();
    expect(account.loggedIn).toBe(false);
    expect(account.accountId).toBe('');
  });

  it('登录后刷新登录态', async () => {
    mockedApi.login.mockResolvedValue({ success: true });
    mockedApi.account.mockResolvedValue({ logged_in: true, account_id: 'a2', display_name: '用户B' });
    await login('a2', '用户B');
    expect(mockedApi.login).toHaveBeenCalledWith('a2', '用户B');
    expect(account.loggedIn).toBe(true);
  });

  it('登出后清空账号信息', async () => {
    account.loggedIn = true;
    account.accountId = 'a3';
    mockedApi.logout.mockResolvedValue({ success: true });
    await logout();
    expect(account.loggedIn).toBe(false);
    expect(account.accountId).toBe('');
  });
});

describe('本地档案包', () => {
  it('加载历史与收藏', async () => {
    mockedApi.history.mockResolvedValue({
      history: [{ series_id: 's1', episode_index: 2, position_sec: 12, updated_at: 1 }],
    });
    mockedApi.favorites.mockResolvedValue({ favorites: [{ series_id: 's1', updated_at: 1 }] });
    await loadArchive();
    expect(archive.loaded).toBe(true);
    expect(archive.history).toHaveLength(1);
    expect(archive.favorites).toHaveLength(1);
  });

  it('加载失败时记录错误且不标记已加载', async () => {
    mockedApi.history.mockRejectedValue(new Error('boom'));
    await loadArchive();
    expect(archive.loaded).toBe(false);
    expect(archive.error).toContain('boom');
  });
});
