import 'system_call_service_contract.dart';
import 'system_call_service_stub.dart'
    if (dart.library.io) 'system_call_service_native.dart'
    as implementation;

SystemCallService createSystemCallService() =>
    implementation.createSystemCallService();
