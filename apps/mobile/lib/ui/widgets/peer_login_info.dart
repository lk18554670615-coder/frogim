import 'dart:async';

import 'package:flutter/foundation.dart' show kIsWeb;
import 'package:flutter/material.dart';

import '../../core/app_controller.dart';
import '../../core/app_theme.dart';
import '../../core/models.dart';
import '../../core/peer_login_info.dart';
import '../../data/live_repository.dart' show ImApiException;

bool showPeerLoginInfoFor(
  BuildContext context,
  Conversation conversation, {
  required AppUser? viewer,
  bool? webOverride,
}) {
  if (!(webOverride ?? kIsWeb) || viewer?.canViewFriendLoginIP != true) {
    return false;
  }
  if (conversation.kind != ConversationKind.direct ||
      conversation.isBusinessChannel) {
    return false;
  }
  return useLinliDesktopLayout(MediaQuery.sizeOf(context).width);
}

/// Keeps IP data only in this visible conversation header, never in user or
/// message caches. Authorization is always checked by the conversation API.
class PeerLoginInfoLabel extends StatefulWidget {
  const PeerLoginInfoLabel({
    super.key,
    required this.controller,
    required this.conversationId,
  });
  final AppController controller;
  final String conversationId;

  @override
  State<PeerLoginInfoLabel> createState() => _PeerLoginInfoLabelState();
}

class _PeerLoginInfoLabelState extends State<PeerLoginInfoLabel>
    with WidgetsBindingObserver {
  PeerLoginInfo? _info;
  bool _failed = false;
  bool _active = false;
  bool _foreground = true;
  bool _inFlight = false;
  bool _permissionAllowed = false;
  bool _accessDenied = false;
  int _generation = 0;
  String? _accountId;
  Timer? _timer;
  StreamSubscription<ImEvent>? _eventSubscription;

  @override
  void initState() {
    super.initState();
    _foreground =
        WidgetsBinding.instance.lifecycleState == null ||
        WidgetsBinding.instance.lifecycleState == AppLifecycleState.resumed;
    _accountId = widget.controller.currentUser?.id;
    _permissionAllowed =
        widget.controller.currentUser?.canViewFriendLoginIP == true;
    widget.controller.addListener(_accountChanged);
    _subscribeToRelationshipChanges();
    WidgetsBinding.instance.addObserver(this);
  }

  void _subscribeToRelationshipChanges() {
    _eventSubscription?.cancel();
    _eventSubscription = widget.controller.repository.events.listen((event) {
      if (!mounted || event.type != ImEventType.friendChanged) return;
      setState(_reset);
      _sync();
    });
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    _active =
        TickerMode.valuesOf(context).enabled &&
        (ModalRoute.isCurrentOf(context) ?? true);
    _sync();
  }

  bool get _enabled =>
      _active &&
      _foreground &&
      widget.controller.authenticated &&
      _accountId != null &&
      _permissionAllowed &&
      !_accessDenied;

  void _sync() {
    if (!_enabled) {
      _reset();
      return;
    }
    if (_timer != null) return;
    _timer = Timer.periodic(
      const Duration(seconds: 30),
      (_) => unawaited(_refresh()),
    );
    unawaited(_refresh());
  }

  void _reset() {
    _timer?.cancel();
    _timer = null;
    _generation++;
    _inFlight = false;
    _info = null;
    _failed = false;
    _accessDenied = false;
  }

  void _accountChanged() {
    final id = widget.controller.currentUser?.id;
    final allowed = widget.controller.currentUser?.canViewFriendLoginIP == true;
    if (id == _accountId &&
        allowed == _permissionAllowed &&
        widget.controller.authenticated) {
      return;
    }
    setState(() {
      _accountId = id;
      _permissionAllowed = allowed;
      _reset();
    });
    _sync();
  }

  @override
  void didUpdateWidget(PeerLoginInfoLabel oldWidget) {
    super.didUpdateWidget(oldWidget);
    if (oldWidget.controller == widget.controller &&
        oldWidget.conversationId == widget.conversationId) {
      return;
    }
    oldWidget.controller.removeListener(_accountChanged);
    widget.controller.addListener(_accountChanged);
    _subscribeToRelationshipChanges();
    _accountId = widget.controller.currentUser?.id;
    _permissionAllowed =
        widget.controller.currentUser?.canViewFriendLoginIP == true;
    _reset();
    _sync();
  }

  @override
  void didChangeAppLifecycleState(AppLifecycleState state) {
    setState(() => _foreground = state == AppLifecycleState.resumed);
    _sync();
  }

  Future<void> _refresh() async {
    if (!mounted || !_enabled || _inFlight) return;
    _inFlight = true;
    final generation = _generation;
    try {
      final info = await widget.controller.repository
          .peerLoginInfo(widget.conversationId)
          .timeout(const Duration(seconds: 12));
      if (!mounted || generation != _generation || !_enabled) return;
      setState(() {
        _info = info;
        _failed = false;
      });
    } catch (error) {
      if (!mounted || generation != _generation || !_enabled) return;
      if (error is ImApiException &&
          (error.statusCode == 403 || error.statusCode == 404)) {
        setState(() {
          _info = null;
          _failed = false;
          _accessDenied = true;
          _timer?.cancel();
          _timer = null;
        });
        return;
      }
      setState(() {
        _info = null;
        _failed = true;
      });
    } finally {
      if (generation == _generation) _inFlight = false;
    }
  }

  @override
  void dispose() {
    _reset();
    _eventSubscription?.cancel();
    widget.controller.removeListener(_accountChanged);
    WidgetsBinding.instance.removeObserver(this);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    if (!_enabled ||
        widget.controller.currentUser?.canViewFriendLoginIP != true) {
      return const SizedBox.shrink();
    }
    final value = _info == null
        ? (_failed ? '暂不可用' : '正在查询…')
        : (_info!.lastLoginIp.isEmpty ? '未记录' : _info!.lastLoginIp);
    final region = _info?.regionLabel ?? (_failed ? '暂不可用' : '正在查询…');
    final style = Theme.of(
      context,
    ).textTheme.labelSmall?.copyWith(fontSize: 11, height: 1.3);
    return Tooltip(
      message:
          '最近一次成功登录时记录的 IP，可能来自任意一端，并非实时连接地址。\n归属地仅供参考。\nIP：$value\n归属地：$region',
      child: Column(
        crossAxisAlignment: CrossAxisAlignment.start,
        mainAxisSize: MainAxisSize.min,
        children: [
          Row(
            children: [
              Expanded(
                child: SingleChildScrollView(
                  scrollDirection: Axis.horizontal,
                  child: SelectableText(
                    '最近登录 IP：$value',
                    key: const Key('peer-login-ip'),
                    style: style,
                    maxLines: 1,
                  ),
                ),
              ),
              if (_failed)
                InkResponse(
                  key: const Key('peer-login-ip-retry'),
                  onTap: () => unawaited(_refresh()),
                  child: const Tooltip(
                    message: '重新查询',
                    child: Icon(Icons.refresh, size: 16),
                  ),
                ),
            ],
          ),
          Text(
            '归属地：$region',
            key: const Key('peer-login-region'),
            style: style,
            maxLines: 1,
            overflow: TextOverflow.ellipsis,
          ),
        ],
      ),
    );
  }
}
