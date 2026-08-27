# 青蛙呱呱移动端设计 QA

- Source visual truth: `/Users/joker/.codex/generated_images/019fb7a1-f640-7b23-854a-be1108d03f14/exec-0e4f47f8-7afe-4dfc-8ffe-2f024ae018d7.png`
- Superseded implementation screenshot: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-light.png`
- Current implementation screenshot: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-header-compact.png`
- Header before/after evidence: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-header-design-qa-comparison.png`
- Original visual-direction comparison: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/design-qa-comparison.png`
- Additional states: `artifacts/screenshots/messages-dark.png`, `artifacts/screenshots/messages-200-percent.png`
- State: iPhone 17 Pro, iOS 26.4, logged-in demo account, conversation list, light appearance
- Viewport: native 402 x 874 pt at 3x; implementation capture 1206 x 2622 px
- Source: 853 x 1844 px. Both source and implementation were normalized to 853 x 1844 px before the 1730 x 1908 px side-by-side comparison.

## Findings

No actionable P0/P1/P2 visual mismatch remains after the final comparison.

- [P3] The reference shows one additional utility icon in the top-right. The production brief explicitly requires only one `+` menu, so the implementation intentionally removes it.
- [P3] The reference uses a large yellow diagonal field. The production brief requires the decoration to be about 35% smaller; the implementation uses a flat, responsive brand surface with no gradient. Image generation attempts produced baked transparency grids, so the clean native surface was retained deliberately instead of shipping a low-quality raster asset.
- [P3] The reference labels filters as `全部 / 个人 / 群组`; the approved product vocabulary is `全部 / 单聊 / 群聊`.

## Required Fidelity Surfaces

- Fonts and typography: system typography resolves to PingFang SC / SF Pro fallback; hierarchy, weights, truncation and line heights remain legible in the native 1x comparison and at accessibility text sizes.
- Spacing and layout rhythm: header grouping, search, filters, 16 pt list radius, 52 pt avatars, 78 pt minimum rows, dividers and bottom navigation preserve the reference hierarchy. The implementation is slightly denser by design and uses the full native safe area.
- Colors and tokens: deep navy `#0F172A`, yellow `#FFC529`, background `#F5F7FA`, preview `#64748B`, unread `#D92343`; dark-mode surfaces and active states use semantic alternatives with sufficient visible separation.
- Image quality and assets: generated 1254 px avatars are center-cropped in circular masks without stretching or transparency halos. The generated 1254 px app icon is used by Flutter, iOS, Android and web launcher outputs.
- Copy and content: standalone Chinese product copy is coherent; the conditional notice, conversation previews, unread counts and relative times use realistic data.
- Icons and interaction states: Cupertino icons are consistent; plus menu, search, filters, conversation navigation, tabs, loading, error, empty, sending, failed, retry and read states are implemented.
- Accessibility: safe areas, semantic labels, 44 pt minimum controls, VoiceOver descriptions, reduced motion, dark mode and extended text size were checked. At `accessibility-extra-large`, content truncates intentionally and persistent controls remain reachable without overflow.

## Comparison History

### Iteration 1

- [P2] Dark bottom navigation used a fixed navy active icon/label and had insufficient contrast on dark surfaces.
- [P2] Fixed 44/58 pt regions and a non-scrollable filter row could clip at 200% text size.
- [P2] The first conversation was highlighted by index instead of a real pinned/important state.
- [P3] Avatar diameter was 54 pt instead of the approved 52 pt.

Fixes applied:

- Added semantic dark active/inactive colors, dark dividers and dark pinned-row treatment.
- Replaced fixed heights with minimum constraints, allowed the bottom bar to grow, and made filters horizontally scrollable.
- Added `Conversation.pinned` across model, demo data and live API parsing; only pinned conversations receive the warm highlight.
- Changed avatar diameter to 52 pt.

Post-fix evidence:

- Light: `artifacts/screenshots/messages-light.png`
- Dark: `artifacts/screenshots/messages-dark.png`
- Extended text: `artifacts/screenshots/messages-200-percent.png`
- Final normalized comparison: `artifacts/screenshots/design-qa-comparison.png`

