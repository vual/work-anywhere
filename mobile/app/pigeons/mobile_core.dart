import 'package:pigeon/pigeon.dart';

class CoreReply {
  CoreReply({required this.ok, required this.payload});

  bool ok;
  String payload;
}

@HostApi()
abstract class MobileCoreApi {
  @async
  CoreReply pair(String uri);

  @async
  CoreReply restoreStored();

  CoreReply persistCredentials();

  CoreReply deleteStoredCredentials();

  @async
  CoreReply call(String method, String paramsJson, int timeoutMilliseconds);

  CoreReply status();

  void close();
}
