import 'dart:io';
import 'native_call_state_contract.dart';
import 'native_call_state_channel.dart';

NativeCallState createNativeCallState() => Platform.isAndroid || Platform.isIOS
    ? ChannelNativeCallState()
    : NoNativeCallState();
