export function adminPath(path: string, base = import.meta.env.BASE_URL): string {
  return `${base.replace(/\/$/, '')}${path}`;
}

export function pagePath(pathname: string, base = import.meta.env.BASE_URL): string {
  const basePath = base.replace(/\/$/, '');
  if (basePath && (pathname === basePath || pathname === `${basePath}/`)) return '/';
  if (basePath && pathname.startsWith(`${basePath}/`)) return pathname.slice(basePath.length);
  return pathname;
}

export function adminAsset(name: string, base = import.meta.env.BASE_URL): string {
  return `${base}${name}`;
}
