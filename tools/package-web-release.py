"""Package a Flutter build with immutable, content-addressed runtime URLs."""
import argparse
import gzip
import hashlib
import json
import pathlib
import shutil


def package(source, output, expected_version=None, expected_build_number=None):
    source, output = pathlib.Path(source).resolve(), pathlib.Path(output).resolve()
    if output == source or output.is_relative_to(source) or source.is_relative_to(output):
        raise ValueError('source and output must be separate directories')
    if output.exists():
        raise ValueError('output already exists; use a new release directory')
    required = ['index.html', 'flutter_bootstrap.js', 'app_startup.js', 'main.dart.js', 'assets/FontManifest.json']
    if any(not (source / name).is_file() for name in required):
        raise ValueError('incomplete Flutter build')
    version_file = source / 'version.json'
    version_info = json.loads(version_file.read_text(encoding='utf-8')) if version_file.exists() else {}
    for key, expected in [('version', expected_version), ('build_number', expected_build_number)]:
        if expected is not None and version_info.get(key) != str(expected):
            raise ValueError('Flutter build ' + key + ' differs from the release version')
    files = sorted((p for p in source.rglob('*') if p.is_file()), key=lambda p: p.relative_to(source).as_posix())
    digest = hashlib.sha256()
    for path in files:
        digest.update(path.relative_to(source).as_posix().encode())
        digest.update(hashlib.sha256(path.read_bytes()).digest())
    release_id = digest.hexdigest()[:16]
    shutil.copytree(source, output)
    runtime = output / 'releases' / release_id
    shutil.copytree(source, runtime)
    html = (output / 'index.html').read_text(encoding='utf-8')
    html = html.replace('</head>', '<meta name="app-resource-base" content="releases/' + release_id + '/">\n'
        '<link rel="preload" href="releases/' + release_id + '/main.dart.js" as="script" fetchpriority="high">\n'
        '<link rel="preload" href="releases/' + release_id + '/assets/assets/fonts/NotoSansSC-Regular.otf" as="fetch" crossorigin fetchpriority="low">\n</head>')
    # Leave bootstrap/startup at revalidated URLs; their resource-base comes from
    # the current HTML. The SDK itself can use the immutable release URL.
    html = html.replace('src="wukongimjssdk-', 'src="releases/' + release_id + '/wukongimjssdk-')
    (output / 'index.html').write_text(html, encoding='utf-8')
    worker = "self.addEventListener('install',()=>self.skipWaiting());\nself.addEventListener('activate',e=>e.waitUntil(self.registration.unregister().then(()=>self.clients.claim())));\n"
    (output / 'flutter_service_worker.js').write_text(worker, encoding='utf-8')
    # Files not named by their contents remain revalidated. Versioned payloads
    # are immutable and get deterministic precompressed gzip sidecars.
    for path in [*runtime.rglob('*'), *(output / p for p in ['index.html', 'flutter_bootstrap.js', 'app_startup.js'])]:
        if path.is_file() and path.suffix in ['.js', '.json', '.wasm', '.otf', '.ttf', '.html', '.bin']:
            data = path.read_bytes()
            compressed = gzip.compress(data, compresslevel=9, mtime=0)
            if len(compressed) < len(data):
                path.with_name(path.name + '.gz').write_bytes(compressed)
    # Workers never belong in the runtime's immutable URL namespace.
    for name in ['index.html', 'flutter_service_worker.js', 'linli_push_worker.js']:
        for suffix in ['', '.gz']:
            p = runtime / (name + suffix)
            if p.exists(): p.unlink()
    manifest = dict(releaseId=release_id, runtimePath='releases/' + release_id,
        version=version_info.get('version'), buildNumber=version_info.get('build_number'),
        mainSHA256=hashlib.sha256((runtime / 'main.dart.js').read_bytes()).hexdigest(),
        runtimeTreeSHA256=hashlib.sha256('\n'.join(p.relative_to(runtime).as_posix() + ':' + hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted((p for p in runtime.rglob('*') if p.is_file()), key=lambda p: p.relative_to(runtime).as_posix())).encode()).hexdigest(),
        fontBytes=sum(p.stat().st_size for p in (runtime / 'assets/assets/fonts').glob('*') if p.suffix in ['.otf', '.ttf']))
    (output / 'web-release.json').write_text(json.dumps(manifest, indent=2), encoding='utf-8')
    return manifest


if __name__ == '__main__':
    parser = argparse.ArgumentParser()
    parser.add_argument('source')
    parser.add_argument('output')
    parser.add_argument('--expected-version')
    parser.add_argument('--expected-build-number')
    args = parser.parse_args()
    print(json.dumps(package(args.source, args.output, args.expected_version, args.expected_build_number)))
