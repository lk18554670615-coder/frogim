---
version: alpha
name: Qingwa Messaging Blue and White
description: A high-frequency messaging system with Telegram-inspired blue and white surfaces, semantic state colors and a coordinated deep blue-gray dark theme.
colors:
  primary: "#1976B9"
  signal: "#1976B9"
  signal-soft: "#E7F2FC"
  pinned-light: "#E7F2FC"
  unread: "#D92343"
  success: "#218A5A"
  warning: "#B45B00"
  error: "#C52233"
  background-light: "#F2F5F8"
  surface-light: "#FFFFFF"
  surface-raised-light: "#FFFFFF"
  label-light: "#182533"
  secondary-label-light: "#5B6B7A"
  tertiary-label-light: "#5B6B7A"
  separator-light: "#DCE3EA"
  field-light: "#E9EEF3"
  background-dark: "#0F1923"
  surface-dark: "#172533"
  surface-raised-dark: "#223445"
  pinned-dark: "#203F57"
  label-dark: "#E8F0F7"
  secondary-label-dark: "#ADBDCC"
  tertiary-label-dark: "#ADBDCC"
  separator-dark: "#304454"
  field-dark: "#223445"
  incoming-light: "#FFFFFF"
  incoming-dark: "#223445"
  outgoing-light: "#DCEEFF"
  outgoing-dark: "#244D6B"
  chat-light: "#E7EFF5"
  primary-dark: "#64B5EF"
  link-light: "#12649E"
  link-dark: "#8ACCF8"
  on-primary: "#FFFFFF"
  on-signal: "#FFFFFF"
typography:
  large-title:
    fontFamily: "SF Pro Display, PingFang SC, system-ui"
    fontSize: 32px
    fontWeight: 700
    lineHeight: 1.18
    letterSpacing: -0.015em
  title:
    fontFamily: "SF Pro Text, PingFang SC, system-ui"
    fontSize: 17px
    fontWeight: 600
    lineHeight: 1.29
    letterSpacing: 0em
  compact-title:
    fontFamily: "SF Pro Text, PingFang SC, system-ui"
    fontSize: 15px
    fontWeight: 600
    lineHeight: 1.33
    letterSpacing: 0em
  body:
    fontFamily: "SF Pro Text, PingFang SC, system-ui"
    fontSize: 17px
    fontWeight: 400
    lineHeight: 1.35
    letterSpacing: 0em
  preview:
    fontFamily: "SF Pro Text, PingFang SC, system-ui"
    fontSize: 15px
    fontWeight: 400
    lineHeight: 1.33
    letterSpacing: 0em
  metadata:
    fontFamily: "SF Pro Text, PingFang SC, system-ui"
    fontSize: 13px
    fontWeight: 400
    lineHeight: 1.38
    letterSpacing: 0em
  caption:
    fontFamily: "SF Pro Text, PingFang SC, system-ui"
    fontSize: 12px
    fontWeight: 500
    lineHeight: 1.33
    letterSpacing: 0em
rounded:
  none: 0px
  xs: 4px
  sm: 8px
  md: 12px
  lg: 16px
  bubble: 18px
  full: 9999px
spacing:
  hairline: 0.5px
  xxs: 2px
  xs: 4px
  sm: 8px
  md: 12px
  lg: 16px
  xl: 20px
  2xl: 24px
  3xl: 32px
