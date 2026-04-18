import type { SessionLogEntry } from "../types/session";
import type { BridgeEvent } from "../types/ws";

export function buildLogEntryFromEvent(event: BridgeEvent): SessionLogEntry {
  switch (event.type) {
    case "log.entry":
      return event.payload.entry;
    case "session.created":
      return createLogEntry(
        event.payload.id,
        "info",
        "bridge",
        `会话 ${event.payload.callId} 已创建`,
        event.payload.updatedAt,
      );
    case "session.updated":
      return createLogEntry(
        event.payload.id,
        "info",
        "bridge",
        `会话 ${event.payload.callId} 更新为 ${event.payload.state}`,
        event.payload.updatedAt,
      );
    case "session.closed":
      return createLogEntry(
        event.payload.id,
        "warn",
        "bridge",
        `会话 ${event.payload.callId} 已关闭`,
        event.payload.closedAt ?? event.payload.updatedAt,
      );
    case "session.transcript.partial":
      return {
        id: `${event.payload.sessionId}-stt-partial`,
        level: "info",
        source: "stt",
        message: `实时转写: ${event.payload.transcript.text}`,
        createdAt: event.payload.transcript.createdAt,
      };
    case "session.transcript.final":
      return createLogEntry(
        event.payload.sessionId,
        "info",
        "stt",
        `收到最终转写: ${event.payload.transcript.text}`,
        event.payload.transcript.createdAt,
      );
    case "session.tts.started":
      return createLogEntry(
        event.payload.sessionId,
        "info",
        "tts",
        event.payload.text ? `TTS 开始播报: ${event.payload.text}` : "TTS 开始播报",
        event.payload.updatedAt,
      );
    case "session.tts.stopped":
      return createLogEntry(
        event.payload.sessionId,
        "warn",
        "tts",
        event.payload.reason ? `TTS 已停止: ${event.payload.reason}` : "TTS 已停止",
        event.payload.updatedAt,
      );
    default:
      return createLogEntry("unknown-session", "info", "system", "收到未知事件", new Date().toISOString());
  }
}

export function shouldRecordDashboardLog(event: BridgeEvent): boolean {
  return event.type !== "session.transcript.partial";
}

function createLogEntry(
  sessionId: string,
  level: SessionLogEntry["level"],
  source: SessionLogEntry["source"],
  message: string,
  createdAt: string,
): SessionLogEntry {
  return {
    id: `${sessionId}-${source}-${level}-${createdAt}`,
    level,
    source,
    message,
    createdAt,
  };
}
