import { useSyncExternalStore } from "react";
import { bridgeSocket } from "./ws";

export function useBridgeConnection() {
  return useSyncExternalStore(
    (listener) => bridgeSocket.subscribeConnection(listener),
    () => bridgeSocket.getConnectionSnapshot(),
    () => bridgeSocket.getConnectionSnapshot(),
  );
}