components:
  masthead:
    backgroundColor: "{colors.surface-light}"
    textColor: "{colors.label-light}"
  app-background-light:
    backgroundColor: "{colors.background-light}"
    textColor: "{colors.label-light}"
  app-background-dark:
    backgroundColor: "{colors.background-dark}"
    textColor: "{colors.label-dark}"
  signal-action:
    backgroundColor: "{colors.signal}"
    textColor: "{colors.on-signal}"
    typography: "{typography.title}"
    rounded: "{rounded.md}"
    height: 50px
    padding: 16px
  search-light:
    backgroundColor: "{colors.surface-light}"
    textColor: "{colors.label-light}"
    rounded: "{rounded.md}"
    height: 40px
    padding: 12px
  search-dark:
    backgroundColor: "{colors.field-dark}"
    textColor: "{colors.label-dark}"
    rounded: "{rounded.md}"
    height: 40px
    padding: 12px
  notice-light:
    backgroundColor: "{colors.signal-soft}"
    textColor: "{colors.label-light}"
    rounded: "{rounded.md}"
    height: 46px
    padding: 12px
  conversation-row-light:
    backgroundColor: "{colors.surface-light}"
    textColor: "{colors.label-light}"
    height: 74px
    padding: 16px
  conversation-row-dark:
    backgroundColor: "{colors.surface-dark}"
    textColor: "{colors.label-dark}"
    height: 74px
    padding: 16px
  message-outgoing:
    backgroundColor: "{colors.outgoing-light}"
    textColor: "{colors.label-light}"
    rounded: "{rounded.bubble}"
    padding: 12px
  message-incoming-light:
    backgroundColor: "{colors.incoming-light}"
    textColor: "{colors.label-light}"
    rounded: "{rounded.bubble}"
    padding: 12px
  message-incoming-dark:
    backgroundColor: "{colors.incoming-dark}"
    textColor: "{colors.label-dark}"
    rounded: "{rounded.bubble}"
    padding: 12px
  unread-badge:
    backgroundColor: "{colors.unread}"
    textColor: "{colors.on-primary}"
    rounded: "{rounded.full}"
    height: 20px
    padding: 6px
  message-context-menu:
    backgroundColor: "{colors.surface-light}"
    textColor: "{colors.label-light}"
    rounded: "{rounded.lg}"
    actionMinHeight: 44px
    maxWidth: 360px
    padding: 12px
  chat-info-group:
    backgroundColor: "{colors.surface-light}"
    textColor: "{colors.label-light}"
    rounded: "{rounded.lg}"
    rowMinHeight: 52px
    padding: 12px
---

# 青蛙呱呱设计系统

## Overview

青蛙呱呱是一款面向高频日常使用、安静而精确的通讯产品。交互遵循 Apple 平台的清晰层级、直接操作、安全区、可预测反馈和无障碍规范，但不复制 Apple Messages 或微信；配色参考 Telegram 的蓝白层级：白色导航、冷灰背景、蓝色操作和浅蓝发送气泡。Logo 保留青蛙轮廓、表情与气泡尾巴，使用 #1976B9 蓝底、白色青蛙和同款蓝色五官，深浅模式一致，不使用纸飞机造型。

The approved message-list source of truth is the fused conversation design at `/Users/joker/.codex/generated_images/019fb7a1-f640-7b23-854a-be1108d03f14/exec-0e4f47f8-7afe-4dfc-8ffe-2f024ae018d7.png`. The approved chat-detail and interaction source is the user's own ZCOOL work `ZNDc4MjUzNjg`, captured under `artifacts/reference/zcool-chat/`. Match their composition and density while translating both into this blue-and-white token system.

## Colors

- Approved reference: https://core.telegram.org/themes. The palette is adapted for this application, not a claim of Telegram exact brand values.
- Light surfaces: canvas #F2F5F8, navigation/cards #FFFFFF, fields #E9EEF3, chat #E7EFF5, dividers #DCE3EA.
- Dark surfaces: canvas/chat #0F1923, navigation/cards #172533, fields/incoming bubbles #223445, dividers #304454.
- Actions: #1976B9 with white text; hover/pressed #14659F. Dark actions use #64B5EF with #0F1923 text; hover/pressed #4A9FD9.
- Selected fills: #E7F2FC / #203F57. Selected text and links: #12649E / #8ACCF8. Do not use button fills as navigation or bubble backgrounds.
- Outgoing bubbles: #DCEEFF / #244D6B. Incoming bubbles: #FFFFFF / #223445. Text: #182533 / #E8F0F7; metadata: #5B6B7A / #ADBDCC.
- Keep red for unread/errors/destructive actions, green for online/success/answering calls, and orange for warnings. Initials avatars use blue; uploaded avatars stay unchanged. The frog brand uses blue/white assets from one master, including app icons, page badges and favicons. Android themed icons use a separate alpha mask with facial cutouts. Regenerating icons must not re-enable native launch logos or overwrite Web page/navigation colors.
- Normal text including links and metadata must reach 4.5:1 contrast. Interactive boundaries and focus indicators must reach 3:1; decorative dividers are quieter.
- Admin uses the light palette only: white sidebar/header, cool gray workspace, blue action buttons and pale blue selected rows.

