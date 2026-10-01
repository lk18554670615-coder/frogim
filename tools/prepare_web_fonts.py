"""Mirror the build engine's on-demand fonts; never enumerate fonts at runtime."""
import argparse
import concurrent.futures
import hashlib
import json
import pathlib
import re
import shutil
import time
import urllib.request


def font_sources(sdk):
    cache = pathlib.Path(sdk) / 'bin/cache'
    engine = cache / 'flutter_web_sdk/lib/_engine/engine'
    data = (engine / 'font_fallback_data.dart').read_text(encoding='utf-8')
    fonts = re.findall(r"NotoFont\(\s*'([^']+)',\s*'([^']+)'", data)
    roboto = re.findall(r"fontFallbackBaseUrl\}([^']+)'", (engine / 'canvaskit/fonts.dart').read_text(encoding='utf-8'))
    if not fonts or len(roboto) != 1:
        raise ValueError('Unsupported Flutter font metadata; review this SDK before packaging')
    paths = sorted({url for _, url in fonts} | set(roboto))
    for path in paths:
        if not re.fullmatch(r'[A-Za-z0-9_./-]+\.woff2?', path) or '..' in pathlib.PurePosixPath(path).parts:
            raise ValueError('Unsafe engine font path')
    return (cache / 'engine.stamp').read_text().strip(), paths


def validate(directory, expected_engine=None):
    directory = pathlib.Path(directory)
    index = json.loads((directory / 'font-index.json').read_text(encoding='utf-8'))
    if expected_engine and index['engineRevision'] != expected_engine:
        raise ValueError('Font mirror differs from the Flutter build engine')
    if not index['files']:
        raise ValueError('Empty font mirror')
    for path, digest in index['files'].items():
        if not re.fullmatch(r'[A-Za-z0-9_./-]+\.woff2?', path) or '..' in pathlib.PurePosixPath(path).parts:
            raise ValueError('Unsafe font mirror path')
        content = (directory / path).read_bytes()
        if content[:4] not in (b'wOF2', b'wOFF') or hashlib.sha256(content).hexdigest() != digest:
            raise ValueError('Invalid font mirror: ' + path)
    return index


def prepare(sdk, cache, proxy=None):
    revision, paths = font_sources(sdk)
    directory = pathlib.Path(cache).resolve() / revision
    directory.mkdir(parents=True, exist_ok=True)
    try:
        index = validate(directory, revision)
        if set(index['files']) == set(paths):
            return directory
    except (OSError, ValueError, KeyError):
        pass

    def download(path):
        target = directory / path
        # Completed files can be reused after an interrupted preparation.
        if target.exists() and target.read_bytes()[:4] in (b'wOF2', b'wOFF'):
            return path, hashlib.sha256(target.read_bytes()).hexdigest()
        target.parent.mkdir(parents=True, exist_ok=True)
        for attempt in range(3):
            try:
                opener = urllib.request.build_opener(urllib.request.ProxyHandler({'http': proxy, 'https': proxy})) if proxy else urllib.request.build_opener()
                with opener.open('https://fonts.gstatic.com/s/' + path, timeout=30) as response:
                    content = response.read()
                if content[:4] not in (b'wOF2', b'wOFF'):
                    raise ValueError('Not a font: ' + path)
                temporary = target.with_suffix('.partial')
                temporary.write_bytes(content)
                temporary.replace(target)
                return path, hashlib.sha256(content).hexdigest()
            except (OSError, ValueError):
                if attempt == 2:
                    raise
                time.sleep(attempt + 1)

    with concurrent.futures.ThreadPoolExecutor(max_workers=8) as workers:
        hashes = dict(workers.map(download, paths))
    (directory / 'font-index.json').write_text(json.dumps(dict(engineRevision=revision,
        source='https://fonts.gstatic.com/s/', files=dict(sorted(hashes.items()))), indent=2), encoding='utf-8')
    validate(directory, revision)
    return directory


def install(web, mirror):
    web = pathlib.Path(web).resolve()
    bootstrap = (web / 'flutter_bootstrap.js').read_text(encoding='utf-8')
    revision = re.search(r'"engineRevision"\s*:\s*"([a-f0-9]+)"', bootstrap)
    if not revision:
        raise ValueError('Cannot identify the Flutter build engine')
    index = validate(mirror, revision.group(1))
    shutil.copytree(mirror, web / 'font-fallbacks', dirs_exist_ok=True)
    manifest = web / 'assets/FontManifest.json'
    fonts = json.loads(manifest.read_text(encoding='utf-8'))
    policy = web / 'web-font-policy.json'
    background = [font for font in fonts if font.get('family') == 'NotoColorEmoji']
    if not background and policy.exists():
        background = json.loads(policy.read_text(encoding='utf-8'))['backgroundFonts']
    policy.write_text(json.dumps(dict(chineseFonts='on-demand', backgroundFonts=background)), encoding='utf-8')
    # Emoji is still registered after first frame by WebEmojiFont.
    manifest.write_text(json.dumps([font for font in fonts if font.get('family') not in
        ('NotoSansSC', 'NotoColorEmoji')], separators=(',', ':')), encoding='utf-8')
    return index


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('web', help='Flutter Web build directory')
    parser.add_argument('--flutter-sdk', required=True)
    parser.add_argument('--cache', default='build/web-font-cache')
    parser.add_argument('--proxy')
    args = parser.parse_args()
    mirror = prepare(args.flutter_sdk, args.cache, args.proxy)
    index = install(args.web, mirror)
    print(json.dumps(dict(engineRevision=index['engineRevision'], fontFiles=len(index['files']),
        bytes=sum((mirror / path).stat().st_size for path in index['files']))))
