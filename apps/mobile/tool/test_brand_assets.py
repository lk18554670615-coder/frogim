"""Run with: python -m unittest discover -s apps/mobile/tool -p test_brand_assets.py"""
import json
import math
import unittest
from pathlib import Path
from PIL import Image
from generate_brand_assets import (
    BRAND_BLUE, WHITE, ADAPTIVE_OCCUPANCY, monochrome, platform_icons,
    recolor_green_master, trim_alpha, validate_master, png_bytes,
)

ROOT = Path(__file__).resolve().parents[1]


class BrandAssetsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        with Image.open(ROOT / "assets/brand/qingwaguagua-mark-flat-source.png") as source:
            cls.source = source.convert("RGBA")
        cls.mark = trim_alpha(cls.source)

    def test_exact_palette_and_real_transparency(self):
        validate_master(self.source)
        self.assertEqual(self.source.getpixel((0, 0))[3], 0)

    def test_one_time_recolor_preserves_contour_and_details(self):
        original = Image.new("RGBA", (5, 1))
        original.putdata([(45, 221, 107, 255), (4, 20, 42, 255), (45, 221, 107, 81), (0, 0, 0, 0), (45, 221, 107, 254)])
        result = recolor_green_master(original)
        self.assertEqual(result.size, original.size)
        self.assertEqual(list(result.getdata()), [WHITE, BRAND_BLUE, (*WHITE[:3], 81), (*BRAND_BLUE[:3], 0), WHITE])

    def test_monochrome_punches_out_facial_details(self):
        alpha = monochrome(self.mark).getchannel("A")
        face_count = body_count = 0
        for pixel, mask_alpha in zip(self.mark.getdata(), alpha.getdata()):
            if pixel[3] == 255 and pixel[:3] == BRAND_BLUE[:3]:
                self.assertEqual(mask_alpha, 0)
                face_count += 1
            if pixel[3] == 255 and pixel[:3] == WHITE[:3]:
                self.assertEqual(mask_alpha, 255)
                body_count += 1
        self.assertGreater(face_count, 1000)
        self.assertGreater(body_count, 10000)

    def test_android_adaptive_tail_inside_safe_circle(self):
        self.assertLess(ADAPTIVE_OCCUPANCY, .5)
        image = Image.open(ROOT / "assets/brand/qingwaguagua-adaptive-foreground.png")
        for y in range(image.height):
            for x in range(image.width):
                if image.getpixel((x, y))[3] > 8:
                    radius = math.hypot(x + .5 - image.width / 2, y + .5 - image.height / 2)
                    self.assertLessEqual(radius, image.width * 66 / 108 / 2)

    def test_apple_icon_catalogs_have_correct_sizes_and_no_alpha(self):
        for platform in ["ios", "macos"]:
            catalog = ROOT / platform / "Runner/Assets.xcassets/AppIcon.appiconset"
            for entry in json.loads((catalog / "Contents.json").read_text())["images"]:
                if "filename" not in entry:
                    continue
                with Image.open(catalog / entry["filename"]) as image:
                    expected = round(float(entry["size"].split("x")[0]) * float(entry["scale"].rstrip("x")))
                    self.assertEqual(image.size, (expected, expected))
                    self.assertEqual(image.mode, "RGB")
                    self.assertEqual(image.getpixel((0, 0)), BRAND_BLUE[:3])

    def test_pwa_maskable_mark_inside_safe_circle(self):
        image = Image.open(ROOT / "web/icons/Icon-maskable-192.png")
        for y in range(image.height):
            for x in range(image.width):
                if image.getpixel((x, y))[0] > 60:
                    self.assertLessEqual(math.hypot(x + .5 - 96, y + .5 - 96), 192 * .4)

    def test_default_avatar_tail_inside_circular_clip(self):
        image = Image.open(ROOT / "assets/brand/qingwaguagua-avatar.png")
        for y in range(image.height):
            for x in range(image.width):
                if image.getpixel((x, y))[0] > 60:
                    self.assertLessEqual(math.hypot(x + .5 - 512, y + .5 - 512), 512)

    def test_admin_and_flutter_share_the_same_badge(self):
        badge = (ROOT / "assets/brand/qingwaguagua-badge.png").read_bytes()
        self.assertEqual(badge, (ROOT.parent / "admin/public/qingwaguagua-mark.png").read_bytes())
        image = Image.open(ROOT / "assets/brand/qingwaguagua-badge.png")
        self.assertEqual(image.getpixel((0, 0))[3], 0)
        self.assertEqual(image.getpixel((512, 0)), BRAND_BLUE)

    def test_all_platform_assets_are_reproducible(self):
        for path, image in platform_icons(self.mark, ROOT).items():
            with self.subTest(path=path):
                self.assertEqual(path.read_bytes(), png_bytes(image))

    def test_master_must_not_accept_a_baked_checkerboard(self):
        with self.assertRaises(ValueError):
            validate_master(Image.new("RGB", (10, 10), "white"))


if __name__ == "__main__":
    unittest.main()