No new actionable P0/P1/P2 issue was found in the final side-by-side pass. A separate focused crop was not needed because each side is preserved at 853 px width in the comparison and the header, list typography, avatar edges, unread badges and bottom navigation remain readable at original detail.

### Iteration 2 — compact message header

- [P2] The 126 x 24 pt diagonal brand stripe crossed the search field and visually collided with the create action.
- [P2] Title, search and three independent filter pills consumed too much vertical space above the conversation list.
- [P2] The decorative stripe remained visible at large accessibility text sizes, where it could compete with functional content.

Fixes applied:

- Reduced the stripe to 52 x 7 pt, placed it above the search field with tested separation from the create control, and hide it above 130% text scale.
- Reduced the title to the approved 32 pt scale, tightened header padding, and standardized the search field to a 40 pt compact visual height.
- Replaced the three floating pills with one platform-style segmented surface while retaining the existing filter behavior and 44 pt interaction targets.
- Added widget assertions for a maximum 150 pt header, stripe/search non-overlap, stripe/create non-overlap, and the large-text decoration fallback.

Post-fix evidence:

- Current native capture: `artifacts/screenshots/messages-header-compact.png`
- Same-viewport before/after: `artifacts/screenshots/messages-header-design-qa-comparison.png`
- Verification: Flutter analyze, 17 widget/unit tests, and iOS Simulator build passed on 2026-07-31.

The final same-viewport comparison confirms that the conversation list begins materially higher while the title, search and filter hierarchy remains clear. No actionable P0/P1/P2 issue remains.

## Implementation Checklist

- [x] Match approved palette, hierarchy and safe-area behavior
- [x] Use the single plus menu and approved filter vocabulary
- [x] Wire visible core controls to real controller/repository actions
- [x] Verify light, dark and extended text states on iOS 26.4
- [x] Run static analysis, tests and native builds

消息首页结果: passed

---

## 对话页补充 QA

- Authorized source visual: `/Users/joker/Documents/New project 2/artifacts/reference/zcool-chat/3fe012da36a57dd3.gif`
- Related authorized boards: `0114dd07247a9a67.gif`, `165251bd7964773e.gif`, `c8764d624ac5688b.gif`
- Implementation screenshot: `artifacts/screenshots/chat-main.png`
- Attachment state: `artifacts/screenshots/chat-attachments.png`
- Legacy bottom-sheet state: `artifacts/screenshots/chat-more-menu.png`
- Anchored message menu: `artifacts/screenshots/message-long-press.png`
- Chat information page: `artifacts/screenshots/chat-info.png`
- Authorized-source message-menu comparison: `artifacts/screenshots/message-long-press-design-qa-comparison.png`
- Legacy-to-current chat-info comparison: `artifacts/screenshots/chat-info-design-qa-comparison.png`
- Final production-scope side-by-side evidence: `artifacts/screenshots/chat-design-qa-comparison.png`
- Source dimensions: 2560 x 1818 px. The primary phone was focused-cropped to 540 x 1210 px, then normalized to 853 x 1844 px.
- Implementation dimensions: 1206 x 2622 px, normalized to 853 x 1844 px.
- Viewport and state: native iPhone 17 Pro, iOS 26.4, 402 x 874 pt at 3x, logged-in group conversation; final implementation capture taken at 20:10 after production-scope controls were finalized.

### Findings

No actionable P0/P1/P2 mismatch remains in the conversation page.

- [P3] The authorized work uses a system-blue outgoing bubble; the approved 青蛙呱呱 brand brief explicitly requires midnight navy, so `#0F172A` is retained while the message alignment, tail geometry and status placement follow the reference.
- [P3] The authorized work presents a broader seven-item utility row. The production implementation exposes only the three verified attachment paths（相册、拍摄、文件）behind `+` and keeps emoji adjacent to the composer, preserving 44 pt targets on narrower devices.

### Fidelity Review

