import 'wukong_gateway_contract.dart';
import 'wukong_gateway_stub.dart'
    if (dart.library.io) 'wukong_gateway_io.dart'
    if (dart.library.js_interop) 'wukong_gateway_web.dart'
    as platform;

export 'wukong_gateway_contract.dart';

WukongGateway createWukongGateway({WukongDataSource? dataSource}) =>
    platform.createWukongGateway(dataSource: dataSource);
