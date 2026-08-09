# 系统来电插件通过 Intent/反射传递固定字段，正式包混淆时必须保留。
-keep class com.hiennv.flutter_callkit_incoming.** { *; }

# 个推的依赖链包含 Apache Tika 的桌面 XML 解析分支；Android 运行路径不可达，
# 但 R8 仍会解析该方法签名。仅忽略缺失的异常类型，不放宽其他压缩规则。
-dontwarn javax.xml.stream.XMLStreamException