## Typography

- Use the platform system font. iOS resolves to SF Pro and PingFang SC; Android resolves to its native system family.
- Top-level title is 32pt bold. Conversation titles are 16pt semibold, previews 14pt regular, and time/status metadata 12–13pt.
- Support Dynamic Type and 200 percent text scaling. Rows, notices, chips, and the composer grow vertically rather than clipping or shrinking text.
- Use tabular numerals for timestamps and unread counts where the platform supports them.

## Layout

- Use a 4pt base grid with 8, 12, 16, 20, 24, and 32pt steps. Phone horizontal gutter is 16pt.
- Respect top and bottom safe areas. Interactive targets are at least 44 by 44pt.
- The top surface contains title/action, search, then a compact three-way segmented filter. At normal text scale its content height targets 142–148pt excluding the safe area. The masthead has no decorative stripe; brand color is reserved for meaningful control state.
- The white conversation sheet begins with a 16pt top radius. Conversation rows are at least 74pt with 48pt circular avatars.
- The important notice is conditional, one line when possible, and 44–48pt high at normal text scale.
- The bottom bar has four stable destinations. Blue is a clear active indicator, never an unexplained dot.

### Desktop and Web workspace

- At widths from 1024px, switch to a true desktop workspace instead of stretching the phone layout. The account rail is 72px, the conversation/contact column is 304px, the chat canvas takes the remaining width, and the optional information panel is 320px from 1280px upward.
- The account rail keeps the four product destinations, unread state, avatar, connection state and settings visually stable. Blue marks the active destination; it never becomes a decorative divider.
- Conversations remain dense enough for high-frequency work: search and filters stay above the list, selected rows use a pale blue surface, and context actions are available by right click as well as touch semantics.
- The chat canvas owns the visual center. Its header, timeline and composer align to one content grid; the details panel may collapse without changing the message reading width abruptly.
- Contacts, Explore and Me use desktop master-detail or action-grid workspaces. They must not render as a narrow mobile list floating inside a large empty canvas.
- Keyboard contract: `Cmd/Ctrl+K` opens global search, `Cmd/Ctrl+F` searches the current conversation, `Enter` sends when appropriate and `Esc` closes the current transient surface.
- From 1023px downward, retain the verified mobile navigation and page stack. No horizontal scrolling is allowed at supported desktop or mobile widths.

## Elevation & Depth

- Prefer tonal layers, separators, and spatial grouping over shadows. The conversation sheet may have one extremely soft top shadow only when needed to separate it from the masthead.
- Blur is limited to system navigation, tab bars, and the chat composer, with opaque fallbacks for reduced transparency.
- Do not stack glass cards or add decorative drop shadows to rows.

## Shapes

- Search, filters, and standard controls use 12pt corners; the conversation sheet uses 16pt top corners; chat bubbles use 18pt corners.
- Avatars are circular. Groups use a real mosaic treatment; system conversations use an explicit system glyph or branded asset.
- Full pills are limited to filters, unread counters, compact status, and presence.

## Components

