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

  @async
  CoreReply selectPairing(String daemonId);

  @async
  CoreReply setPairingEnabled(String daemonId, bool enabled);

  @async
  CoreReply updatePairing(String daemonId, String displayName, String relayUrl);

  @async
  CoreReply removePairing(String daemonId);

  @async
  CoreReply reorderPairings(List<String> daemonIds);

  @async
  CoreReply probePairing(String daemonId);

  CoreReply persistCredentials();

  CoreReply deleteStoredCredentials();

  @async
  CoreReply call(String method, String paramsJson, int timeoutMilliseconds);

  CoreReply status();

  void close();
}
