import { describe, expect, it } from 'vitest';
import { adminAsset, adminPath, pagePath } from './adminPath';

describe('enterprise admin public base', () => {
  it('keeps independent deployments at the root', () => {
    expect(adminPath('/users', '/')).toBe('/users');
    expect(pagePath('/users', '/')).toBe('/users');
    expect(adminAsset('favicon.png', '/')).toBe('/favicon.png');
  });

  it('scopes shared-ingress pages and assets under /admin/', () => {
    expect(adminPath('/users?q=1', '/admin/')).toBe('/admin/users?q=1');
    expect(pagePath('/admin/', '/admin/')).toBe('/');
    expect(pagePath('/admin/users', '/admin/')).toBe('/users');
    expect(adminAsset('favicon.png', '/admin/')).toBe('/admin/favicon.png');
  });
});
