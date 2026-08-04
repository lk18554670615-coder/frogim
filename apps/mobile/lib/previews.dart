import 'package:flutter/material.dart';
import 'package:flutter/widget_previews.dart';

import 'core/app_controller.dart';
import 'core/app_theme.dart';
import 'core/models.dart';
import 'data/demo_repository.dart';
import 'ui/screens/chat_screen.dart';
import 'ui/screens/home_screen.dart';
import 'ui/screens/login_screen.dart';

Widget _frame(Widget child, {Brightness brightness = Brightness.light}) =>
    MaterialApp(theme: buildLinliTheme(brightness), home: child);

AppController _homeController() {
  final now = DateTime(2026, 7, 31, 12, 30);
  return AppController(DemoImRepository())
    ..connected = true
    ..contacts = DemoImRepository.people
    ..conversations = [
      Conversation(
        id: 'c-team',
        title: '邻里产品小组',
        subtitle: '安然：新的动效稿已上传',
        updatedAt: now,
        kind: ConversationKind.group,
        unread: 3,
        members: DemoImRepository.people.take(4).toList(),
      ),
      Conversation(
        id: 'c-linyu',
        title: '林屿',
        subtitle: '晚点一起看下新版本？',
        updatedAt: now.subtract(const Duration(minutes: 18)),
        kind: ConversationKind.direct,
        members: [DemoImRepository.people.first],
      ),
    ];
}

Conversation _chatConversation() => Conversation(
  id: 'c-team',
  title: '邻里产品小组',
  subtitle: '安然：新的动效稿已上传',
  updatedAt: DateTime(2026, 7, 31),
  kind: ConversationKind.group,
  members: DemoImRepository.people.take(4).toList(),
);

@Preview(name: '登录 · Light', group: 'Apple HIG', size: Size(390, 844))
Widget loginPreview() =>
    _frame(LoginScreen(controller: AppController(DemoImRepository())));

@Preview(
  name: '登录 · Dark',
  group: 'Apple HIG',
  size: Size(390, 844),
  brightness: Brightness.dark,
)
Widget loginDarkPreview() => _frame(
  LoginScreen(controller: AppController(DemoImRepository())),
  brightness: Brightness.dark,
);

@Preview(name: '消息主页 · Light', group: 'Apple HIG', size: Size(390, 844))
Widget homePreview() =>
    _frame(HomeScreen(controller: _homeController(), onToggleTheme: () {}));

@Preview(
  name: '消息主页 · Dark',
  group: 'Apple HIG',
  size: Size(390, 844),
  brightness: Brightness.dark,
)
Widget homeDarkPreview() => _frame(
  HomeScreen(controller: _homeController(), onToggleTheme: () {}),
  brightness: Brightness.dark,
);

@Preview(name: '群聊 · Light', group: 'Apple HIG', size: Size(390, 844))
Widget chatPreview() => _frame(
  ChatScreen(
    controller: AppController(DemoImRepository()),
    conversation: _chatConversation(),
  ),
);

@Preview(
  name: '群聊 · Dark',
  group: 'Apple HIG',
  size: Size(390, 844),
  brightness: Brightness.dark,
)
Widget chatDarkPreview() => _frame(
  ChatScreen(
    controller: AppController(DemoImRepository()),
    conversation: _chatConversation(),
  ),
  brightness: Brightness.dark,
);

@Preview(name: '消息气泡状态', group: 'Components', size: Size(390, 320))
Widget messageStatesPreview() => _frame(
  Scaffold(
    body: ListView(
      padding: const EdgeInsets.all(16),
      children: [
        MessageBubble(
          message: ChatMessage(
            id: '1',
            conversationId: 'c',
            senderId: 'u',
            senderName: '林屿',
            text: '新的交互已经准备好了。',
            sentAt: DateTime(2026, 7, 31, 12, 20),
            isMine: false,
          ),
        ),
        MessageBubble(
          message: ChatMessage(
            id: '2',
            conversationId: 'c',
            senderId: 'me',
            senderName: '我',
            text: '收到，我现在来体验。',
            sentAt: DateTime(2026, 7, 31, 12, 21),
            isMine: true,
            status: MessageStatus.read,
          ),
        ),
      ],
    ),
  ),
);