- **Masthead:** white/deep blue-gray background, readable themed title, one trailing `+` action. Scan, add contact, and create group live inside that menu.
- **Search:** 40pt field with magnifier, clear action, focus state, and real filtering. It must work with the keyboard and screen readers.
- **Conversation filters:** exactly `全部`, `单聊`, `群聊`; use the compact platform segmented pattern, maintain selection and scroll state, and let Dynamic Type grow the control rather than clipping labels.
- **Important notice:** conditional 44–48pt strip with icon, concise text, and a clear destination. Hide it when no actionable notice exists.
- **Conversation row:** 48pt circular avatar, 16pt semibold title, 14pt one-line preview, 12–13pt time, deeper-red unread badge, text-leading separator. A pinned row uses a cool neutral fill and an explicit `置顶` marker, with a themed blue selection fill. Long titles ellipsize before colliding with metadata.
- **Avatar:** people use properly cropped portrait assets, groups use a balanced 2x2 mosaic, and system threads use a purpose-made system icon. Initials are a fallback, not the default showcase state.
- **Chat bubble:** outgoing pale blue/deep blue with themed text; incoming uses white/deep blue-gray. Maximum width is 76 percent. Delivery, failure, retry, recall, and read state remain visible and accessible.
- **Chat navigation:** avatar/name/presence remain readable beside the back action; the single trailing ellipsis is labeled `聊天信息` and pushes a full information page. Long names truncate without displacing the action.
- **Message timeline:** show calm time separators only when the gap is meaningful. Incoming messages include a sender avatar in direct chat and sender name plus avatar in groups. Image messages use a real thumbnail and stable aspect ratio; voice messages expose duration and playback state.
- **Message action menu:** long press opens a compact, anchored context surface near the touched bubble, never a full-width bottom list. A dim dismissible barrier preserves focus; a two-line message preview establishes context. `回复`, eligible `复制`/`转发`, `收藏`, and `多选` are the first action group. Policy-gated `撤回`, local `删除本机记录`, and received-message `举报` form the second group. The surface adapts above or below the anchor at screen edges, is at most 360pt wide, scrolls before overflowing, supports 200 percent text, uses 44pt minimum targets, emits restrained haptics, and exposes a named VoiceOver route. Tapping outside closes it.
- **Composer:** expandable field with 44pt controls. Send uses the established active state only when content is sendable; otherwise the trailing control is visually quiet. The verified attachment panel contains `相册`, `拍摄`, and `文件`; emoji remains beside the field. Unverified recording and call controls are absent from production UI.
- **Chat information page:** the trailing ellipsis navigates directly to `聊天信息`, not a bottom sheet. The page starts with a responsive member/contact matrix, then grouped rows for local message search and group/contact profile. `举报会话`, contextual `加入黑名单`, and `清空本地记录` sit in the final safety/data group in red; block and clear retain explicit second confirmations, while reporting continues through its reason-and-submit flow. Group chat never shows the single-contact blacklist action.
- **Tab bar:** four destinations on white/deep blue-gray; blue selected icon/label with a pale/deep blue selected surface.
- **Dialogs and destructive actions:** use a polished platform-appropriate sheet/dialog, explicit consequence copy, focus management, and confirmation for irreversible actions.

## Do's and Don'ts

- Do preserve Apple-grade hierarchy, motion restraint, focus behavior, safe areas, haptics, and accessibility semantics.
- Do use real assets or the closest matching icon-library glyph; keep icon optical weights consistent.
- Do verify light mode, dark mode, reduced motion, increased contrast, VoiceOver/TalkBack, long Chinese group names, and 200 percent text.
- Do keep the content list quiet and reserve blue emphasis for meaningful controls.
- Don't copy copyrighted screens, logos, illustrations, avatars, or source files from the ZCOOL reference.
- Don't add extra top actions beside the single `+` menu.
- Don't use thick decorative rails, ambiguous decorative dots, wide shadows, giant slogans, or generic gradient cards.
- Don't silently substitute demo data after authentication, network, or protocol failures.
- Don't introduce a new color, spacing, radius, or icon family without updating this document.
