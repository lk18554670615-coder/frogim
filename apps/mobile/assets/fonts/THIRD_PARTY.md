# Noto Sans SC

- Upstream: `notofonts/noto-cjk`
- Version/tag: `Sans2.004`
- File: `Sans/SubsetOTF/SC/NotoSansSC-Regular.otf`
- Source: <https://github.com/notofonts/noto-cjk/blob/Sans2.004/Sans/SubsetOTF/SC/NotoSansSC-Regular.otf>
- SHA-256: `faa6c9df652116dde789d351359f3d7e5d2285a2b2a1f04a2d7244df706d5ea9`
- License: SIL Open Font License 1.1; the unmodified upstream license is stored in `OFL.txt`.

The font is bundled as the deterministic Flutter Web Chinese fallback. Native
platforms keep their existing system typography; the asset is not selected as
their primary family.

# Noto Color Emoji

- Upstream: `googlefonts/noto-emoji`
- Commit: `8998f5dd683424a73e2314a8c1f1e359c19e8742`
- File: `fonts/NotoColorEmoji.ttf`
- Source: <https://github.com/googlefonts/noto-emoji/blob/8998f5dd683424a73e2314a8c1f1e359c19e8742/fonts/NotoColorEmoji.ttf>
- SHA-256: `72a635cb3d2f3524c51620cdde406b217204e8a6a06c6a096ff8ed4b5fd6e27b`
- License: SIL Open Font License 1.1; the unmodified upstream license is stored in `NotoColorEmoji-OFL.txt`.

The unmodified font is registered as a Web fallback so CanvasKit can render
user messages and structured live interactions without dropping emoji glyphs.
