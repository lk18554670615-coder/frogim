# 青蛙呱呱设计 QA

## Visual truth

- Message-list direction: user-authorized ZCOOL work `ZNjgxMTAzOTY` plus the selected fused target at `/Users/joker/.codex/generated_images/019fb7a1-f640-7b23-854a-be1108d03f14/exec-0e4f47f8-7afe-4dfc-8ffe-2f024ae018d7.png`.
- Conversation direction: user-authorized ZCOOL work `ZNDc4MjUzNjg`, captured under `/Users/joker/Documents/New project 2/artifacts/reference/zcool-chat/`.
- Implementation viewport: iPhone 17 Pro, iOS 26.4, native 402 x 874 pt at 3x.

## Evidence

- Message list: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-light.png`
- Message-list comparison: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/design-qa-comparison.png`
- Compact message header: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-header-compact.png`
- Message-header before/after: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-header-design-qa-comparison.png`
- Conversation: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/chat-main.png`
- Attachment panel: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/chat-attachments.png`
- Conversation menu: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/chat-more-menu.png`
- Conversation comparison: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/chat-design-qa-comparison.png`
- Message long-press menu: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/message-long-press.png`
- Long-press comparison: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/message-long-press-design-qa-comparison.png`
- Chat information page: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/chat-info.png`
- Chat-information comparison: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/chat-info-design-qa-comparison.png`
- Dark and large-text states: `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-dark.png` and `/Users/joker/Documents/New project 2/apps/mobile/artifacts/screenshots/messages-200-percent.png`

## Final comparison

The final side-by-side pass found no actionable P0/P1/P2 visual mismatch. The message header is now compact, keeps the yellow accent above the search field and clear of the create action, and hides nonessential decoration at large text sizes. The last chat fixes restored dark status-bar content on every light chat surface, reduced noisy time separators to first/day/15-minute boundaries, reserved green for true online state, and replaced the group initial with the real brand asset. Message long press now opens an anchored, dismissible, permission-aware context surface; the trailing ellipsis opens a grouped chat-information page instead of a mixed bottom sheet. Typography, safe areas, 44 pt controls, semantic colors, image crops, dark mode, extended text and the main interaction states were rechecked at the same viewport.

The complete iteration log and fidelity checklist are in `apps/mobile/design-qa.md`.

## PC Web QA — 2026-08-01

- Visual direction: the user's ZCOOL desktop reference `ZNTA4MDczMTY` was captured into `artifacts/pc-web-redesign/source-board-01.webp` through `source-board-05.webp`; the compact desktop composition, information density and master-detail rhythm were used without copying third-party assets.
- Implementation evidence: `artifacts/pc-web-redesign/implementation-web-login-1280x900.png`, `implementation-web-workspace-1280x900.png`, `implementation-web-tools-1280x760.png` and `implementation-web-account-1280x760.png`.
- Same-canvas comparison: `artifacts/pc-web-redesign/design-comparison-reference-vs-implementation.png` places the source and the built desktop workspace together for visual review.
- Desktop structure: 72 px account rail, 304 px conversation list, flexible chat canvas and a 320 px collapsible details panel at large widths. Contacts use master-detail; Explore and Me use desktop action workspaces rather than enlarged phone lists.
- Interaction pass: account navigation, conversation selection, global/current search, message context actions, attachment/emoji/voice/file entry points, profile/settings routes and light/dark appearance were exercised in the in-app browser.
- Fix cycle: the desktop login policy row was brought back inside its card at 1280 x 900; Explore and Me were rebuilt after the first browser pass exposed excessive empty space; the profile QR control received explicit light/dark foreground and surface colors.
- Automated result: Flutter analyze passed, 95 Flutter tests passed, and the production Web build passed.

## Admin console QA — 2026-08-01

- Visual direction: the compact left rail, dense operational tables and restrained card hierarchy were checked against `artifacts/pc-web-redesign/source-board-05.webp`; the implementation retains the existing 青蛙呱呱 green brand system and does not copy third-party artwork.
- Desktop browser pass: 1280 x 720, 212 px fixed navigation rail, 15 reachable navigation items, four-column metric strip, no horizontal viewport overflow. Overview and operations pages were inspected after data loading completed.
- Responsive browser pass: 390 x 844, navigation moves fully off canvas behind an accessible menu button, operations cards collapse to one 354 px column and no horizontal viewport overflow was found.
- Interaction pass: the timed-ban dialog exposes all five duration choices, restores focus, traps keyboard navigation, requires an operator reason and disables confirmation when the reason is empty.
- State pass: loading, empty, error/retry and paginated table components are present. The demo-only operations and relationship pages contain representative data; production builds cannot enable the demo selector.
- Runtime pass: no application console error was observed during the inspected desktop/mobile navigation and destructive-confirmation flow.
- Static and build checks: admin tests 16/16, TypeScript lint, production build and dependency audit passed. Backend tests, vet and the PostgreSQL-backed lifecycle/operations suite passed.

final result: passed
