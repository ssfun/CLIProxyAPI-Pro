import { describe, expect, spyOn, test } from 'bun:test';
import { authFilesApi } from '../src/services/api/authFiles';
import { apiClient } from '../src/services/api/client';

describe('auth file list cache with identity lookups', () => {
  test('keeps targeted lookups out of the full-list cache and invalidates after refresh', async () => {
    const get = spyOn(apiClient, 'get');
    const post = spyOn(apiClient, 'post').mockResolvedValue({ access_token: 'discard-me' });
    try {
      await authFilesApi.requestManualRefresh('devin.json');
      get.mockResolvedValueOnce({ files: [{ name: 'all.json' }] });
      expect((await authFilesApi.list()).files[0].name).toBe('all.json');
      get.mockResolvedValueOnce({ files: [{ name: 'devin.json', auth_index: '2' }] });
      expect((await authFilesApi.list({ name: 'devin.json', authIndex: '2' })).files[0].name)
        .toBe('devin.json');
      expect(get).toHaveBeenLastCalledWith('/auth-files', {
        params: { name: 'devin.json', auth_index: '2' },
      });
      expect((await authFilesApi.list()).files[0].name).toBe('all.json');
      expect(get).toHaveBeenCalledTimes(2);

      expect(await authFilesApi.requestManualRefresh('devin.json', '2')).toBeUndefined();
      get.mockResolvedValueOnce({ files: [{ name: 'refreshed.json' }] });
      expect((await authFilesApi.list()).files[0].name).toBe('refreshed.json');
      expect(get).toHaveBeenCalledTimes(3);
    } finally {
      await authFilesApi.requestManualRefresh('devin.json');
      get.mockRestore();
      post.mockRestore();
    }
  });
});
