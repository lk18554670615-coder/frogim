import 'package:http/http.dart' as http;

import 'client_upgrade_installer_contract.dart';
import 'client_upgrade_installer_stub.dart'
    if (dart.library.io) 'client_upgrade_installer_io.dart'
    as platform;

export 'client_upgrade_installer_contract.dart';

ClientUpgradeInstaller createClientUpgradeInstaller({http.Client? client}) =>
    platform.createClientUpgradeInstaller(client: client);
