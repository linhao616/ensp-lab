// LeftPanel - 左侧统一标签栏
// 整合：设备库（添加设备）/ 连线种类（连线类型选择 + 连线清单）
// 取代原先独立的右侧「拓扑资源」面板与设备列表 Tab，所有信息收拢到左侧。
import { useState } from 'react';
import {
  type Link,
  type Device,
  type DeviceTypeEntry,
  type DeviceType,
  type LinkTypeMode,
  DEVICE_META,
} from '../types';
import ConnectionList from './ConnectionList';
import LinkTypes from './LinkTypes';

interface LeftPanelProps {
  deviceTypes: DeviceTypeEntry[];
  onAddDeviceClick: (type: DeviceType) => void;
  onAddAnnotation: () => void;
  onAddVxlanTemplate: () => void;
  hasTopology: boolean;
  links: Link[];
  devices: Record<string, Device>;
  selectedLinkId: string | null;
  onSelectLink: (id: string) => void;
  onLocateLink: (id: string) => void;
  onDeleteLink: (id: string) => void;
  linkTypeMode: LinkTypeMode;
  onSelectLinkType: (m: LinkTypeMode) => void;
}

type TabKey = 'devices' | 'links';

export default function LeftPanel(props: LeftPanelProps) {
  const [tab, setTab] = useState<TabKey>('devices');

  return (
    <div className="lp-root">
      <div className="lp-tabs">
        <button
          type="button"
          className={`lp-tab${tab === 'devices' ? ' active' : ''}`}
          onClick={() => setTab('devices')}
        >
          设备库
        </button>
        <button
          type="button"
          className={`lp-tab${tab === 'links' ? ' active' : ''}`}
          onClick={() => setTab('links')}
        >
          连线种类
        </button>
      </div>

      <div className="lp-body">
        {tab === 'devices' && (
          <div className="lp-palette">
            {props.deviceTypes.map((d) => {
              const meta = DEVICE_META[d.type as DeviceType];
              if (!meta) return null;
              return (
                <div
                  key={d.type}
                  className="device-item"
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData('deviceType', d.type);
                    e.dataTransfer.effectAllowed = 'copy';
                  }}
                  onClick={() => {
                    if (props.hasTopology) props.onAddDeviceClick(d.type as DeviceType);
                  }}
                  title={`拖拽到画布或点击进入添加模式: ${d.name}`}
                >
                  <div className="device-icon" style={{ background: meta.color }}>
                    {meta.icon}
                  </div>
                  <span className="device-name">{d.name}</span>
                </div>
              );
            })}
            <div className="toolbar-divider" />
            <div className="panel-toolbar">
              <button
                className="btn btn-secondary btn-sm"
                onClick={props.onAddAnnotation}
                disabled={!props.hasTopology}
              >
                + 添加标注
              </button>
              <button
                className="btn btn-secondary btn-sm"
                onClick={props.onAddVxlanTemplate}
                disabled={!props.hasTopology}
                title="插入预填的 VXLAN 规划说明（纯 TXT）"
              >
                📋 VXLAN 模板
              </button>
            </div>
          </div>
        )}

        {tab === 'links' && (
          <div className="lp-links">
            <LinkTypes activeType={props.linkTypeMode} onSelect={props.onSelectLinkType} />
            <div className="lp-subhead">连线清单 ({props.links.length})</div>
            <div className="lp-connlist">
              <ConnectionList
                links={props.links}
                devices={props.devices}
                selectedLinkId={props.selectedLinkId}
                onSelect={props.onSelectLink}
                onLocate={props.onLocateLink}
                onDelete={props.onDeleteLink}
              />
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
