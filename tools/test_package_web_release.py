import gzip
import importlib.util
import json
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('packager', pathlib.Path(__file__).with_name('package-web-release.py'))
packager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packager)


class PackagingTests(unittest.TestCase):
    def test_emoji_is_removed_only_from_web_startup_manifest(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            source = root / 'input'
            (source / 'assets/assets/fonts').mkdir(parents=True)
            for name in ['index.html', 'flutter_bootstrap.js', 'app_startup.js', 'main.dart.js']:
                (source / name).write_text('<head></head>')
            fonts = [
                dict(family='NotoSansSC', fonts=[dict(asset='assets/fonts/NotoSansSC-Regular.otf')]),
                dict(family='NotoColorEmoji', fonts=[dict(asset='assets/fonts/NotoColorEmoji.ttf')]),
            ]
            original = json.dumps(fonts)
            (source / 'assets/FontManifest.json').write_text(original)
            for font in fonts:
                (source / 'assets' / font['fonts'][0]['asset']).write_bytes(b'font' * 1000)
            manifest = packager.package(source, root / 'output')
            runtime = root / 'output' / manifest['runtimePath']
            eager_fonts = json.loads((runtime / 'assets/FontManifest.json').read_text())
            self.assertEqual([f['family'] for f in eager_fonts], ['NotoSansSC'])
            self.assertEqual(manifest['deferredFonts'], [fonts[1]])
            self.assertEqual(gzip.decompress((runtime / 'assets/assets/fonts/NotoColorEmoji.ttf.gz').read_bytes()), b'font' * 1000)
            self.assertEqual((source / 'assets/FontManifest.json').read_text(), original)
            self.assertNotIn('NotoColorEmoji', (root / 'output/index.html').read_text())

    def test_release_version_is_checked_before_packaging(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            source = root / 'input'
            (source / 'assets').mkdir(parents=True)
            for name in ['index.html', 'flutter_bootstrap.js', 'app_startup.js', 'main.dart.js', 'assets/FontManifest.json']:
                (source / name).write_text('<head></head>' if name == 'index.html' else '[]')
            (source / 'version.json').write_text('{"version":"1.0.12","build_number":"4016"}')
            with self.assertRaises(ValueError):
                packager.package(source, root / 'wrong', '1.0.16', '8023')
            self.assertFalse((root / 'wrong').exists())
            (source / 'version.json').write_text('{"version":"1.0.16","build_number":"8023"}')
            manifest = packager.package(source, root / 'correct', '1.0.16', '8023')
            self.assertEqual(manifest['version'], '1.0.16')
            self.assertEqual(manifest['buildNumber'], '8023')

    def test_release_urls_change_with_content_and_gzip_matches_original(self):
        with tempfile.TemporaryDirectory() as folder:
            root = pathlib.Path(folder)
            source = root / 'input'
            (source / 'assets/assets/fonts').mkdir(parents=True)
            files = {'index.html': '<head></head><script src="wukongimjssdk-1.3.5.umd.js"></script>',
                'flutter_bootstrap.js': 'bootstrap', 'app_startup.js': 'startup',
                'main.dart.js': 'initial-runtime' * 1000, 'assets/FontManifest.json': '[]',
                'assets/assets/fonts/NotoSansSC-Regular.otf': 'font' * 1000,
                'linli_push_worker.js': 'push', 'flutter_service_worker.js': 'old-cache'}
            for name, content in files.items(): (source / name).write_text(content)
            first = packager.package(source, root / 'one')
            second = packager.package(source, root / 'two')
            self.assertEqual(first['releaseId'], second['releaseId'])
            runtime = root / 'one' / first['runtimePath']
            self.assertEqual(gzip.decompress((runtime / 'main.dart.js.gz').read_bytes()), (runtime / 'main.dart.js').read_bytes())
            self.assertIn(first['runtimePath'] + '/main.dart.js', (root / 'one/index.html').read_text())
            self.assertFalse((runtime / 'linli_push_worker.js').exists())
            self.assertEqual((root / 'one/linli_push_worker.js').read_text(), 'push')
            (source / 'main.dart.js').write_text('updated-runtime')
            third = packager.package(source, root / 'three')
            self.assertNotEqual(first['releaseId'], third['releaseId'])
            with self.assertRaises(ValueError): packager.package(source, source)
            with self.assertRaises(ValueError): packager.package(source, root / 'one')


if __name__ == '__main__': unittest.main()
