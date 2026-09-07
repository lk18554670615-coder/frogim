"""Generate all blue/white brand assets from one transparent frog master.

Never regenerate manifests, storyboards or launch XML: icon updates must not
change page colors or re-enable the deliberately blank native splash.
"""
from __future__ import annotations
import argparse
import base64
import io
import json
from pathlib import Path
from PIL import Image, ImageChops, ImageDraw

BRAND_BLUE = (25, 118, 185, 255)
WHITE = (255, 255, 255, 255)
RESAMPLING = Image.Resampling.LANCZOS
ADAPTIVE_OCCUPANCY = .48  # Entire asymmetric tail fits in the 66/108 safe circle.


def recolor_green_master(image: Image.Image) -> Image.Image:
    """One-time palette migration; keep the original contour and facial geometry."""
    rgba = image.convert("RGBA")
    pixels = []
    for red, green, blue, alpha in rgba.getdata():
        color = WHITE if green > max(red, blue) else BRAND_BLUE
        # Remove near-opaque export noise, but retain all boundary alpha values.
        pixels.append((*color[:3], 255 if alpha >= 250 else alpha))
    result = Image.new("RGBA", rgba.size)
    result.putdata(pixels)
    return result


def trim_alpha(image: Image.Image) -> Image.Image:
    rgba = image.convert("RGBA")
    visible = rgba.getchannel("A").point(lambda v: 255 if v > 8 else 0)
    bounds = visible.getbbox()
    if bounds is None:
        raise ValueError("The source image has no visible pixels")
    return rgba.crop(bounds)


def validate_master(image: Image.Image) -> None:
    rgba = image.convert("RGBA")
    if rgba.getchannel("A").getextrema() != (0, 255):
        raise ValueError("Master must have real transparency and opaque details")
    colors = {p[:3] for p in rgba.getdata() if p[3] > 8}
    if colors != {WHITE[:3], BRAND_BLUE[:3]}:
        raise ValueError("Master must contain only #FFFFFF and #1976B9")