- Typography: system Chinese hierarchy, sender label, segmented time, message copy and 10 pt delivery state follow the reference scale and remain legible.
- Layout and spacing: avatar-led incoming rows, right-aligned outgoing rows, 16 pt bubbles, media crop, persistent layered composer and safe-area placement are matched.
- Colors: semantic light-gray receive bubble, midnight navy outgoing bubble, green online state, subdued timestamps and red destructive actions are consistent in light and dark appearance.
- Assets: the conversation uses real generated avatar/media assets. `weekend-coffee.png` replaces the duplicated male avatar and is also exercised as a real image-message component; there are no placeholder boxes or fake raster icons.
- Content and states: text, reply quote, image, received voice, file, recall, sending, delivered, read and failed/retry components exist. Long-press actions separate ordinary and destructive actions; reply、转发、收藏与多选均使用真实 controller/repository 路径。
- Interactions: production UI removes call、recording、pin and notification-mute controls until their external contracts can be verified. Local search, copy, reply, retry, recall, local delete, local clear, report and block use actual local/controller/repository paths.
- Accessibility: header state, incoming message semantics, image labels, tooltips, 44 pt controls, scroll keyboard dismissal and consequence copy were verified in native simulator accessibility output.

The 853 px-wide focused source and implementation panels make header, bubble, image, voice, timestamp, status and composer detail readable, so no additional crop is required.

### Conversation Comparison History

Initial native capture findings:

- [P1] The light conversation surface inherited light status-bar content, making time, Wi-Fi and battery nearly invisible.
- [P2] Five-minute time separators produced unnecessary visual noise at 19:18, 19:27 and 19:32.
- [P2] The group member count used green online/success color.
- [P2] The group header used an initial-only avatar instead of a real asset.

Fixes and post-fix evidence:

- `ChatScreen` now applies dark status-bar content; `HomeScreen` explicitly restores light content over the navy header and dark content on light tabs.
- Time separators now appear only at the first message, day boundaries, or gaps of at least 15 minutes.
- Group member count uses secondary-label color; green is reserved for a direct contact who is actually online.
- Group header uses the real 青蛙呱呱 brand asset. The updated `chat-main.png` shows all four fixes at the same iPhone 17 Pro viewport.

The post-fix side-by-side pass found no remaining P0/P1/P2 mismatch.

### Production-scope visual regression

- Rebuilt and installed the final iOS Simulator app after media and authentication hardening.
- `chat-main.png` confirms the call button is absent and the composer retains balanced spacing.
- `chat-design-qa-comparison.png` was regenerated from the original authorized ZCOOL source and the latest 20:10 `chat-main.png`, with both panels normalized to 853 x 1844 px. This supersedes the earlier 19:49 comparison and is the final production-scope comparison.
- `chat-attachments.png` confirms only the three working system-picker actions are exposed.
- `chat-more-menu.png` is retained only as legacy evidence of the oversized bottom sheet that mixed navigation and destructive actions.
- `message-long-press.png` confirms the replacement is anchored to the selected message, preserves message context, keeps the five common actions together, and separates local deletion/reporting below a divider.
- `chat-info.png` confirms the top-right action now navigates to a dedicated information page with contact/member, content-search, and safety/data groups instead of opening another transient menu.
- `message-long-press-design-qa-comparison.png` compares the authorized ZCOOL conversation source with the final anchored menu at matched 853 x 1844 px panels.
- `chat-info-design-qa-comparison.png` compares the legacy bottom-sheet state with the final information architecture at matched 853 x 1844 px panels.
- Accessibility output confirms the same visible labels and 44 pt controls on iPhone 17 Pro / iOS 26.4.

### Message menu and chat-info acceptance

- [x] Long press opens at the message position with scale/fade motion, dimming, edge adaptation and outside-tap dismissal.
- [x] Reply、copy、forward、favorite and multi-select use the compact primary grid; recall/delete/report remain policy-aware secondary actions.
- [x] The top-right `聊天信息` route provides real search/profile/report/block/clear paths and confirmation copy for destructive effects.
- [x] Post-capture P1 fixed: `GlassAppBar` now declares transparent status-bar color plus theme-aware icon/brightness values; the final light `chat-info.png` shows readable black time, Wi-Fi and battery indicators.
- [x] VoiceOver exposes `显示消息操作`; route and button labels were verified from the native iOS accessibility tree.
- [x] Widget coverage includes direct/group route differences, barrier dismissal, multi-select, dark appearance and 200% text without overflow.

No P0/P1/P2 visual, interaction or accessibility issue remains in these two redesigned surfaces.

final result: passed
