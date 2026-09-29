import 'dart:convert';
import 'package:crypto/crypto.dart';
import '../proto/proto.dart';

class Options {
  String? uid, token;
  // Local storage only. Never change the wire UID to namespace a database.
  String? databaseNamespace;
  String get databaseIdentity => databaseNamespace == null
      ? uid!
      : 'tenant_${sha256.convert(utf8.encode(databaseNamespace!))}';
  String? addr; // connect address IP:PORT
  int protoVersion = 0x04; // protocol version
  int deviceFlag = 0;
  bool debug = true;
  Function(Function(String addr) complete)?
      getAddr; // async get connect address
  Proto proto = Proto();
  Options();

  Options.newDefault(this.uid, this.token, {this.addr});
}
