// 后端 REST API 封装
import type {
  Topology,
  Device,
  Link,
  TextAnnotation,
  CliResponse,
  DeviceTypeEntry,
  SimStatus,
  SimulatePacketResult,
  IPConfig,
  SetIPConfigRequest,
} from './types';

async function request<T>(method: string, url: string, body?: unknown): Promise<T> {
  const res = await fetch(url, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    let msg = `HTTP ${res.status}`;
    try {
      const err = await res.json();
      msg = (err && err.error) || msg;
    } catch {
      // ignore JSON parse errors
    }
    throw new Error(msg);
  }
  if (res.status === 204) return undefined as T;
  const text = await res.text();
  if (!text) return undefined as T;
  return JSON.parse(text) as T;
}

export interface PingResult {
  src: string;
  dst: string;
  dst_ip: string;
  sent: number;
  received: number;
  lost: number;
  rtt_ms?: number;
  details: string[];
}

export const api = {
  listTopologies: () => request<Topology[]>('GET', '/api/topologies'),
  getTopology: (id: string) => request<Topology>('GET', `/api/topologies/${encodeURIComponent(id)}`),
  createTopology: (data: Partial<Topology>) => request<Topology>('POST', '/api/topologies', data),
  updateTopology: (id: string, data: Partial<Topology>) =>
    request<Topology>('PUT', `/api/topologies/${encodeURIComponent(id)}`, data),
  deleteTopology: (id: string) => request<void>('DELETE', `/api/topologies/${encodeURIComponent(id)}`),

  addDevice: (topoId: string, data: Partial<Device>) =>
    request<Device>('POST', `/api/topologies/${encodeURIComponent(topoId)}/devices`, data),
  updateDevice: (topoId: string, deviceId: string, data: Partial<Device>) =>
    request<Device>('PUT', `/api/topologies/${encodeURIComponent(topoId)}/devices/${encodeURIComponent(deviceId)}`, data),
  deleteDevice: (topoId: string, deviceId: string) =>
    request<void>('DELETE', `/api/topologies/${encodeURIComponent(topoId)}/devices/${encodeURIComponent(deviceId)}`),
  powerDevice: (topoId: string, deviceId: string, action: 'on' | 'off') =>
    request<Device>('POST', `/api/topologies/${encodeURIComponent(topoId)}/devices/${encodeURIComponent(deviceId)}/power`, { action }),

  addLink: (topoId: string, data: Partial<Link>) =>
    request<Link>('POST', `/api/topologies/${encodeURIComponent(topoId)}/links`, data),
  updateLink: (topoId: string, linkId: string, data: Partial<Link>) =>
    request<Link>('PUT', `/api/topologies/${encodeURIComponent(topoId)}/links/${encodeURIComponent(linkId)}`, data),
  deleteLink: (topoId: string, linkId: string) =>
    request<void>('DELETE', `/api/topologies/${encodeURIComponent(topoId)}/links/${encodeURIComponent(linkId)}`),

  executeCli: (topoId: string, deviceId: string, command: string) =>
    request<CliResponse>('POST', `/api/topologies/${encodeURIComponent(topoId)}/devices/${encodeURIComponent(deviceId)}/cli`, { command }),

  // IP 配置（方案二 Web UI / 方案三 REST API）
  getIPConfig: (topoId: string, deviceId: string, iface?: string) =>
    request<IPConfig>(
      'GET',
      `/api/topologies/${encodeURIComponent(topoId)}/devices/${encodeURIComponent(deviceId)}/ip-config${
        iface ? `?interface=${encodeURIComponent(iface)}` : ''
      }`,
    ),
  setIPConfig: (topoId: string, deviceId: string, data: SetIPConfigRequest) =>
    request<IPConfig>(
      'POST',
      `/api/topologies/${encodeURIComponent(topoId)}/devices/${encodeURIComponent(deviceId)}/ip-config`,
      data,
    ),

  addAnnotation: (topoId: string, data: Partial<TextAnnotation>) =>
    request<TextAnnotation>('POST', `/api/topologies/${encodeURIComponent(topoId)}/annotations`, data),
  updateAnnotation: (topoId: string, annoId: string, data: Partial<TextAnnotation>) =>
    request<TextAnnotation>('PUT', `/api/topologies/${encodeURIComponent(topoId)}/annotations/${encodeURIComponent(annoId)}`, data),
  deleteAnnotation: (topoId: string, annoId: string) =>
    request<void>('DELETE', `/api/topologies/${encodeURIComponent(topoId)}/annotations/${encodeURIComponent(annoId)}`),

  getDeviceTypes: () => request<DeviceTypeEntry[]>('GET', '/api/devices/types'),
  getSimStatus: (topoId?: string) =>
    request<SimStatus>('GET', `/api/sim/status${topoId ? `?topology=${encodeURIComponent(topoId)}` : ''}`),
  simulatePacket: (topoId: string, src: string, dst: string, ttl = 64) =>
    request<SimulatePacketResult>('POST', `/api/topologies/${encodeURIComponent(topoId)}/simulate-packet`, { src, dst, ttl }),
  health: () => request<{ status: string; platform: string; engine_count: number; timestamp: string }>('GET', '/health'),

  createTopologySimple: (data: { name: string; nodes: Array<{ id: string; type: string; name?: string }>; links?: Array<{ source_device: string; source_port: string; target_device: string; target_port: string }> }) =>
    request<{ id: string; name: string; device_count: number; link_count: number; created_at: string }>('POST', '/api/topology', data),
  pingTopology: (id: string, src: string, dst: string, count = 4) =>
    request<PingResult>('GET', `/api/topology/${encodeURIComponent(id)}/ping?src=${encodeURIComponent(src)}&dst=${encodeURIComponent(dst)}&count=${count}`),
};
