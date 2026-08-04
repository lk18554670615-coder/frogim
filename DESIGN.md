---
version: alpha
name: Linli Messaging Midnight Signal
description: A premium, high-frequency messaging system combining Apple-grade interaction discipline with a distinctive midnight-and-signal visual identity.
colors:
  primary: "#0F172A"
  signal: "#FFC529"
  signal-soft: "#FFF8DF"
  pinned-light: "#F8FAFC"
  unread: "#D92343"
  success: "#218A5A"
  warning: "#B45B00"
  error: "#C52233"
  background-light: "#F5F7FA"
  surface-light: "#FFFFFF"
  surface-raised-light: "#FFFFFF"
  label-light: "#0F172A"
  secondary-label-light: "#64748B"
  tertiary-label-light: "#94A3B8"
  separator-light: "#E7EAF0"
  field-light: "#EEF1F5"
  background-dark: "#080D18"
  surface-dark: "#111827"
  surface-raised-dark: "#172033"
  pinned-dark: "#172033"
  label-dark: "#F8FAFC"
  secondary-label-dark: "#A8B2C2"
  tertiary-label-dark: "#748096"
  separator-dark: "#263247"
  field-dark: "#1B2639"
  incoming-light: "#EEF1F5"
  incoming-dark: "#1B2639"
  on-primary: "#FFFFFF"
  on-signal: "#0F172A"
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
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
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
    backgroundColor: "{colors.primary}"
    textColor: "{colors.on-primary}"
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

# 邻里通讯设计系统

## Overview

邻里通讯是一款面向高频日常使用、安静而精确的通讯产品。交互遵循 Apple 平台的清晰层级、直接操作、安全区、可预测反馈和无障碍规范，但不复制 Apple Messages 或微信；识别度来自深夜色顶部区域与克制使用的暖色信号黄。

The approved message-list source of truth is the fused conversation design at `/Users/joker/.codex/generated_images/019fb7a1-f640-7b23-854a-be1108d03f14/exec-0e4f47f8-7afe-4dfc-8ffe-2f024ae018d7.png`. The approved chat-detail and interaction source is the user's own ZCOOL work `ZNDc4MjUzNjg`, captured under `artifacts/reference/zcool-chat/`. Match their composition and density while translating both into this midnight-and-signal token system.

## Colors

- Midnight `#0F172A` anchors navigation, outgoing messages, selected states, and high-confidence actions.
- Signal yellow `#FFC529` is reserved for the active-tab indicator, important reminders, selected controls, and the strongest primary action. It is never decorative or a general background wash.
- The light canvas is `#F5F7FA`; message lists sit on white. In dark mode, use `#080D18` and `#111827`, not inverted light colors.
- Conversation previews use `#64748B`; metadata uses `#94A3B8`. The deeper unread red is `#D92343` and must include a number or accessible label.
- Important reminders may use the subtle `#FFF8DF` surface. Pinned conversations use a cool neutral surface plus an explicit pin label so they cannot be mistaken for alerts.

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
- The bottom bar has four stable destinations. Yellow is a clear active indicator, never an unexplained dot.

### Desktop and Web workspace

- At widths from 1024px, switch to a true desktop workspace instead of stretching the phone layout. The account rail is 72px, the conversation/contact column is 304px, the chat canvas takes the remaining width, and the optional information panel is 320px from 1280px upward.
- The account rail keeps the four product destinations, unread state, avatar, connection state and settings visually stable. Yellow only marks the active destination; it never becomes a decorative divider.
- Conversations remain dense enough for high-frequency work: search and filters stay above the list, selected rows use a quiet neutral surface, and context actions are available by right click as well as touch semantics.
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

- **Masthead:** midnight background, readable white title, one trailing `+` action. Scan, add contact, and create group live inside that menu.
- **Search:** 40pt field with magnifier, clear action, focus state, and real filtering. It must work with the keyboard and screen readers.
- **Conversation filters:** exactly `全部`, `单聊`, `群聊`; use the compact platform segmented pattern, maintain selection and scroll state, and let Dynamic Type grow the control rather than clipping labels.
- **Important notice:** conditional 44–48pt strip with icon, concise text, and a clear destination. Hide it when no actionable notice exists.
- **Conversation row:** 48pt circular avatar, 16pt semibold title, 14pt one-line preview, 12–13pt time, deeper-red unread badge, text-leading separator. A pinned row uses a cool neutral fill and an explicit `置顶` marker, never the reminder yellow. Long titles ellipsize before colliding with metadata.
- **Avatar:** people use properly cropped portrait assets, groups use a balanced 2x2 mosaic, and system threads use a purpose-made system icon. Initials are a fallback, not the default showcase state.
- **Chat bubble:** outgoing midnight with white text; incoming uses the semantic fill. Maximum width is 76 percent. Delivery, failure, retry, recall, and read state remain visible and accessible.
- **Chat navigation:** avatar/name/presence remain readable beside the back action; the single trailing ellipsis is labeled `聊天信息` and pushes a full information page. Long names truncate without displacing the action.
- **Message timeline:** show calm time separators only when the gap is meaningful. Incoming messages include a sender avatar in direct chat and sender name plus avatar in groups. Image messages use a real thumbnail and stable aspect ratio; voice messages expose duration and playback state.
- **Message action menu:** long press opens a compact, anchored context surface near the touched bubble, never a full-width bottom list. A dim dismissible barrier preserves focus; a two-line message preview establishes context. `回复`, eligible `复制`/`转发`, `收藏`, and `多选` are the first action group. Policy-gated `撤回`, local `删除本机记录`, and received-message `举报` form the second group. The surface adapts above or below the anchor at screen edges, is at most 360pt wide, scrolls before overflowing, supports 200 percent text, uses 44pt minimum targets, emits restrained haptics, and exposes a named VoiceOver route. Tapping outside closes it.
- **Composer:** expandable field with 44pt controls. Send uses the established active state only when content is sendable; otherwise the trailing control is visually quiet. The verified attachment panel contains `相册`, `拍摄`, and `文件`; emoji remains beside the field. Unverified recording and call controls are absent from production UI.
- **Chat information page:** the trailing ellipsis navigates directly to `聊天信息`, not a bottom sheet. The page starts with a responsive member/contact matrix, then grouped rows for local message search and group/contact profile. `举报会话`, contextual `加入黑名单`, and `清空本地记录` sit in the final safety/data group in red; block and clear retain explicit second confirmations, while reporting continues through its reason-and-submit flow. Group chat never shows the single-contact blacklist action.
- **Tab bar:** four destinations, midnight active icon/label in light mode, and a short yellow indicator aligned to the selected item.
- **Dialogs and destructive actions:** use a polished platform-appropriate sheet/dialog, explicit consequence copy, focus management, and confirmation for irreversible actions.

## Do's and Don'ts

- Do preserve Apple-grade hierarchy, motion restraint, focus behavior, safe areas, haptics, and accessibility semantics.
- Do use real assets or the closest matching icon-library glyph; keep icon optical weights consistent.
- Do verify light mode, dark mode, reduced motion, increased contrast, VoiceOver/TalkBack, long Chinese group names, and 200 percent text.
- Do keep the content list quiet and reserve signal yellow for meaning.
- Don't copy copyrighted screens, logos, illustrations, avatars, or source files from the ZCOOL reference.
- Don't add extra top actions beside the single `+` menu.
- Don't use thick yellow rails, ambiguous decorative dots, wide shadows, giant slogans, or generic gradient cards.
- Don't silently substitute demo data after authentication, network, or protocol failures.
- Don't introduce a new color, spacing, radius, or icon family without updating this document.
