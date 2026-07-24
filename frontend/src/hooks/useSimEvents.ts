// SSE Hook: 订阅 /api/sim/events 推送的 PacketEvent
//
// 端点格式: "event: packet\ndata: <json>\n\n"
// 服务端还会发送 "event: connected" 表示握手成功，以及 ": heartbeat" 注释。
import { useEffect, useRef, useState } from 'react';
import type { PacketEvent } from '../types';

interface UseSimEventsResult {
  events: PacketEvent[];
  isConnected: boolean;
  error: string | null;
}

const MAX_EVENTS = 200;
const MAX_RETRIES = 5;

export function useSimEvents(topologyId: string | null): UseSimEventsResult {
  const [events, setEvents] = useState<PacketEvent[]>([]);
  const [isConnected, setIsConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const retryRef = useRef(0);
  const esRef = useRef<EventSource | null>(null);

  useEffect(() => {
    if (!topologyId) {
      setEvents([]);
      setIsConnected(false);
      setError(null);
      return;
    }

    let closed = false;
    let reconnectTimer: number | null = null;

    const connect = () => {
      if (closed) return;
      const url = `/api/sim/events?topology=${encodeURIComponent(topologyId)}`;
      const es = new EventSource(url);
      esRef.current = es;

      es.addEventListener('connected', () => {
        retryRef.current = 0;
        setIsConnected(true);
        setError(null);
      });

      es.addEventListener('packet', (e: MessageEvent) => {
        try {
          const data = JSON.parse(e.data) as PacketEvent;
          setEvents((prev) => {
            const next = prev.slice(-(MAX_EVENTS - 1));
            next.push(data);
            return next;
          });
        } catch (err) {
          console.error('useSimEvents: failed to parse packet event', err);
        }
      });

      es.onerror = () => {
        setIsConnected(false);
        try {
          es.close();
        } catch {
          // ignore
        }
        esRef.current = null;

        const attempt = retryRef.current;
        if (attempt >= MAX_RETRIES) {
          setError(`SSE 连接失败（已重试 ${MAX_RETRIES} 次）`);
          return;
        }
        retryRef.current = attempt + 1;
        // 指数退避: 1s, 2s, 4s, 8s, 16s
        const delay = Math.min(1000 * Math.pow(2, attempt), 16000);
        reconnectTimer = window.setTimeout(connect, delay);
      };
    };

    // 重置重试计数并连接
    retryRef.current = 0;
    setEvents([]);
    setError(null);
    setIsConnected(false);
    connect();

    return () => {
      closed = true;
      if (reconnectTimer !== null) {
        clearTimeout(reconnectTimer);
        reconnectTimer = null;
      }
      if (esRef.current) {
        try {
          esRef.current.close();
        } catch {
          // ignore
        }
        esRef.current = null;
      }
      retryRef.current = 0;
    };
  }, [topologyId]);

  return { events, isConnected, error };
}
