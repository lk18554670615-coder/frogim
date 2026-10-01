import gzip
import importlib.util
import pathlib
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('packager', pathlib.Path(__file__).with_name('package-web-release.py'))
packager = importlib.util.module_from_spec(spec)
spec.loader.exec_module(packager)


class PackagingTests(unittest.TestCase):
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
