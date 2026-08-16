# Design System

## Overview

青蛙呱呱控制台采用限制性配色和高信息密度产品布局。具体场景是：值班运营在明亮办公室中持续查看举报队列和系统状态，需要屏幕安静、信息明确、风险动作不含糊。

## Theme

- Light-first product interface
- Restrained color strategy
- Solid surfaces, subtle dividers, no decorative gradients

## Color Palette

- Primary: `oklch(0.52 0.13 160)`，用于主要动作、当前导航和可交互焦点
- Primary strong: `oklch(0.40 0.105 160)`，用于品牌侧栏和高对比状态
- Background: `oklch(1 0 0)`
- Surface: `oklch(0.975 0.006 160)`
- Ink: `oklch(0.20 0.02 160)`
- Muted: `oklch(0.48 0.025 160)`
- Accent: `oklch(0.64 0.17 50)`，仅用于风险提醒与待办强调
- Success, warning, danger and info each have filled, soft, and text variants

## Typography

使用 `Inter, PingFang SC, Microsoft YaHei, system-ui, sans-serif` 单一字体栈。页面标题 24px/700，区块标题 16px/650，正文 14px/400，表格和标签 13px。数字使用等宽特性保持扫描稳定。

## Layout

桌面端使用 248px 固定侧栏和内容工作区。小于 880px 时侧栏转为抽屉，表格允许水平滚动，指标按 2 列或 1 列重排。内容宽度不做狭窄居中，数据工作台应利用可用空间。

## Components

- Buttons: 8px radius, 36px height, explicit text labels
- Inputs: 9px radius, visible focus ring, 40px default height
- Panels: 12px radius, border only, no wide decorative shadow
- Tables: 48px rows, sticky semantic header where useful
- Status badges: text plus dot or icon, never color alone
- Dialogs: only for destructive confirmation or focused editing
- Loading: skeleton rows and metric placeholders
- Empty states: explain why empty and provide next useful action

## Motion

State transitions use 160–220ms ease-out. Navigation and drawers may translate; content remains visible by default. All motion is disabled or reduced under `prefers-reduced-motion`.