def transparent_square(mark: Image.Image, size: int, occupancy: float) -> Image.Image:
    canvas = Image.new("RGBA", (size, size))
    target = int(round(size * occupancy))
    scale = min(target / mark.width, target / mark.height)
    resized = mark.resize(
        (max(1, round(mark.width * scale)), max(1, round(mark.height * scale))),
        RESAMPLING,
    )
    canvas.alpha_composite(resized, ((size - resized.width) // 2, (size - resized.height) // 2))
    return canvas


def tile(mark: Image.Image, size: int, occupancy: float, *, rounded: bool = False) -> Image.Image:
    canvas = Image.new("RGBA", (size, size), BRAND_BLUE)
    canvas.alpha_composite(transparent_square(mark, size, occupancy))
    if rounded:
        mask = Image.new("L", (size, size))
        ImageDraw.Draw(mask).rounded_rectangle((0, 0, size - 1, size - 1), radius=size * .22, fill=255)
        canvas.putalpha(mask)
        return canvas
    return canvas.convert("RGB")


def monochrome(mark: Image.Image) -> Image.Image:
    # Android tints alpha, not RGB: punch eyes/nose/smile out of the mask.
    body = mark.getchannel("R").point(lambda v: 255 if v == 255 else 0)
    result = Image.new("RGBA", mark.size, WHITE)
    result.putalpha(ImageChops.multiply(body, mark.getchannel("A")))
    return result


def platform_icons(mark: Image.Image, root: Path) -> dict[Path, Image.Image]:
    brand = root / "assets" / "brand"
    mask = monochrome(mark)
    # Android crops the 108dp layer to a 72dp viewport; the complete frog must
    # fit the 66dp safe circle, not merely a 66dp square. Never use a badge mask.
    result = {
        brand / "qingwaguagua-mark-transparent.png": transparent_square(mark, 1024, .88),
        brand / "qingwaguagua-badge.png": tile(mark, 1024, .84, rounded=True),
        brand / "qingwaguagua-icon.png": tile(mark, 1024, .84),
        # Chat avatars are circular: retain the tail inside that clipping mask.
        brand / "qingwaguagua-avatar.png": tile(mark, 1024, .76),
        brand / "qingwaguagua-adaptive-foreground.png": transparent_square(mark, 1024, ADAPTIVE_OCCUPANCY),
        brand / "qingwaguagua-adaptive-monochrome.png": transparent_square(mask, 1024, ADAPTIVE_OCCUPANCY),
    }
    android = root / "android" / "app" / "src" / "main" / "res"
    for density, scale in [("mdpi", 1), ("hdpi", 1.5), ("xhdpi", 2), ("xxhdpi", 3), ("xxxhdpi", 4)]:
        drawable = android / f"drawable-{density}"
        result[android / f"mipmap-{density}" / "ic_launcher.png"] = tile(mark, round(48 * scale), .84)
        for filename, layer in [("ic_launcher_foreground.png", mark), ("ic_launcher_monochrome.png", mask)]:
            result[drawable / filename] = transparent_square(layer, round(108 * scale), ADAPTIVE_OCCUPANCY)
        # Legacy assets stay unreferenced by the native splash XML.
        result[drawable / "splash_logo.png"] = tile(mark, round(132 * scale), .84, rounded=True)
        result[drawable / "splash_logo_android12.png"] = transparent_square(mark, round(132 * scale), .54)

    for platform in ["ios", "macos"]:
        appicon = root / platform / "Runner" / "Assets.xcassets" / "AppIcon.appiconset"
        catalog = json.loads((appicon / "Contents.json").read_text(encoding="utf-8"))
        for item in catalog["images"]:
            if "filename" in item:
                size = round(float(item["size"].split("x")[0]) * float(item["scale"].rstrip("x")))
                result[appicon / item["filename"]] = tile(mark, size, .84)

    ios_launch = root / "ios" / "Runner" / "Assets.xcassets" / "LaunchImage.imageset"
    for suffix, size in [("", 132), ("@2x", 264), ("@3x", 396)]:
        result[ios_launch / f"LaunchImage{suffix}.png"] = tile(mark, size, .84, rounded=True)
    for size in [192, 512]:
        result[root / "web" / "icons" / f"Icon-{size}.png"] = tile(mark, size, .84)
        result[root / "web" / "icons" / f"Icon-maskable-{size}.png"] = tile(mark, size, .62)
    result[root / "web" / "favicon.png"] = tile(mark, 32, .84, rounded=True)
    admin = root.parent / "admin" / "public"
    result[admin / "qingwaguagua-mark.png"] = result[brand / "qingwaguagua-badge.png"]
    result[admin / "favicon.png"] = tile(mark, 32, .84, rounded=True)
    return result


def png_bytes(image: Image.Image) -> bytes:
    output = io.BytesIO()
    image.save(output, format="PNG", optimize=True)
    return output.getvalue()


def write_or_check(destination: Path, data: bytes, check: bool) -> None:
    if check:
        if not destination.exists() or destination.read_bytes() != data:
            raise ValueError(f"Stale brand asset: {destination}")
        return
    destination.parent.mkdir(parents=True, exist_ok=True)
    temporary = destination.with_suffix(destination.suffix + ".tmp")
    try:
        temporary.write_bytes(data)
        temporary.replace(destination)
    finally:
        temporary.unlink(missing_ok=True)


def preview(mark: Image.Image) -> Image.Image:
    sheet = Image.new("RGB", (1000, 400), "#F2F5F8")
    draw = ImageDraw.Draw(sheet)
    for i, (label, icon) in enumerate([
        ("App icon", tile(mark, 224, .84)),
        ("Page badge", tile(mark, 224, .84, rounded=True)),
    ]):
        x = 24 + i * 250
        sheet.paste(icon, (x, 32), icon.getchannel("A") if icon.mode == "RGBA" else None)
        draw.text((x, 274), label, fill="#182533")
    for i, (label, layer) in enumerate([
        ("Adaptive circle", transparent_square(mark, 336, ADAPTIVE_OCCUPANCY).crop((56, 56, 280, 280))),
        ("Monochrome alpha", transparent_square(monochrome(mark), 336, ADAPTIVE_OCCUPANCY).crop((56, 56, 280, 280))),
    ]):
        canvas = Image.new("RGBA", (224, 224), BRAND_BLUE)
        canvas.alpha_composite(layer)
        circle = Image.new("L", (224, 224))
        ImageDraw.Draw(circle).ellipse((0, 0, 223, 223), fill=255)
        x = 524 + i * 240
        sheet.paste(canvas, (x, 32), circle)
        draw.text((x, 274), label, fill="#182533")
    x = 24
    for size in [16, 20, 24, 32, 48, 64]:
        sheet.paste(tile(mark, size, .84), (x, 306))
        draw.text((x, 380), str(size), fill="#182533")
        x += size + 28
    return sheet


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("mobile_root", type=Path)
    parser.add_argument("--recolor-green", action="store_true", help="One-time migration of the original green master")
    parser.add_argument("--check", action="store_true", help="Check derived assets without writing")
    args = parser.parse_args()
    if args.check and args.recolor_green:
        parser.error("--check and --recolor-green cannot be combined")
    root = args.mobile_root.resolve()
    with Image.open(args.source) as opened:
        source = opened.convert("RGBA")
    if args.recolor_green:
        source = recolor_green_master(source)
    validate_master(source)
    if args.recolor_green:
        write_or_check(args.source, png_bytes(source), False)
    mark = trim_alpha(source)
    assets = platform_icons(mark, root)
    for destination, asset in assets.items():
        write_or_check(destination, png_bytes(asset), args.check)
    # Keep the optional SVG favicon consistent; PNG remains the HTML entry.
    badge = assets[root / "assets" / "brand" / "qingwaguagua-badge.png"]
    embedded = base64.b64encode(png_bytes(badge.resize((64, 64), RESAMPLING))).decode("ascii")
    svg = f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 64 64"><image width="64" height="64" href="data:image/png;base64,{embedded}"/></svg>\n'
    write_or_check(root.parent / "admin" / "public" / "favicon.svg", svg.encode("utf-8"), args.check)
    if not args.check:
        write_or_check(root / "artifacts" / "brand-blue" / "icon-review.png", png_bytes(preview(mark)), False)
    print(f"{'Verified' if args.check else 'Generated'} {len(assets)} PNG assets and the SVG favicon")


if __name__ == "__main__":
    main()
