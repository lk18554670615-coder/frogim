import hashlib
import json
import pathlib
import tempfile
import unittest

from prepare_web_fonts import font_sources, install, validate


class WebFontsTests(unittest.TestCase):
    def fixture(self, root):
        mirror = root / 'mirror'
        (mirror / 'notosanssc/v1').mkdir(parents=True)
        content = b'wOF2font-fixture'
        path = 'notosanssc/v1/one.woff2'
        (mirror / path).write_bytes(content)
        (mirror / 'font-index.json').write_text(json.dumps(dict(engineRevision='abcdef',
            files={path: hashlib.sha256(content).hexdigest()})))
        web = root / 'web'
        (web / 'assets').mkdir(parents=True)
        (web / 'flutter_bootstrap.js').write_text('{"engineRevision":"abcdef"}')
        fonts = [dict(family=name, fonts=[]) for name in ('NotoSansSC', 'NotoColorEmoji', 'MaterialIcons')]
        (web / 'assets/FontManifest.json').write_text(json.dumps(fonts))
        return web, mirror

    def test_only_web_startup_fonts_change_and_install_is_repeatable(self):
        with tempfile.TemporaryDirectory() as folder:
            web, mirror = self.fixture(pathlib.Path(folder))
            before = (mirror / 'font-index.json').read_bytes()
            install(web, mirror)
            install(web, mirror)
            self.assertEqual([f['family'] for f in json.loads((web / 'assets/FontManifest.json').read_text())], ['MaterialIcons'])
            self.assertEqual(json.loads((web / 'web-font-policy.json').read_text())['backgroundFonts'][0]['family'], 'NotoColorEmoji')
            self.assertEqual((web / 'font-fallbacks/font-index.json').read_bytes(), before)
            self.assertEqual((mirror / 'font-index.json').read_bytes(), before)

    def test_wrong_engine_or_corrupt_shard_prevents_install(self):
        with tempfile.TemporaryDirectory() as folder:
            web, mirror = self.fixture(pathlib.Path(folder))
            original = (web / 'assets/FontManifest.json').read_bytes()
            with self.assertRaises(ValueError): validate(mirror, 'different')
            (mirror / 'notosanssc/v1/one.woff2').write_bytes(b'bad')
            with self.assertRaises(ValueError): install(web, mirror)
            self.assertEqual((web / 'assets/FontManifest.json').read_bytes(), original)
            self.assertFalse((web / 'font-fallbacks').exists())

    def test_sdk_drives_shard_names_and_default_latin_font(self):
        with tempfile.TemporaryDirectory() as folder:
            sdk = pathlib.Path(folder)
            engine = sdk / 'bin/cache/flutter_web_sdk/lib/_engine/engine'
            (engine / 'canvaskit').mkdir(parents=True)
            (sdk / 'bin/cache/engine.stamp').write_text('abcdef')
            (engine / 'font_fallback_data.dart').write_text("NotoFont('Noto Sans SC 0', 'notosanssc/v1/one.woff2'),")
            (engine / 'canvaskit/fonts.dart').write_text("'${configuration.fontFallbackBaseUrl}roboto/v1/latin.woff2'")
            revision, paths = font_sources(sdk)
            self.assertEqual(revision, 'abcdef')
            self.assertEqual(paths, ['notosanssc/v1/one.woff2', 'roboto/v1/latin.woff2'])


if __name__ == '__main__': unittest.main()
