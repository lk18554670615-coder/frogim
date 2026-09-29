import 'native_call_state_stub.dart'
    if (dart.library.io) 'native_call_state_io.dart'
    as platform;
export 'native_call_state_contract.dart';
import 'native_call_state_contract.dart';

NativeCallState createNativeCallState() => platform.createNativeCallState();
