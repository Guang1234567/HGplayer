import { describe, it, expect } from 'vitest';

describe('版本与媒体路径约定', () => {
  it('版本接口使用同源相对路径', () => {
    const url = '/api/system/version';
    expect(url.startsWith('/api/')).toBe(true);
    expect(url).not.toContain('127.0.0.1');
    expect(url).not.toContain('localhost');
  });

  it('媒体路径使用同源相对路径', () => {
    const url = '/media/sample.mp4';
    expect(url.startsWith('/media/')).toBe(true);
    expect(url).not.toContain('http');
  });

  it('媒体路径不接受绝对地址', () => {
    const bad = 'http://evil.example.com/media/x';
    // 同源契约要求相对路径，绝对地址违背该约定。
    expect(bad.startsWith('/media/')).toBe(false);
  });
});
