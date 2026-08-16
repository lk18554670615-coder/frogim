"""Generate deterministic launcher and splash assets from the transparent brand mark."""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image


NAVY = (18, 59, 50, 255)
RESAMPLING = Image.Resampling.LANCZOS


def trim_alpha(image: Image.Image) -> Image.Image:
    rgba = image.convert("RGBA")
    alpha = rgba.getchannel("A")
    visible = alpha.point(lambda value: 255 if value > 8 else 0)
    bounds = visible.getbbox()
    if bounds is None:
        raise ValueError("The source image has no visible pixels")
    return rgba.crop(bounds)


def transparent_square(mark: Image.Image, size: int, occupancy: float) -> Image.Image:
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    target = int(round(size * occupancy))
    scale = min(target / mark.width, target / mark.height)
    resized = mark.resize(
        (max(1, round(mark.width * scale)), max(1, round(mark.height * scale))),
        RESAMPLING,
    )
    position = ((size - resized.width) // 2, (size - resized.height) // 2)
    canvas.alpha_composite(resized, position)
    return canvas


def save_png(image: Image.Image, destination: Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    image.save(destination, format="PNG", optimize=True)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("source", type=Path)
    parser.add_argument("mobile_root", type=Path)
    args = parser.parse_args()

    root = args.mobile_root.resolve()
    mark = trim_alpha(Image.open(args.source))

    brand_dir = root / "assets" / "brand"
    master = transparent_square(mark, 1024, 0.88)
    # Keep the full frog and speech-bubble tail inside Android's 66% adaptive
    # icon safe zone. The platform scales this layer behind a circle/squircle
    # mask, so a larger foreground crops the eyes on Pixel launchers.
    adaptive = transparent_square(mark, 1024, 0.62)
    save_png(master, brand_dir / "qingwaguagua-mark-transparent.png")
    save_png(adaptive, brand_dir / "qingwaguagua-adaptive-foreground.png")

    legacy_icon = Image.new("RGBA", (1024, 1024), NAVY)
    legacy_icon.alpha_composite(transparent_square(mark, 1024, 0.84))
    save_png(legacy_icon.convert("RGB"), brand_dir / "qingwaguagua-icon.png")

    android_res = root / "android" / "app" / "src" / "main" / "res"
    densities = {
        "drawable-mdpi": 132,
        "drawable-hdpi": 198,
        "drawable-xhdpi": 264,
        "drawable-xxhdpi": 396,
        "drawable-xxxhdpi": 528,
    }
    for folder, size in densities.items():
        save_png(
            transparent_square(mark, size, 0.92),
            android_res / folder / "splash_logo.png",
        )
        # Android 12+ applies its own splash-icon mask. Keep a dedicated,
        # more generously padded source so the eyes and speech-bubble tail are
        # not clipped by that platform mask.
        save_png(
            transparent_square(mark, size, 0.54),
            android_res / folder / "splash_logo_android12.png",
        )

    ios_launch = root / "ios" / "Runner" / "Assets.xcassets" / "LaunchImage.imageset"
    for filename, size in (
        ("LaunchImage.png", 132),
        ("LaunchImage@2x.png", 264),
        ("LaunchImage@3x.png", 396),
    ):
        save_png(transparent_square(mark, size, 0.92), ios_launch / filename)

    audit_dir = root / "artifacts" / "design-audit" / "current-run"
    icon_preview = Image.new("RGBA", (1024, 1024), NAVY)
    icon_preview.alpha_composite(adaptive)
    save_png(icon_preview.convert("RGB"), audit_dir / "07-adaptive-icon-preview.png")

    splash_preview = Image.new("RGBA", (1080, 2400), (255, 255, 255, 255))
    splash_mark = transparent_square(mark, 396, 0.92)
    splash_preview.alpha_composite(splash_mark, ((1080 - 396) // 2, (2400 - 396) // 2))
    save_png(splash_preview.convert("RGB"), audit_dir / "08-splash-preview.png")


if __name__ == "__main__":
    main()
